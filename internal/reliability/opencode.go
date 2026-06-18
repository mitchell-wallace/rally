package reliability

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	opencodeRetryAfterRe = regexp.MustCompile(`(?i)retry.?(?:after|in)\s+(\d+)\s*(?:s|sec|seconds?)`)

	// opencodeFlatErrorRe extracts the flat server-log carrier
	// `error.error="<Wrapper>: <message>"`. Confirmed third-pass (spike-2): the
	// structured provider error for subscription usage limits reaches only the
	// server log as this flat field, never a nested data.message on stdout.
	opencodeFlatErrorRe = regexp.MustCompile(`error\.error="([^"]*)"`)

	// opencode-specific reset parsing (do not overload the gemini regex).
	// Space-separated spans ("Resets in 7 days", "... 5 hour", "... 30 minutes")
	// and absolute timestamps ("reset at ...", "will reset at ...") with no
	// timezone marker.
	opencodeResetAtRe   = regexp.MustCompile(`(?i)reset\s+at\s+(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2})`)
	opencodeResetSpanRe = regexp.MustCompile(`(?i)(\d+)\s+(day|hour|minute|second)s?`)
)

// opencodeResetLayout matches opencode's local-time reset timestamp; the value
// carries no timezone marker so it is parsed in time.Local and treated as
// approximate (benching slightly long is the safe direction).
const opencodeResetLayout = "2006-01-02 15:04:05"

type opencodeErrorEvent struct {
	Type  string `json:"type"`
	Error *struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
			Ref     string `json:"ref"`
		} `json:"data"`
	} `json:"error"`
}

func ParseOpencodeError(stderr string, model string) *FailureEvidence {
	if stderr == "" {
		return nil
	}

	var eventError *opencodeErrorEvent
	sawErrorEvent := false
	flatError := ""
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The flat server-log carrier is a logfmt line, not JSON; scan for it
		// before attempting a JSON decode.
		if flatError == "" {
			if m := opencodeFlatErrorRe.FindStringSubmatch(line); m != nil {
				flatError = m[1]
			}
		}
		var ev opencodeErrorEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "error" {
			sawErrorEvent = true
			if ev.Error != nil {
				eventError = &ev
			}
			break
		}
	}

	if !sawErrorEvent && flatError == "" {
		return nil
	}

	var fe FailureEvidence
	fe.Provider = extractProviderFromModel(model)
	fe.RawSignal = truncateSignal(stderr, 256)

	if eventError == nil && flatError == "" {
		fe.Category = CategoryAgentError
		return &fe
	}

	name := ""
	msg := ""
	if eventError != nil {
		name = eventError.Error.Name
		msg = eventError.Error.Data.Message
	}

	switch {
	case msg != "":
		fe.Message = truncateSignal(msg, 200)
	case flatError != "":
		fe.Message = truncateSignal(flatError, 200)
	case name != "":
		fe.Message = truncateSignal(name, 200)
	}

	lowerName := strings.ToLower(name)
	// Content checks span the structured data.message and the flat server-log
	// error.error value so usage-limit signatures match across opencode's
	// AI_APICallError / AI_RetryError / UnknownError wrappers.
	lowerMsg := strings.ToLower(strings.TrimSpace(msg + " " + flatError))

	switch {
	case containsAny(lowerName, "usagelimit", "quotaexceeded", "resourceexhausted") ||
		containsAny(lowerMsg, "usage limit", "quota exceeded", "resource_exhausted",
			"usage limit reached", "monthly usage limit", "usage limit reached for"):
		fe.Category = CategoryUsageLimit
		fe.StatusCode = 429
		if dur, at := parseOpencodeReset(lowerMsg); at != nil {
			fe.ResetAt = at
		} else if dur > 0 {
			fe.ResetAfter = dur
		} else if d := parseResetsIn(lowerMsg); d > 0 {
			fe.ResetAfter = d
		}

	case containsAny(lowerName, "ratelimit", "toomanyrequests") ||
		containsAny(lowerMsg, "rate limit", "too many requests"):
		fe.Category = CategoryShortRateLimit
		fe.StatusCode = 429
		fe.RetryAfter = parseRetryAfterSeconds(lowerMsg)
		if fe.RetryAfter == 0 {
			fe.RetryAfter = 60 * time.Second
		}

	case containsAny(lowerName, "auth", "permission", "unauthorized", "forbidden") ||
		containsAny(lowerMsg, "authentication", "invalid api key", "unauthorized", "forbidden"):
		fe.Category = CategoryAuthOrProxy
		fe.StatusCode = 401

	case containsAny(lowerName, "modelnotfound", "notfounderror") && containsAny(lowerMsg, "model") ||
		containsAny(lowerMsg, "model not found", "model does not exist"):
		fe.Category = CategoryInvalidModel
		fe.StatusCode = 404

	case containsAny(lowerName, "overload") ||
		containsAny(lowerMsg, "overloaded", "503"):
		fe.Category = CategoryProviderOverloaded
		fe.StatusCode = 503
		fe.RetryAfter = 30 * time.Second

	default:
		fe.Category = CategoryAgentError
	}

	return &fe
}

func extractProviderFromModel(model string) string {
	if model == "" {
		return ""
	}
	parts := strings.SplitN(model, "/", 2)
	return parts[0]
}

func parseRetryAfterSeconds(s string) time.Duration {
	m := opencodeRetryAfterRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	var secs int
	for _, ch := range m[1] {
		if ch >= '0' && ch <= '9' {
			secs = secs*10 + int(ch-'0')
		}
	}
	if secs == 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// parseOpencodeReset extracts opencode's reset timing from a usage-limit
// message. It prefers an absolute timestamp (the authoritative reset, returned
// as ResetAt) over a space-separated span ("7 days" / "5 hour" / "30 minutes",
// returned as a relative duration). The absolute timestamp carries no timezone
// marker, so it is parsed in time.Local and is approximate. Returns (0, nil)
// when neither shape is present, leaving the caller to fall back.
func parseOpencodeReset(s string) (time.Duration, *time.Time) {
	if m := opencodeResetAtRe.FindStringSubmatch(s); len(m) == 2 {
		// Collapse any internal whitespace so the fixed layout matches.
		ts := strings.Join(strings.Fields(m[1]), " ")
		if t, err := time.ParseInLocation(opencodeResetLayout, ts, time.Local); err == nil {
			return 0, &t
		}
	}
	if m := opencodeResetSpanRe.FindStringSubmatch(s); len(m) == 3 {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return 0, nil
		}
		var unit time.Duration
		switch strings.ToLower(m[2]) {
		case "day":
			unit = 24 * time.Hour
		case "hour":
			unit = time.Hour
		case "minute":
			unit = time.Minute
		case "second":
			unit = time.Second
		}
		return time.Duration(n) * unit, nil
	}
	return 0, nil
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
