package reliability

import (
	"encoding/json"
	"fmt"
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

// claudeSevenDayDefaultReset is the fallback reset window for Claude's
// seven-day usage limit when the output carries no parseable reset timing.
const claudeSevenDayDefaultReset = 7 * 24 * time.Hour

// claudeStreamEventLine is the minimal shape of a claude stream-json line
// that classification cares about: the event type, plus the structured
// rate_limit_info payload carried by rate_limit_event records.
type claudeStreamEventLine struct {
	Type          string                    `json:"type"`
	RateLimitInfo *claudeRateLimitEventInfo `json:"rate_limit_info"`
}

// claudeRateLimitEventInfo mirrors the rate_limit_info payload of a claude
// rate_limit_event record. Status is the event's verdict ("allowed" while the
// request passed); ResetsAt is the window reset as epoch seconds;
// RateLimitType names the window ("five_hour", "seven_day").
type claudeRateLimitEventInfo struct {
	Status        string `json:"status"`
	ResetsAt      int64  `json:"resetsAt"`
	RateLimitType string `json:"rateLimitType"`
}

// scanClaudeStreamLines splits raw claude output into its structured
// rate_limit_event records (in stream order) and the remaining text with
// those record lines removed.
//
// rate_limit_event records are machine state, not prose: their vocabulary
// ("rate_limit", "five_hour", "seven_day") must never drive the text-based
// verdicts, and whether a limit was hit is decided by the record's status
// field alone. Every ALLOWED event names both windows, so feeding the raw
// lines to the text classifier turns a healthy stream into a usage limit.
func scanClaudeStreamLines(stderr string) (events []claudeRateLimitEventInfo, masked string) {
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			kept = append(kept, line)
			continue
		}
		var ev claudeStreamEventLine
		if err := json.Unmarshal([]byte(trimmed), &ev); err == nil && ev.Type == "rate_limit_event" {
			var info claudeRateLimitEventInfo
			if ev.RateLimitInfo != nil {
				info = *ev.RateLimitInfo
			}
			events = append(events, info)
			continue
		}
		kept = append(kept, line)
	}
	return events, strings.Join(kept, "\n")
}

// claudeEventReportsLimit reports whether a rate_limit_event record reports
// an actual limit: a status that is not ALLOWED. Records with an empty status
// carry no verdict and stay informational.
func claudeEventReportsLimit(info claudeRateLimitEventInfo) bool {
	return info.Status != "" && !strings.EqualFold(info.Status, "allowed")
}

// claudeUsageLimitFromEvents builds usage_limit evidence from the latest
// rate_limit_event whose status is not ALLOWED. The record's reset (epoch
// seconds) is authoritative; the canned window defaults apply only when the
// record carries no reset at all.
func claudeUsageLimitFromEvents(events []claudeRateLimitEventInfo) *FailureEvidence {
	var latest claudeRateLimitEventInfo
	found := false
	for _, info := range events {
		if claudeEventReportsLimit(info) {
			latest = info
			found = true
		}
	}
	if !found {
		return nil
	}
	ev := &FailureEvidence{
		Provider:   ProviderAnthropic,
		Category:   CategoryUsageLimit,
		StatusCode: 429,
		Message:    fmt.Sprintf("rate_limit_event status=%s window=%s", latest.Status, latest.RateLimitType),
		RawSignal: truncateSignal(fmt.Sprintf("status=%s resetsAt=%d window=%s",
			latest.Status, latest.ResetsAt, latest.RateLimitType), 256),
	}
	if latest.ResetsAt > 0 {
		reset := time.Unix(latest.ResetsAt, 0).UTC()
		ev.ResetAt = &reset
		return ev
	}
	switch strings.ToLower(latest.RateLimitType) {
	case "seven_day":
		ev.ResetAfter = claudeSevenDayDefaultReset
	case "five_hour":
		ev.ResetAfter = claudeFiveHourDefaultReset
	}
	return ev
}

// claudeEventRecordReset returns the reset carried by the latest
// rate_limit_event record, ALLOWED or not: an ALLOWED event still reports
// when its window next resets, which caps any pause derived from limit text.
func claudeEventRecordReset(events []claudeRateLimitEventInfo) *time.Time {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].ResetsAt > 0 {
			reset := time.Unix(events[i].ResetsAt, 0).UTC()
			return &reset
		}
	}
	return nil
}

// claudeUnterminatedStreamEvidence reports whether the output is a claude
// stream-json transcript that ended without a result or error record: the
// process was cut off mid-response rather than reporting a failure of its
// own. With no verdict from the CLI and no rate-limit record reporting a
// non-ALLOWED status, the try is transient — retryable, never a pause.
func claudeUnterminatedStreamEvidence(stderr string) *FailureEvidence {
	streamEvents := 0
	sawResult, sawError := false, false
	for _, line := range strings.Split(stderr, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var ev claudeStreamEventLine
		if err := json.Unmarshal([]byte(trimmed), &ev); err != nil || ev.Type == "" {
			continue
		}
		streamEvents++
		switch ev.Type {
		case "result":
			sawResult = true
		case "error":
			sawError = true
		}
	}
	if streamEvents == 0 || sawResult || sawError {
		return nil
	}
	return &FailureEvidence{
		Provider:  ProviderAnthropic,
		Category:  CategoryTransientInfra,
		Message:   "claude stream ended without a result or error record",
		RawSignal: truncateSignal(stderr, 256),
	}
}

// ParseClaudeError examines raw error output from Claude for structured
// provider signals and returns a populated FailureEvidence when a known
// signature is found. Returns nil when no recognised signature is present.
func ParseClaudeError(stderr string) *FailureEvidence {
	if stderr == "" {
		return nil
	}

	// Structured rate_limit_event records are authoritative: a record whose
	// status is not ALLOWED decides the limit verdict, and the record lines
	// are removed from the text the word-based branches see below.
	limitEvents, text := scanClaudeStreamLines(stderr)
	if ev := claudeUsageLimitFromEvents(limitEvents); ev != nil {
		return ev
	}

	var ev FailureEvidence
	ev.Provider = ProviderAnthropic

	if claudeModelNotFoundRe.MatchString(text) {
		ev.Category = CategoryInvalidModel
		ev.Message = firstLineMatch(text, claudeModelNotFoundRe)
		ev.RawSignal = truncateSignal(text, 256)
		ev.StatusCode = 404
		return &ev
	}

	if claudeAuthFailedRe.MatchString(text) {
		ev.Category = CategoryAuthOrProxy
		ev.Message = firstLineMatch(text, claudeAuthFailedRe)
		ev.RawSignal = truncateSignal(text, 256)
		ev.StatusCode = 401
		return &ev
	}

	if claudeHTTP529Re.MatchString(text) || claudeOverloadRe.MatchString(text) {
		ev.Category = CategoryProviderOverloaded
		ev.Message = firstLineMatch(text, claudeHTTP529Re, claudeOverloadRe)
		ev.RawSignal = truncateSignal(text, 256)
		ev.StatusCode = 529
		ev.RetryAfter = 30 * time.Second
		return &ev
	}

	// Claude reports its rolling usage windows (five-hour, seven-day) as
	// rate_limit errors, and its limit footers ("five-hour limit reached ·
	// resets in 3h 24m") may omit the words "rate limit" entirely. Enter the
	// limit branch on any of: explicit rate-limit text, a named usage window,
	// or a "limit" message carrying parsed reset timing.
	hasRateLimit := claudeRateLimitRe.MatchString(text)
	hasWindow := claudeSevenDayRe.MatchString(text) || claudeFiveHourRe.MatchString(text)
	resetAfter, resetAt := parseClaudeReset(text)
	hasReset := resetAfter > 0 || resetAt != nil
	if hasRateLimit || hasWindow || (hasReset && claudeLimitWordRe.MatchString(text)) {
		ev.Message = firstLineMatch(text, claudeRateLimitRe, claudeLimitWordRe)
		ev.RawSignal = truncateSignal(text, 256)
		ev.StatusCode = 429

		// Usage windows are quota exhaustion, not short rate limits:
		// classify usage_limit so the quota scope is benched until reset
		// rather than waited out inline in the attempt loop.
		switch {
		case claudeSevenDayRe.MatchString(text):
			ev.Category = CategoryUsageLimit
			ev.ResetAfter = claudeSevenDayDefaultReset
		case claudeFiveHourRe.MatchString(text):
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

		// Reset resolution: parsed text timing beats the window defaults, a
		// rate-limit record's reset beats the defaults, and no pause may
		// outlive the latest parsed reset — a canned default is used only
		// when nothing was parsed at all.
		recordReset := claudeEventRecordReset(limitEvents)
		switch {
		case resetAfter > 0 && recordReset != nil:
			if recordReset.Before(time.Now().Add(resetAfter)) {
				ev.ResetAt = recordReset
			} else {
				ev.ResetAfter = resetAfter
			}
		case resetAfter > 0:
			ev.ResetAfter = resetAfter
		case resetAt != nil && recordReset != nil:
			if recordReset.Before(*resetAt) {
				ev.ResetAt = recordReset
			} else {
				ev.ResetAt = resetAt
			}
		case resetAt != nil:
			ev.ResetAt = resetAt
		case recordReset != nil:
			ev.ResetAfter = 0
			ev.ResetAt = recordReset
		}
		return &ev
	}

	// A stream transcript that ends with neither a result record nor an
	// error record was cut off mid-response: transient, so the try retries
	// instead of pausing the quota scope.
	if cut := claudeUnterminatedStreamEvidence(stderr); cut != nil {
		return cut
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
