package reliability

import (
	"regexp"
	"strings"
	"time"
)

const ProviderOpenAI = "openai"

var (
	codexUsageLimitRe  = regexp.MustCompile(`(?i)hit your usage limit|usage limit`)
	codexTryAgainAtRe  = regexp.MustCompile(`(?i)try again at\s+(\d{1,2}:\d{2}\s*(?:AM|PM))`)
	codexRateLimitRe   = regexp.MustCompile(`(?i)rate.?limit|too many requests`)
	codexModelNotFound = regexp.MustCompile(`(?i)model.?not.?found`)
	codexAuthRe        = regexp.MustCompile(`(?i)invalid api.?key|authentication.?failed|unauthorized`)
)

func ParseCodexError(stderr string) *FailureEvidence {
	if stderr == "" {
		return nil
	}

	var ev FailureEvidence
	ev.Provider = ProviderOpenAI

	if codexUsageLimitRe.MatchString(stderr) {
		ev.Category = CategoryUsageLimit
		ev.Message = firstLineMatch(stderr, codexUsageLimitRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		if m := codexTryAgainAtRe.FindStringSubmatch(stderr); len(m) >= 2 {
			if t := parseTryAgainAt(m[1]); t != nil {
				ev.ResetAt = t
				ev.ResetAfter = time.Until(*t)
				if ev.ResetAfter < 0 {
					ev.ResetAfter = 0
				}
			}
		}
		return &ev
	}

	if codexAuthRe.MatchString(stderr) {
		ev.Category = CategoryAuthOrProxy
		ev.Message = firstLineMatch(stderr, codexAuthRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		ev.StatusCode = 401
		return &ev
	}

	if codexModelNotFound.MatchString(stderr) {
		ev.Category = CategoryInvalidModel
		ev.Message = firstLineMatch(stderr, codexModelNotFound)
		ev.RawSignal = truncateSignal(stderr, 256)
		ev.StatusCode = 404
		return &ev
	}

	if codexRateLimitRe.MatchString(stderr) {
		ev.Category = CategoryShortRateLimit
		ev.Message = firstLineMatch(stderr, codexRateLimitRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		ev.StatusCode = 429
		ev.RetryAfter = 60 * time.Second
		return &ev
	}

	return nil
}

func parseTryAgainAt(s string) *time.Time {
	s = strings.TrimSpace(s)
	t, err := time.Parse("3:04 PM", s)
	if err != nil {
		t, err = time.Parse("3:04PM", s)
		if err != nil {
			return nil
		}
	}
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	return &target
}
