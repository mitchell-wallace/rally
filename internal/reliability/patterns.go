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
	// ChangedPaths lists the files that changed during this try (for evidence).
	ChangedPaths []string
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
	// Category is the stable FailureCategory taxonomy value. May be empty
	// for decisions produced by legacy code paths that have not yet been
	// updated to populate it.
	Category FailureCategory
	// DisplayLabel is a short, human-readable label for the failure,
	// distinct from Reason (which is typically the pattern name).
	// Carries no harness name unless the category is intentionally
	// harness-specific.
	DisplayLabel string
	// Evidence is structured failure evidence populated by the classification
	// path. Non-nil on every priority so callers can plumb it to telemetry.
	Evidence *FailureEvidence
}

type Pattern struct {
	Name     string
	Match    func(logLines []string) bool
	Strategy RetryStrategy
	Extract  func(logLines []string) time.Duration
	// Category is the FailureCategory for this pattern. The decision's
	// FailureClass is derived from it via CategoryToClass (design Decision 3),
	// so the category — not a per-pattern class — is the single source of
	// truth for what feeds the freeze counter.
	Category FailureCategory
	// Harness constrains this pattern to a specific harness. When non-empty,
	// the pattern matches only when the failing harness equals this value
	// (case-insensitive). When empty, the pattern is harness-agnostic.
	Harness string
}

var claudeRateLimitRegex = regexp.MustCompile(`retry-after:?\s*(\d+)`)

// ErrorPatterns is the ordered table of error classification rules.
// Patterns are evaluated top-to-bottom; the first match wins.
// Each pattern is tagged with a FailureCategory, from which the resilience
// FailureClass is derived. Patterns with a non-empty Harness field match
// only when the failing harness matches.
var ErrorPatterns = []Pattern{
	// ── Infra-class: rate limits ──
	{
		Name: "claude rate-limit interrupt",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "rate-limit") || containsSubstring(lines, "429 Too Many Requests")
		},
		Strategy: StrategyWaitResume,
		Category: CategoryShortRateLimit,
		Harness:  "claude",
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
		Strategy: StrategyWaitResume,
		Category: CategoryShortRateLimit,
		Extract: func(lines []string) time.Duration {
			return 60 * time.Second
		},
	},
	{
		Name: "usage limit hit",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "usage limit") || containsSubstring(lines, "hit your usage limit")
		},
		Strategy: StrategyWaitResume,
		Category: CategoryUsageLimit,
		Extract: func(lines []string) time.Duration {
			return 120 * time.Second
		},
	},

	// ── Infra-class: harness/launch errors ──
	{
		Name:     "argument list too long",
		Match:    func(lines []string) bool { return containsSubstring(lines, "argument list too long") },
		Strategy: StrategyFreshRestart,
		Category: CategoryHarnessLaunch,
	},
	{
		Name:     "fork/exec error",
		Match:    func(lines []string) bool { return containsSubstring(lines, "fork/exec") },
		Strategy: StrategyFreshRestart,
		Category: CategoryHarnessLaunch,
	},
	{
		Name:     "exec format error",
		Match:    func(lines []string) bool { return containsSubstring(lines, "exec format error") },
		Strategy: StrategyFreshRestart,
		Category: CategoryHarnessLaunch,
	},
	{
		Name: "no such file or directory (harness)",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "exec:") && containsSubstring(lines, "not found")
		},
		Strategy: StrategyRotate,
		Category: CategoryHarnessLaunch,
	},

	// ── Infra-class: API timeout / network stall ──
	{
		Name: "API timeout",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "request timed out") || containsSubstring(lines, "deadline exceeded") || containsSubstring(lines, "context deadline exceeded")
		},
		Strategy: StrategyResume,
		Category: CategoryTransientInfra,
	},
	{
		Name: "connection refused",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "connection refused") || containsSubstring(lines, "connection reset")
		},
		Strategy: StrategyResume,
		Category: CategoryTransientInfra,
	},
	{
		Name: "network unreachable",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "network is unreachable") || containsSubstring(lines, "no route to host")
		},
		Strategy: StrategyResume,
		Category: CategoryTransientInfra,
	},
	{
		Name: "TLS handshake timeout",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "tls handshake timeout") || containsSubstring(lines, "certificate verify failed")
		},
		Strategy: StrategyResume,
		Category: CategoryTransientInfra,
	},
	{
		Name: "server error 5xx",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "500 internal server error") || containsSubstring(lines, "502 bad gateway") || containsSubstring(lines, "503 service unavailable") || containsSubstring(lines, "504 gateway timeout")
		},
		Strategy: StrategyResume,
		Category: CategoryTransientInfra,
	},

	// ── Infra-class: stall-detection signals ──
	{
		Name: "stall detected",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "stall detected") || containsSubstring(lines, "stall recovery")
		},
		Strategy: StrategyResume,
		Category: CategoryTransientInfra,
	},

	// ── Agent-class: harness-specific patterns ──
	{
		Name:     "opencode API bad request",
		Match:    func(lines []string) bool { return containsSubstring(lines, "API bad request") },
		Strategy: StrategyRotate,
		Category: CategoryAgentError,
		Harness:  "opencode",
	},
	{
		Name: "gemini-cli exit 1",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "exit status 1") &&
				containsSubstring(lines, "gemini-cli") &&
				!containsSubstring(lines, "IneligibleTierError") &&
				!containsSubstring(lines, "UNSUPPORTED_CLIENT") &&
				!containsSubstring(lines, "no longer supported for Gemini Code Assist") &&
				!containsSubstring(lines, "Error authenticating")
		},
		Strategy: StrategyResume,
		Category: CategoryAgentError,
		Harness:  "antigravity",
	},
	{
		Name: "gemini auth or unsupported client",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "IneligibleTierError") ||
				containsSubstring(lines, "UNSUPPORTED_CLIENT") ||
				containsSubstring(lines, "no longer supported for Gemini Code Assist") ||
				containsSubstring(lines, "Error authenticating")
		},
		Strategy: StrategyRotate,
		Category: CategoryAuthOrProxy,
		Harness:  "gemini",
	},
	{
		Name: "antigravity auth or unsupported client",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "IneligibleTierError") ||
				containsSubstring(lines, "UNSUPPORTED_CLIENT") ||
				containsSubstring(lines, "no longer supported for Gemini Code Assist") ||
				containsSubstring(lines, "Error authenticating")
		},
		Strategy: StrategyRotate,
		Category: CategoryAuthOrProxy,
		Harness:  "antigravity",
	},
	{
		Name: "codex completion despite limit warning",
		Match: func(lines []string) bool {
			return containsSubstring(lines, "limit warning") && containsSubstring(lines, "completion")
		},
		Strategy: StrategyNoOp,
		Category: CategoryAgentError,
		Harness:  "codex",
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
// It returns a StrategyDecision that includes the retry strategy, failure
// class, and category. The harness parameter scopes harness-specific patterns;
// pass "" for harness-agnostic classification.
//
// ctx may be nil when no incomplete-detection context is available; evidence
// may be nil when the executor supplied none. Evidence carrying a non-empty
// Category takes priority over all text-based classification.
//
// Classification priority:
//  1. Typed executor Evidence (when non-nil with a Category)
//  2. Provider/config/quota detection (future; placeholder for evidence parsers)
//  3. Dirty-tree incomplete check (file changes without finalization)
//  4. Harness-scoped text patterns from ErrorPatterns
//  5. Default unidentified_issue
//
// Unknown/unmatched errors default to FailureAgent (the does-not-freeze side).

// DirtyTreeEvidence builds the Priority-3 dirty-tree incomplete-finalization
// FailureEvidence from a bounded changed-paths list. It is the single source of
// truth for the dirty_tree evidence shape: ClassifyError's Priority 3 path and
// the runner's operator-worthy incomplete_finalization capture both build it
// here, so a Priority-3 failure always emits failure_evidence.source=dirty_tree
// with a non-empty, 256-rune-bounded raw_signal/message. When changedPaths is
// empty the raw_signal falls back to the message verbatim so the
// failure_evidence block still surfaces a non-empty diagnostic marker.
func DirtyTreeEvidence(changedPaths []string) *FailureEvidence {
	raw := truncateSignal(strings.Join(changedPaths, ", "), 256)
	if strings.TrimSpace(raw) == "" {
		raw = "agent exited without finalizing"
	}
	return &FailureEvidence{
		Category:  CategoryIncompleteFinalization,
		Message:   "agent exited without finalizing",
		Source:    "dirty_tree",
		RawSignal: raw,
	}
}

func ClassifyError(logLines []string, harness string, ctx *ClassifyContext, evidence *FailureEvidence) StrategyDecision {
	// ── Priority 1: Typed executor Evidence ──
	// When the executor has already resolved a category, trust it.
	if evidence != nil && evidence.Category != "" {
		cat := evidence.Category
		class := CategoryToClass(cat)
		// Default the source tag when the executor didn't set one.
		ev := *evidence // shallow copy — don't mutate the caller's struct
		if ev.Source == "" {
			ev.Source = "executor_evidence"
		}
		decision := StrategyDecision{
			Category:     cat,
			FailureClass: class,
			DisplayLabel: CategoryDisplayLabel(cat),
			Reason:       string(cat),
			Evidence:     &ev,
		}
		// Derive strategy from the category.
		switch cat {
		case CategoryShortRateLimit, CategoryProviderOverloaded:
			decision.Strategy = StrategyWaitResume
			if evidence.RetryAfter > 0 {
				decision.Cooldown = evidence.RetryAfter
			} else {
				decision.Cooldown = 60 * time.Second
			}
		case CategoryUsageLimit:
			decision.Strategy = StrategyWaitResume
			if evidence.RetryAfter > 0 {
				decision.Cooldown = evidence.RetryAfter
			} else {
				decision.Cooldown = 120 * time.Second
			}
		case CategoryTransientInfra:
			decision.Strategy = StrategyResume
		case CategoryHarnessLaunch:
			decision.Strategy = StrategyFreshRestart
		case CategoryInvalidModel, CategoryAuthOrProxy:
			decision.Strategy = StrategyRotate
		case CategoryIncompleteFinalization:
			decision.Strategy = StrategyResume
		default:
			decision.Strategy = StrategyFreshRestart
		}
		return decision
	}

	// ── Priority 2: Provider/config/quota detection ──
	// (Future: runner-side fallback parsers will populate evidence here.
	//  For now this is a placeholder; no behavior change vs today.)

	// ── Priority 3: Dirty-tree incomplete check ──
	if ctx != nil && ctx.HasFileChanges && !ctx.Finalized {
		return StrategyDecision{
			Strategy:     StrategyResume,
			Reason:       "incomplete: file changes without finalization",
			FailureClass: FailureIncomplete,
			Category:     CategoryIncompleteFinalization,
			DisplayLabel: CategoryDisplayLabel(CategoryIncompleteFinalization),
			Evidence:     DirtyTreeEvidence(ctx.ChangedPaths),
		}
	}

	// ── Priority 4: Harness-scoped text patterns ──
	for _, pattern := range ErrorPatterns {
		// Skip harness-scoped patterns when harness doesn't match.
		if pattern.Harness != "" && !strings.EqualFold(pattern.Harness, harness) {
			continue
		}
		if pattern.Match(logLines) {
			cat := pattern.Category
			if cat == "" {
				cat = CategoryUnidentifiedIssue
			}
			// CategoryToClass is the authoritative failure-class derivation for
			// categorized classifications (design Decision 3): the category's
			// mapping — not the pattern's literal FailureClass — decides what
			// feeds the freeze counter. This keeps e.g. the usage_limit pattern
			// from incrementing infraFailures even though its text matches the
			// rate-limit family.
			ev := &FailureEvidence{
				Category:  cat,
				Source:    "text_pattern",
				Message:   pattern.Name,
				RawSignal: extractMatchingLine(logLines, pattern),
			}
			decision := StrategyDecision{
				Strategy:     pattern.Strategy,
				Reason:       pattern.Name,
				FailureClass: CategoryToClass(cat),
				Category:     cat,
				DisplayLabel: CategoryDisplayLabel(cat),
				Evidence:     ev,
			}
			if pattern.Extract != nil {
				decision.Cooldown = pattern.Extract(logLines)
			}
			return decision
		}
	}

	// ── Priority 5: Default unidentified_issue ──
	ev := &FailureEvidence{
		Category:  CategoryUnidentifiedIssue,
		Source:    "unmatched",
		Message:   "no recognised provider signal",
		RawSignal: logTailSignal(logLines),
	}
	return StrategyDecision{
		Strategy:     StrategyFreshRestart,
		Reason:       "unknown error",
		FailureClass: FailureAgent,
		Category:     CategoryUnidentifiedIssue,
		DisplayLabel: CategoryDisplayLabel(CategoryUnidentifiedIssue),
		Evidence:     ev,
	}
}

// extractMatchingLine re-scans logLines after a pattern match to find
// a representative matching line for RawSignal (design Open Q1: re-scan,
// do NOT change the Pattern.Match API).
func extractMatchingLine(logLines []string, p Pattern) string {
	// Re-apply the match function on each individual line to find the
	// first line that satisfies the pattern on its own.
	for _, line := range logLines {
		if p.Match([]string{line}) {
			return truncateSignal(line, 256)
		}
	}
	// If no single line matches (multi-line pattern), fall back to the
	// bounded log tail.
	return logTailSignal(logLines)
}

// logTailSignal returns a bounded tail of logLines as a RawSignal string.
func logTailSignal(logLines []string) string {
	// Take the last few lines to stay bounded.
	const maxLines = 5
	start := 0
	if len(logLines) > maxLines {
		start = len(logLines) - maxLines
	}
	tail := strings.Join(logLines[start:], "\n")
	if strings.TrimSpace(tail) == "" {
		return "no log output"
	}
	return truncateSignal(tail, 256)
}
