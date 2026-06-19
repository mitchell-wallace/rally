package reliability

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

const ProviderGemini = "gemini"

var (
	geminiResourceExhaustedRe = regexp.MustCompile(`(?i)RESOURCE_EXHAUSTED`)
	geminiQuotaRe             = regexp.MustCompile(`(?i)Individual quota reached`)
	geminiResetsInRe          = regexp.MustCompile(`(?i)Resets\s+in\s+(\S+)`)
	geminiHTTP429Re           = regexp.MustCompile(`(?i)\b429\b`)
	geminiAuthOrEligibilityRe = regexp.MustCompile(`(?i)IneligibleTierError|UNSUPPORTED_CLIENT|no longer supported for Gemini Code Assist|Error authenticating`)
)

// ParseGeminiError examines raw error output from Antigravity/Gemini for
// structured provider signals and returns a populated FailureEvidence when a
// known signature is found. Returns nil when no recognised signature is present.
func ParseGeminiError(stderr string) *FailureEvidence {
	if stderr == "" {
		return nil
	}

	var ev FailureEvidence
	ev.Provider = ProviderGemini

	if geminiResourceExhaustedRe.MatchString(stderr) || geminiQuotaRe.MatchString(stderr) {
		ev.Category = CategoryUsageLimit
		ev.Message = firstLineMatch(stderr, geminiResourceExhaustedRe, geminiQuotaRe)
		ev.RawSignal = truncateSignal(stderr, 256)

		if dur := parseResetsIn(stderr); dur > 0 {
			ev.ResetAfter = dur
		}

		if geminiHTTP429Re.MatchString(stderr) {
			ev.StatusCode = 429
		}

		return &ev
	}

	if geminiHTTP429Re.MatchString(stderr) {
		ev.Category = CategoryShortRateLimit
		ev.StatusCode = 429
		ev.Message = "HTTP 429"
		ev.RawSignal = truncateSignal(stderr, 256)
		return &ev
	}

	if geminiAuthOrEligibilityRe.MatchString(stderr) {
		ev.Category = CategoryAuthOrProxy
		ev.Message = firstLineMatch(stderr, geminiAuthOrEligibilityRe)
		ev.RawSignal = truncateSignal(stderr, 256)
		return &ev
	}

	return nil
}

func parseResetsIn(s string) time.Duration {
	m := geminiResetsInRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	dur := strings.TrimRight(m[1], ".,;:!?")
	return parseGoDuration(dur)
}

var durationWithDaysRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)d`)

// parseGoDuration extends time.ParseDuration to handle "d" (day) suffixes
// by converting them to hours before delegating to the standard parser.
func parseGoDuration(s string) time.Duration {
	if m := durationWithDaysRe.FindStringSubmatch(s); len(m) == 2 {
		days, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0
		}
		return time.Duration(days * 24 * float64(time.Hour))
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}

func firstLineMatch(s string, patterns ...*regexp.Regexp) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, p := range patterns {
			if p.MatchString(line) {
				return truncateSignal(line, 200)
			}
		}
	}
	return truncateSignal(s, 200)
}

func truncateSignal(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}
