package reliability

import (
	"encoding/json"
	"log"
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

	// opencode-specific reset parsing uses its own shapes.
	// Space-separated spans ("Resets in 7 days", "... 5 hour", "... 30 minutes")
	// and absolute timestamps ("reset at ...", "will reset at ...") with no
	// timezone marker.
	opencodeResetAtRe = regexp.MustCompile(`(?i)reset\s+at\s+(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2})(?:\s*(Z|z|[+-]\d{2}:?\d{2}))?`)

	// opencodeWindowRe matches the z.ai coding-plan window phrase "Usage
	// limit reached for 5 hour(s)" (also minutes/seconds/days), which states
	// the length of the provider's rolling usage window. The companion reset
	// stamp is naive Asia/Shanghai local time.
	opencodeWindowRe    = regexp.MustCompile(`(?i)usage\s+limit\s+reached\s+for\s+(\d+)\s+(day|hour|minute|second)s?\b`)
	opencodeResetSpanRe = regexp.MustCompile(`(?i)(\d+)\s+(day|hour|minute|second)s?`)
)

// opencodeResetLayout matches opencode's local-time reset timestamp. A naive
// value is interpreted per parseOpencodeReset: z.ai windowed stamps in the
// fixed UTC+8 zone, other providers in time.Local as an approximation.
const opencodeResetLayout = "2006-01-02 15:04:05"

// zaiResetZone is the fixed zone z.ai emits naive reset stamps in:
// Asia/Shanghai is UTC+8 year-round (no DST), and z.ai's "Your limit will
// reset at YYYY-MM-DD HH:MM:SS" carries no offset of its own.
var zaiResetZone = time.FixedZone("UTC+8", 8*3600)

// opencodeNow is the clock used by opencode reset parsing; a var so tests can
// pin the present when checking window caps and expired resets.
var opencodeNow = time.Now

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
		fe.Category = CategoryUnidentifiedIssue
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
	if flatError == "" && opencodeWrapperOnlyUnknownError(lowerName, lowerMsg) {
		return nil
	}

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

func opencodeWrapperOnlyUnknownError(lowerName, lowerMsg string) bool {
	if lowerName != "unknownerror" {
		return false
	}
	msg := strings.TrimSpace(lowerMsg)
	msg = strings.TrimSuffix(msg, ".")
	msg = strings.Join(strings.Fields(msg), " ")
	switch msg {
	case "", "unexpected server error", "unexpected server error. check server logs for details":
		return true
	default:
		return false
	}
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
// returned as a relative duration).
//
// Timezone handling: a stamp carrying its own zone designator (Z or ±HH:MM)
// keeps that offset. A naive stamp in the z.ai windowed shape ("Usage limit
// reached for N hour(s). Your limit will reset at …") is parsed in z.ai's
// fixed UTC+8 zone — never time.Local, which on a UTC host misreads the stamp
// 8h late. Other naive stamps stay host-local and approximate (unchanged).
//
// Window cap: when the message states the window (the z.ai "for N hour(s)"
// phrase), the returned ResetAt is never later than now+window. A parsed
// reset beyond that is a parse error, so the pause falls back to now+window
// and the anomaly is logged; a reset already in the past is kept as-is (an
// expired limit is not a pause).
//
// Returns (0, nil) when neither shape is present, leaving the caller to fall
// back.
func parseOpencodeReset(s string) (time.Duration, *time.Time) {
	window, hasWindow := parseOpencodeWindow(s)
	if m := opencodeResetAtRe.FindStringSubmatch(s); len(m) == 3 {
		// Collapse any internal whitespace so the fixed layout matches.
		stamp := strings.Join(strings.Fields(m[1]), " ")
		if t, ok := parseOpencodeResetStamp(stamp, m[2], hasWindow); ok {
			if hasWindow {
				t = capOpencodeResetAtWindow(t, opencodeNow(), window, stamp)
			}
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

// parseOpencodeResetStamp parses the captured reset stamp. A stamp with an
// explicit zone designator keeps that offset; a naive stamp is read in the
// z.ai fixed UTC+8 zone when the message carries z.ai's windowed usage-limit
// shape, and host-locally otherwise. The caller lowercases the message, so
// RFC3339 "T" separators and "Z" designators are re-uppercased here.
func parseOpencodeResetStamp(stamp, offset string, zai bool) (time.Time, bool) {
	if len(stamp) >= 11 && (stamp[10] == 't' || stamp[10] == 'T') {
		normalized := []byte(stamp)
		normalized[10] = 'T'
		stamp = string(normalized)
	}
	if offset == "z" {
		offset = "Z"
	}
	if offset != "" {
		for _, layout := range []string{
			time.RFC3339,
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05Z0700",
			"2006-01-02T15:04:05Z0700",
		} {
			if t, err := time.Parse(layout, stamp+offset); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	loc := time.Local
	if zai {
		loc = zaiResetZone
	}
	for _, layout := range []string{opencodeResetLayout, "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, stamp, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseOpencodeWindow extracts the stated usage window from the z.ai phrase
// "Usage limit reached for N hour(s)". The second return is false when the
// message states no window.
func parseOpencodeWindow(s string) (time.Duration, bool) {
	m := opencodeWindowRe.FindStringSubmatch(s)
	if len(m) != 3 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
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
	if unit == 0 {
		return 0, false
	}
	return time.Duration(n) * unit, true
}

// capOpencodeResetAtWindow enforces that a stated-window reset never outlives
// the window: a parsed reset later than now+window means the stamp was read
// wrong (or is garbage), so the pause falls back to the window length and the
// anomaly is logged for operator triage.
func capOpencodeResetAtWindow(reset, now time.Time, window time.Duration, stamp string) time.Time {
	deadline := now.Add(window)
	if reset.After(deadline) {
		log.Printf("reliability: opencode usage-limit reset stamp %q parsed as %s, beyond the stated %s window from %s; capping reset at %s",
			stamp, reset.UTC().Format(time.RFC3339), window, now.UTC().Format(time.RFC3339), deadline.UTC().Format(time.RFC3339))
		return deadline
	}
	return reset
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
