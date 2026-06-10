package reliability

import (
	"regexp"
	"time"
)

const ProviderAnthropic = "anthropic"

var (
	claudeRateLimitRe     = regexp.MustCompile(`(?i)rate.?limit`)
	claudeFiveHourRe      = regexp.MustCompile(`(?i)\bfive.?\s*hour\b`)
	claudeSevenDayRe      = regexp.MustCompile(`(?i)\bseven.?\s*day\b`)
	claudeModelNotFoundRe = regexp.MustCompile(`(?i)model.?not.?found`)
	claudeAuthFailedRe    = regexp.MustCompile(`(?i)authentication.?failed`)
	claudeHTTP529Re       = regexp.MustCompile(`(?i)\b529\b`)
	claudeOverloadRe      = regexp.MustCompile(`(?i)overload`)
)

// ParseClaudeError examines raw error output from Claude for structured
// provider signals and returns a populated FailureEvidence when a known
// signature is found. Returns nil when no recognised signature is present.
func ParseClaudeError(stderr string) *FailureEvidence {
	if stderr == "" {
		return nil
	}

	var ev FailureEvidence
	ev.Provider = ProviderAnthropic

	if claudeModelNotFoundRe.MatchString(stderr) {
		ev.Category = CategoryInvalidModel
		ev.Message = firstLineMatch(stderr, claudeModelNotFoundRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		ev.StatusCode = 404
		return &ev
	}

	if claudeAuthFailedRe.MatchString(stderr) {
		ev.Category = CategoryAuthOrProxy
		ev.Message = firstLineMatch(stderr, claudeAuthFailedRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		ev.StatusCode = 401
		return &ev
	}

	if claudeHTTP529Re.MatchString(stderr) || claudeOverloadRe.MatchString(stderr) {
		ev.Category = CategoryProviderOverloaded
		ev.Message = firstLineMatch(stderr, claudeHTTP529Re, claudeOverloadRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		ev.StatusCode = 529
		ev.RetryAfter = 30 * time.Second
		return &ev
	}

	if claudeRateLimitRe.MatchString(stderr) {
		ev.Message = firstLineMatch(stderr, claudeRateLimitRe)
		ev.RawSignal = truncateSignal(stderr, 256)

		if claudeSevenDayRe.MatchString(stderr) {
			ev.Category = CategoryUsageLimit
			ev.StatusCode = 429
			return &ev
		}

		if claudeFiveHourRe.MatchString(stderr) {
			ev.Category = CategoryShortRateLimit
			ev.StatusCode = 429
			ev.RetryAfter = 5 * time.Hour
			return &ev
		}

		ev.Category = CategoryShortRateLimit
		ev.StatusCode = 429
		ev.RetryAfter = 60 * time.Second
		return &ev
	}

	return nil
}
