package reliability

import (
	"regexp"
	"strconv"
	"strings"
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
	claudeLimitWordRe     = regexp.MustCompile(`(?i)\blimit\b`)

	// Usage-limit reset timing. Claude phrases limit messages as rate_limit
	// and appends when the window resets, either as a span ("resets in 3h 24m")
	// or a clock time ("resets at 14:30" / "resets at 2:30 PM"). Both are
	// anchored to "reset(s)" so stray durations or timestamps elsewhere in the
	// output cannot false-positive.
	claudeResetSpanRe  = regexp.MustCompile(`(?i)resets?(?:\s+(?:in|at))?\s+(\d{1,3})\s*h(?:\s*(\d{1,2})\s*m)?\b`)
	claudeResetClockRe = regexp.MustCompile(`(?i)resets?(?:\s+at)?\s+(\d{1,2}):(\d{2})\s*(am|pm)?\b`)
)

// claudeFiveHourDefaultReset is the fallback reset window for Claude's
// five-hour usage limit when the output carries no parseable reset timing:
// the window is rolling, so the worst case is a full five hours.
const claudeFiveHourDefaultReset = 5 * time.Hour

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

	// Claude reports its rolling usage windows (five-hour, seven-day) as
	// rate_limit errors, and its limit footers ("five-hour limit reached ·
	// resets in 3h 24m") may omit the words "rate limit" entirely. Enter the
	// limit branch on any of: explicit rate-limit text, a named usage window,
	// or a "limit" message carrying parsed reset timing.
	hasRateLimit := claudeRateLimitRe.MatchString(stderr)
	hasWindow := claudeSevenDayRe.MatchString(stderr) || claudeFiveHourRe.MatchString(stderr)
	resetAfter, resetAt := parseClaudeReset(stderr)
	hasReset := resetAfter > 0 || resetAt != nil
	if hasRateLimit || hasWindow || (hasReset && claudeLimitWordRe.MatchString(stderr)) {
		ev.Message = firstLineMatch(stderr, claudeRateLimitRe, claudeLimitWordRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		ev.StatusCode = 429

		// Usage windows are quota exhaustion, not short rate limits:
		// classify usage_limit so the quota scope is benched until reset
		// rather than waited out inline in the attempt loop.
		switch {
		case claudeSevenDayRe.MatchString(stderr):
			ev.Category = CategoryUsageLimit
			ev.ResetAfter = 7 * 24 * time.Hour
		case claudeFiveHourRe.MatchString(stderr):
			ev.Category = CategoryUsageLimit
			ev.ResetAfter = claudeFiveHourDefaultReset
		case hasReset:
			// A limit message carrying explicit reset timing is a usage
			// window even without a named span.
			ev.Category = CategoryUsageLimit
		default:
			ev.Category = CategoryShortRateLimit
			ev.RetryAfter = 60 * time.Second
			return &ev
		}

		// Parsed reset timing beats the window defaults.
		if resetAfter > 0 {
			ev.ResetAfter = resetAfter
		}
		if resetAt != nil {
			ev.ResetAt = resetAt
		}
		return &ev
	}

	return nil
}

// parseClaudeReset extracts reset timing from a Claude limit message: a span
// ("resets in 3h 24m") as a duration, or a clock time ("resets at 14:30",
// "resets at 2:30 PM") as the next occurrence of that wall-clock time. Returns
// zero values when no reset timing is present.
func parseClaudeReset(s string) (time.Duration, *time.Time) {
	if m := claudeResetSpanRe.FindStringSubmatch(s); len(m) >= 2 {
		hours, _ := strconv.Atoi(m[1])
		minutes := 0
		if m[2] != "" {
			minutes, _ = strconv.Atoi(m[2])
		}
		if d := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute; d > 0 {
			return d, nil
		}
	}

	if m := claudeResetClockRe.FindStringSubmatch(s); len(m) >= 3 {
		hour, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		switch strings.ToLower(m[3]) {
		case "pm":
			if hour < 12 {
				hour += 12
			}
		case "am":
			if hour == 12 {
				hour = 0
			}
		}
		if hour > 23 || minute > 59 {
			return 0, nil
		}
		now := time.Now()
		target := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
		if !target.After(now) {
			target = target.Add(24 * time.Hour)
		}
		return 0, &target
	}

	return 0, nil
}
