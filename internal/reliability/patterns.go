package reliability

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type RetryStrategy string

const (
	StrategyRotate       RetryStrategy = "rotate"
	StrategyResume       RetryStrategy = "resume + retry"
	StrategyWaitResume   RetryStrategy = "wait + resume"
	StrategyNoOp         RetryStrategy = "no-op"
	StrategyFreshRestart RetryStrategy = "fresh restart"
)

type StrategyDecision struct {
	Strategy RetryStrategy
	Cooldown time.Duration
	Reason   string
}

type Pattern struct {
	Name     string
	Match    func(logLines []string) bool
	Strategy RetryStrategy
	Extract  func(logLines []string) time.Duration
}

var claudeRateLimitRegex = regexp.MustCompile(`retry-after:?\s*(\d+)`)

var ErrorPatterns = []Pattern{
	{
		Name:     "opencode API bad request",
		Match:    func(lines []string) bool { return containsSubstring(lines, "API bad request") },
		Strategy: StrategyRotate,
	},
	{
		Name:     "gemini-cli exit 1",
		Match:    func(lines []string) bool { return containsSubstring(lines, "exit status 1") && containsSubstring(lines, "gemini-cli") },
		Strategy: StrategyResume,
	},
	{
		Name:     "claude rate-limit interrupt",
		Match:    func(lines []string) bool { return containsSubstring(lines, "rate-limit") || containsSubstring(lines, "429 Too Many Requests") },
		Strategy: StrategyWaitResume,
		Extract: func(lines []string) time.Duration {
			for _, line := range lines {
				if match := claudeRateLimitRegex.FindStringSubmatch(strings.ToLower(line)); len(match) > 1 {
					if secs, err := strconv.Atoi(match[1]); err == nil {
						return time.Duration(secs) * time.Second
					}
				}
			}
			return 60 * time.Second // default
		},
	},
	{
		Name:     "codex completion despite limit warning",
		Match:    func(lines []string) bool { return containsSubstring(lines, "limit warning") && containsSubstring(lines, "completion") },
		Strategy: StrategyNoOp,
	},
}

func containsSubstring(lines []string, sub string) bool {
	lowerSub := strings.ToLower(sub)
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), lowerSub) {
			return true
		}
	}
	return false
}

// ClassifyError matches against the last N lines of the try log post-failure.
func ClassifyError(logLines []string) StrategyDecision {
	for _, pattern := range ErrorPatterns {
		if pattern.Match(logLines) {
			decision := StrategyDecision{Strategy: pattern.Strategy, Reason: pattern.Name}
			if pattern.Extract != nil {
				decision.Cooldown = pattern.Extract(logLines)
			}
			return decision
		}
	}
	return StrategyDecision{Strategy: StrategyFreshRestart, Reason: "unknown error"}
}
