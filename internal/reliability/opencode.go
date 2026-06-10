package reliability

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

var (
	opencodeRetryAfterRe = regexp.MustCompile(`(?i)retry.?(?:after|in)\s+(\d+)\s*(?:s|sec|seconds?)`)
	opencodeResetsInRe   = regexp.MustCompile(`(?i)resets?\s+in\s+(\S+)`)
)

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
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
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

	if !sawErrorEvent {
		return nil
	}

	var fe FailureEvidence
	fe.Provider = extractProviderFromModel(model)
	fe.RawSignal = truncateSignal(stderr, 256)

	if eventError == nil {
		fe.Category = CategoryAgentError
		return &fe
	}

	name := eventError.Error.Name
	msg := eventError.Error.Data.Message

	if msg != "" {
		fe.Message = truncateSignal(msg, 200)
	} else if name != "" {
		fe.Message = truncateSignal(name, 200)
	}

	lowerName := strings.ToLower(name)
	lowerMsg := strings.ToLower(msg)

	switch {
	case containsAny(lowerName, "usagelimit", "quotaexceeded", "resourceexhausted") ||
		containsAny(lowerMsg, "usage limit", "quota exceeded", "resource_exhausted"):
		fe.Category = CategoryUsageLimit
		fe.StatusCode = 429
		if dur := parseResetsIn(msg); dur > 0 {
			fe.ResetAfter = dur
		}

	case containsAny(lowerName, "ratelimit", "toomanyrequests") ||
		containsAny(lowerMsg, "rate limit", "too many requests"):
		fe.Category = CategoryShortRateLimit
		fe.StatusCode = 429
		fe.RetryAfter = parseRetryAfterSeconds(msg)
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

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
