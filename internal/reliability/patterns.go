package reliability

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FailureClass categorizes failures into three classes that drive the
// resilience cascade and retry behavior.
type FailureClass string

const (
	// FailureInfra indicates an infrastructure failure: rate limits,
	// harness/launch errors, API timeouts, network stalls, or stall-detection
	// kills. Only >1 infra failure within a run escalates the freeze cascade.
	FailureInfra FailureClass = "infra"

	// FailureAgent indicates an ordinary agent error or short no-op try.
	// Agent failures retry but do NOT increment the freeze counter.
	// This is also the default for unrecognized errors (does-not-freeze side).
	FailureAgent FailureClass = "agent"

	// FailureIncomplete indicates the agent produced file changes but did not
	// finalize the lap (no `laps done`/`laps handoff`). This class cannot be
	// determined from error text alone — the caller sets it based on
	// working-tree state via ClassifyContext or post-hoc.
	FailureIncomplete FailureClass = "incomplete"
)

// ClassifyContext provides additional context that cannot be determined from
// error text alone. Callers pass this to ClassifyError to enable detection of
// incomplete failures.
type ClassifyContext struct {
	// HasFileChanges indicates the working tree has uncommitted changes.
	HasFileChanges bool
	// Finalized indicates the agent called `laps done` or `laps handoff`.
	Finalized bool
}

type RetryStrategy string

const (
	StrategyRotate       RetryStrategy = "rotate"
	StrategyResume       RetryStrategy = "resume + retry"
	StrategyWaitResume   RetryStrategy = "wait + resume"
	StrategyNoOp         RetryStrategy = "no-op"
	StrategyFreshRestart RetryStrategy = "fresh restart"
)

type StrategyDecision struct {
	Strategy     RetryStrategy
	Cooldown     time.Duration
	Reason       string
	FailureClass FailureClass
}

type Pattern struct {
	Name         string
	Match        func(logLines []string) bool
	Strategy     RetryStrategy
	FailureClass FailureClass
	Extract      func(logLines []string) time.Duration
}

var claudeRateLimitRegex = regexp.MustCompile(`retry-after:?\s*(\d+)`)

// ErrorPatterns is the ordered table of error classification rules.
// Patterns are evaluated top-to-bottom; the first match wins.
// Each pattern is tagged with a FailureClass for the resilience cascade.
var ErrorPatterns = []Pattern{
	// ── Infra-class: rate limits ──
	{
		Name: "claude rate-limit interrupt",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "rate-limit") || containsSubstring(lines, "429 Too Many Requests")
		},
		Strategy:     StrategyWaitResume,
		FailureClass: FailureInfra,
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
		Name: "rate limit generic",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "rate limit") || containsSubstring(lines, "too many requests")
		},
		Strategy:     StrategyWaitResume,
		FailureClass: FailureInfra,
		Extract: func(lines []string) time.Duration {
			return 60 * time.Second
		},
	},
	{
		Name: "usage limit hit",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "usage limit") || containsSubstring(lines, "hit your usage limit")
		},
		Strategy:     StrategyWaitResume,
		FailureClass: FailureInfra,
		Extract: func(lines []string) time.Duration {
			return 120 * time.Second
		},
	},

	// ── Infra-class: harness/launch errors ──
	{
		Name:         "argument list too long",
		Match:        func(lines []string) bool { return containsSubstring(lines, "argument list too long") },
		Strategy:     StrategyFreshRestart,
		FailureClass: FailureInfra,
	},
	{
		Name:         "fork/exec error",
		Match:        func(lines []string) bool { return containsSubstring(lines, "fork/exec") },
		Strategy:     StrategyFreshRestart,
		FailureClass: FailureInfra,
	},
	{
		Name:         "exec format error",
		Match:        func(lines []string) bool { return containsSubstring(lines, "exec format error") },
		Strategy:     StrategyFreshRestart,
		FailureClass: FailureInfra,
	},
	{
		Name: "no such file or directory (harness)",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "exec:") && containsSubstring(lines, "not found")
		},
		Strategy:     StrategyRotate,
		FailureClass: FailureInfra,
	},

	// ── Infra-class: API timeout / network stall ──
	{
		Name: "API timeout",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "request timed out") || containsSubstring(lines, "deadline exceeded") || containsSubstring(lines, "context deadline exceeded")
		},
		Strategy:     StrategyResume,
		FailureClass: FailureInfra,
	},
	{
		Name: "connection refused",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "connection refused") || containsSubstring(lines, "connection reset")
		},
		Strategy:     StrategyResume,
		FailureClass: FailureInfra,
	},
	{
		Name: "network unreachable",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "network is unreachable") || containsSubstring(lines, "no route to host")
		},
		Strategy:     StrategyResume,
		FailureClass: FailureInfra,
	},
	{
		Name: "TLS handshake timeout",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "tls handshake timeout") || containsSubstring(lines, "certificate verify failed")
		},
		Strategy:     StrategyResume,
		FailureClass: FailureInfra,
	},
	{
		Name: "server error 5xx",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "500 internal server error") || containsSubstring(lines, "502 bad gateway") || containsSubstring(lines, "503 service unavailable") || containsSubstring(lines, "504 gateway timeout")
		},
		Strategy:     StrategyResume,
		FailureClass: FailureInfra,
	},

	// ── Infra-class: stall-detection signals ──
	{
		Name: "stall detected",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "stall detected") || containsSubstring(lines, "stall recovery")
		},
		Strategy:     StrategyResume,
		FailureClass: FailureInfra,
	},

	// ── Agent-class: harness-specific patterns ──
	{
		Name:         "opencode API bad request",
		Match:        func(lines []string) bool { return containsSubstring(lines, "API bad request") },
		Strategy:     StrategyRotate,
		FailureClass: FailureAgent,
	},
	{
		Name: "gemini-cli exit 1",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "exit status 1") && containsSubstring(lines, "gemini-cli")
		},
		Strategy:     StrategyResume,
		FailureClass: FailureAgent,
	},
	{
		Name: "codex completion despite limit warning",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "limit warning") && containsSubstring(lines, "completion")
		},
		Strategy:     StrategyNoOp,
		FailureClass: FailureAgent,
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
// It returns a StrategyDecision that includes the retry strategy and failure
// class. An optional ClassifyContext can be provided to detect incomplete
// failures (file changes without finalization). Pass nil if no context is
// available.
//
// Unknown/unmatched errors default to FailureAgent (the does-not-freeze side).
func ClassifyError(logLines []string, ctx ...*ClassifyContext) StrategyDecision {
	// Check for incomplete classification first if context is provided.
	if len(ctx) > 0 && ctx[0] != nil {
		c := ctx[0]
		if c.HasFileChanges && !c.Finalized {
			return StrategyDecision{
				Strategy:     StrategyResume,
				Reason:       "incomplete: file changes without finalization",
				FailureClass: FailureIncomplete,
			}
		}
	}

	for _, pattern := range ErrorPatterns {
		if pattern.Match(logLines) {
			decision := StrategyDecision{
				Strategy:     pattern.Strategy,
				Reason:       pattern.Name,
				FailureClass: pattern.FailureClass,
			}
			if pattern.Extract != nil {
				decision.Cooldown = pattern.Extract(logLines)
			}
			return decision
		}
	}

	// Unknown failures default to FailureAgent (task 5.6 — does-not-freeze side).
	return StrategyDecision{
		Strategy:     StrategyFreshRestart,
		Reason:       "unknown error",
		FailureClass: FailureAgent,
	}
}
