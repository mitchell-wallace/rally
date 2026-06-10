package reliability

import (
	"testing"
	"time"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name             string
		logLines         []string
		harness          string // harness passed to ClassifyError
		expectedStrategy RetryStrategy
		expectedCooldown time.Duration
		expectedClass    FailureClass
		expectedCategory FailureCategory
	}{
		// ── Infra-class: rate limits ──
		{
			name:    "claude rate-limit interrupt with retry-after",
			harness: "claude",
			logLines: []string{
				"sending to claude...",
				"error 429 Too Many Requests",
				"retry-after: 120",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 120 * time.Second,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryShortRateLimit,
		},
		{
			name:    "claude rate-limit interrupt without retry-after",
			harness: "claude",
			logLines: []string{
				"sending to claude...",
				"rate-limit exceeded",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 60 * time.Second, // default
			expectedClass:    FailureInfra,
			expectedCategory: CategoryShortRateLimit,
		},
		{
			name: "generic rate limit",
			logLines: []string{
				"error: rate limit reached for this API key",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 60 * time.Second,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryShortRateLimit,
		},
		{
			name: "too many requests generic",
			logLines: []string{
				"HTTP 429: Too Many Requests - please slow down",
			},
			expectedStrategy: StrategyWaitResume,
			// Matches "too many requests" in the generic rate-limit pattern
			// (claude-scoped pattern is skipped since no harness specified)
			expectedCooldown: 60 * time.Second,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryShortRateLimit,
		},
		{
			name: "usage limit hit",
			logLines: []string{
				"You've hit your usage limit. Upgrade to Pro.",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 120 * time.Second,
			// usage_limit maps to FailureAgent via CategoryToClass (the
			// authoritative class derivation), so it must NOT feed the freeze
			// counter even though the pattern text resembles a rate limit.
			expectedClass:    FailureAgent,
			expectedCategory: CategoryUsageLimit,
		},

		// ── Infra-class: harness/launch errors ──
		{
			name: "argument list too long",
			logLines: []string{
				"fork/exec /usr/bin/claude: argument list too long",
			},
			// fork/exec matches first in pattern order
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryHarnessLaunch,
		},
		{
			name: "argument list too long standalone",
			logLines: []string{
				"error: argument list too long when spawning process",
			},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryHarnessLaunch,
		},
		{
			name: "fork/exec error",
			logLines: []string{
				"fork/exec /usr/local/bin/agent: permission denied",
			},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryHarnessLaunch,
		},
		{
			name: "exec format error",
			logLines: []string{
				"exec format error: cannot run binary",
			},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryHarnessLaunch,
		},
		{
			name: "harness not found",
			logLines: []string{
				"exec: \"claude\": executable file not found in $PATH",
			},
			expectedStrategy: StrategyRotate,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryHarnessLaunch,
		},

		// ── Infra-class: API timeout / network stall ──
		{
			name: "request timed out",
			logLines: []string{
				"API call failed: request timed out after 30s",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "context deadline exceeded",
			logLines: []string{
				"error: context deadline exceeded",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "deadline exceeded generic",
			logLines: []string{
				"grpc: deadline exceeded waiting for response",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "connection refused",
			logLines: []string{
				"dial tcp 127.0.0.1:8080: connection refused",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "connection reset",
			logLines: []string{
				"read tcp: connection reset by peer",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "network unreachable",
			logLines: []string{
				"dial tcp: network is unreachable",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "no route to host",
			logLines: []string{
				"dial tcp: no route to host",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "TLS handshake timeout",
			logLines: []string{
				"net/http: TLS handshake timeout",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "certificate verify failed",
			logLines: []string{
				"x509: certificate verify failed",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "500 internal server error",
			logLines: []string{
				"HTTP 500 Internal Server Error from API",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "502 bad gateway",
			logLines: []string{
				"error: 502 Bad Gateway",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "503 service unavailable",
			logLines: []string{
				"error: 503 Service Unavailable",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "504 gateway timeout",
			logLines: []string{
				"error: 504 Gateway Timeout",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},

		// ── Infra-class: stall-detection signals ──
		{
			name: "stall detected",
			logLines: []string{
				"relay 1 run 1 attempt 1: stall detected, killing process",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},
		{
			name: "stall recovery",
			logLines: []string{
				"relay 1 run 1 attempt 1 stall recovery: files committed, treating as success",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
			expectedCategory: CategoryTransientInfra,
		},

		// ── Agent-class patterns (harness-scoped) ──
		{
			name:    "opencode API bad request",
			harness: "opencode",
			logLines: []string{
				"some output",
				"error: API bad request from provider",
			},
			expectedStrategy: StrategyRotate,
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
		{
			name:    "gemini-cli exit 1",
			harness: "antigravity",
			logLines: []string{
				"running gemini-cli...",
				"fatal error",
				"exit status 1",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
		{
			name:    "codex completion despite limit warning",
			harness: "codex",
			logLines: []string{
				"warning: limit warning reached",
				"completion generated",
			},
			expectedStrategy: StrategyNoOp,
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},

		// ── Unknown failures default to FailureAgent ──
		{
			name: "unknown failure defaults to agent class",
			logLines: []string{
				"some unexpected error",
				"segfault",
			},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
		{
			name:             "empty log defaults to agent class",
			logLines:         []string{},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
		{
			name:             "nil log defaults to agent class",
			logLines:         nil,
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyError(tt.logLines, tt.harness)
			if decision.Strategy != tt.expectedStrategy {
				t.Errorf("expected strategy %q, got %q (reason: %s)", tt.expectedStrategy, decision.Strategy, decision.Reason)
			}
			if decision.Cooldown != tt.expectedCooldown {
				t.Errorf("expected cooldown %v, got %v", tt.expectedCooldown, decision.Cooldown)
			}
			if decision.FailureClass != tt.expectedClass {
				t.Errorf("expected failure class %q, got %q (reason: %s)", tt.expectedClass, decision.FailureClass, decision.Reason)
			}
			if tt.expectedCategory != "" && decision.Category != tt.expectedCategory {
				t.Errorf("expected category %q, got %q (reason: %s)", tt.expectedCategory, decision.Category, decision.Reason)
			}
		})
	}
}

func TestClassifyError_IncompleteContext(t *testing.T) {
	tests := []struct {
		name          string
		logLines      []string
		ctx           *ClassifyContext
		expectedClass FailureClass
		expectedName  string
	}{
		{
			name:     "incomplete: has file changes but not finalized",
			logLines: []string{"some error"},
			ctx: &ClassifyContext{
				HasFileChanges: true,
				Finalized:      false,
			},
			expectedClass: FailureIncomplete,
			expectedName:  "incomplete: file changes without finalization",
		},
		{
			name:     "not incomplete: finalized even with file changes",
			logLines: []string{"some unexpected error"},
			ctx: &ClassifyContext{
				HasFileChanges: true,
				Finalized:      true,
			},
			expectedClass: FailureAgent, // falls through to unknown
			expectedName:  "unknown error",
		},
		{
			name:     "not incomplete: no file changes",
			logLines: []string{"some unexpected error"},
			ctx: &ClassifyContext{
				HasFileChanges: false,
				Finalized:      false,
			},
			expectedClass: FailureAgent, // falls through to unknown
			expectedName:  "unknown error",
		},
		{
			name:          "nil context uses pattern matching",
			logLines:      []string{"fork/exec /bin/agent: permission denied"},
			ctx:           nil,
			expectedClass: FailureInfra,
			expectedName:  "fork/exec error",
		},
		{
			name:     "incomplete below provider patterns: rate limit wins",
			logLines: []string{"error 429 Too Many Requests"},
			ctx: &ClassifyContext{
				HasFileChanges: true,
				Finalized:      false,
			},
			// The dirty-tree incomplete check is now *below* pattern matching
			// for provider/config/quota patterns, but still above harness-
			// scoped text patterns. A generic rate limit (harness-agnostic)
			// matches before the incomplete check only when it's a real rate
			// limit signal — but in the new ordering, incomplete check is
			// priority 3 (before text patterns priority 4). However, the
			// "rate limit generic" pattern is harness-agnostic (no Harness
			// field), so it matches in priority 4. The incomplete check at
			// priority 3 wins.
			expectedClass: FailureIncomplete,
			expectedName:  "incomplete: file changes without finalization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyError(tt.logLines, "", tt.ctx)
			if decision.FailureClass != tt.expectedClass {
				t.Errorf("expected failure class %q, got %q (reason: %s)", tt.expectedClass, decision.FailureClass, decision.Reason)
			}
			if decision.Reason != tt.expectedName {
				t.Errorf("expected reason %q, got %q", tt.expectedName, decision.Reason)
			}
		})
	}
}

func TestClassifyError_BackwardCompatibility(t *testing.T) {
	// Verify that calling ClassifyError with empty harness works.
	decision := ClassifyError([]string{"fork/exec /bin/agent: error"}, "")
	if decision.FailureClass != FailureInfra {
		t.Errorf("expected FailureInfra, got %q", decision.FailureClass)
	}
	if decision.Reason != "fork/exec error" {
		t.Errorf("expected reason 'fork/exec error', got %q", decision.Reason)
	}
}

func TestFailureClassValues(t *testing.T) {
	// Verify the enum values are as documented.
	if FailureInfra != "infra" {
		t.Errorf("FailureInfra should be 'infra', got %q", FailureInfra)
	}
	if FailureAgent != "agent" {
		t.Errorf("FailureAgent should be 'agent', got %q", FailureAgent)
	}
	if FailureIncomplete != "incomplete" {
		t.Errorf("FailureIncomplete should be 'incomplete', got %q", FailureIncomplete)
	}
}

func TestErrorPatterns_AllTagged(t *testing.T) {
	// Every pattern in the table must have a non-empty FailureClass and Category.
	for _, p := range ErrorPatterns {
		if p.FailureClass == "" {
			t.Errorf("pattern %q has no FailureClass set", p.Name)
		}
		if p.Category == "" {
			t.Errorf("pattern %q has no Category set", p.Name)
		}
	}
}

// ── Harness scoping tests ──

func TestClassifyError_HarnessScoping(t *testing.T) {
	tests := []struct {
		name             string
		logLines         []string
		harness          string
		expectedReason   string
		expectedClass    FailureClass
		expectedCategory FailureCategory
	}{
		{
			// The "claude rate-limit interrupt" pattern is scoped to claude.
			// When harness is "codex", the claude-specific pattern is skipped,
			// but the generic "rate limit" pattern (harness-agnostic) matches
			// "too many requests" only if present. "rate-limit" as a hyphenated
			// word does NOT match "rate limit" (space). So codex with only
			// the prose "rate-limit" falls through to the default.
			name:             "codex log with rate-limit prose does NOT classify as claude rate limit",
			logLines:         []string{"The agent encountered a rate-limit from the provider"},
			harness:          "codex",
			expectedReason:   "unknown error",
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
		{
			// But the same log on claude harness DOES match.
			name:             "claude log with rate-limit matches",
			logLines:         []string{"The agent encountered a rate-limit from the provider"},
			harness:          "claude",
			expectedReason:   "claude rate-limit interrupt",
			expectedClass:    FailureInfra,
			expectedCategory: CategoryShortRateLimit,
		},
		{
			// Harness-agnostic patterns match regardless of harness.
			name:             "generic rate limit matches any harness",
			logLines:         []string{"error: rate limit exceeded"},
			harness:          "codex",
			expectedReason:   "rate limit generic",
			expectedClass:    FailureInfra,
			expectedCategory: CategoryShortRateLimit,
		},
		{
			// opencode-scoped pattern doesn't match on claude
			name:             "opencode API bad request on claude harness is unknown",
			logLines:         []string{"error: API bad request from provider"},
			harness:          "claude",
			expectedReason:   "unknown error",
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
		{
			// opencode-scoped pattern matches on opencode
			name:             "opencode API bad request on opencode harness",
			logLines:         []string{"error: API bad request from provider"},
			harness:          "opencode",
			expectedReason:   "opencode API bad request",
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
		{
			// codex-scoped pattern doesn't match on antigravity
			name:             "codex completion pattern on antigravity harness is unknown",
			logLines:         []string{"warning: limit warning reached", "completion generated"},
			harness:          "antigravity",
			expectedReason:   "unknown error",
			expectedClass:    FailureAgent,
			expectedCategory: CategoryAgentError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyError(tt.logLines, tt.harness)
			if decision.Reason != tt.expectedReason {
				t.Errorf("expected reason %q, got %q", tt.expectedReason, decision.Reason)
			}
			if decision.FailureClass != tt.expectedClass {
				t.Errorf("expected failure class %q, got %q", tt.expectedClass, decision.FailureClass)
			}
			if decision.Category != tt.expectedCategory {
				t.Errorf("expected category %q, got %q", tt.expectedCategory, decision.Category)
			}
		})
	}
}

func TestClassifyError_EvidencePriority(t *testing.T) {
	// When evidence is provided with a category, it takes priority over
	// text-based classification.
	evidence := &FailureEvidence{
		Category:   CategoryUsageLimit,
		RetryAfter: 300 * time.Second,
	}
	logLines := []string{"fork/exec /bin/agent: error"} // would match harness_launch
	decision := ClassifyError(logLines, "", nil, evidence)

	if decision.Category != CategoryUsageLimit {
		t.Errorf("expected category %q, got %q", CategoryUsageLimit, decision.Category)
	}
	if decision.FailureClass != FailureAgent { // usage_limit maps to agent
		t.Errorf("expected failure class %q, got %q", FailureAgent, decision.FailureClass)
	}
	if decision.Strategy != StrategyWaitResume {
		t.Errorf("expected strategy %q, got %q", StrategyWaitResume, decision.Strategy)
	}
	if decision.Cooldown != 300*time.Second {
		t.Errorf("expected cooldown 5m, got %v", decision.Cooldown)
	}
}

func TestClassifyError_NilEvidence(t *testing.T) {
	// Nil evidence should be tolerated (process-level harness_launch
	// failures have no TryResult).
	var evidence *FailureEvidence
	decision := ClassifyError([]string{"fork/exec /bin/agent: error"}, "", nil, evidence)
	if decision.FailureClass != FailureInfra {
		t.Errorf("expected FailureInfra, got %q", decision.FailureClass)
	}
	if decision.Category != CategoryHarnessLaunch {
		t.Errorf("expected category %q, got %q", CategoryHarnessLaunch, decision.Category)
	}
}

func TestClassifyError_EmptyEvidence(t *testing.T) {
	// Evidence with empty category falls through to text-based classification.
	evidence := &FailureEvidence{}
	decision := ClassifyError([]string{"fork/exec /bin/agent: error"}, "", nil, evidence)
	if decision.FailureClass != FailureInfra {
		t.Errorf("expected FailureInfra, got %q", decision.FailureClass)
	}
	if decision.Category != CategoryHarnessLaunch {
		t.Errorf("expected category %q, got %q", CategoryHarnessLaunch, decision.Category)
	}
}

func TestClassifyError_CategoryToClassAuthoritativeForTextPatterns(t *testing.T) {
	// The text-pattern path must derive FailureClass from the category via
	// CategoryToClass, NOT from the pattern's literal FailureClass field. This
	// guards the load-bearing freeze-counter invariant: a usage_limit text
	// match maps to FailureAgent (does-not-freeze) even though the pattern
	// entry is tagged FailureInfra for documentation.
	decision := ClassifyError([]string{"You've hit your usage limit. Upgrade to Pro."}, "")
	if decision.Category != CategoryUsageLimit {
		t.Fatalf("expected category %q, got %q", CategoryUsageLimit, decision.Category)
	}
	if decision.FailureClass != CategoryToClass(CategoryUsageLimit) {
		t.Errorf("expected failure class to equal CategoryToClass(usage_limit)=%q, got %q",
			CategoryToClass(CategoryUsageLimit), decision.FailureClass)
	}
	if decision.FailureClass == FailureInfra {
		t.Errorf("usage_limit must not classify as FailureInfra (would feed the freeze counter)")
	}

	// Every categorized pattern's decision class must agree with CategoryToClass
	// for its category, regardless of the pattern's literal FailureClass field.
	for _, p := range ErrorPatterns {
		if p.Category == "" {
			continue
		}
		want := CategoryToClass(p.Category)
		d := ClassifyError(nil, p.Harness, nil, &FailureEvidence{Category: p.Category})
		if d.FailureClass != want {
			t.Errorf("pattern %q (category %q): expected class %q, got %q",
				p.Name, p.Category, want, d.FailureClass)
		}
	}
}

func TestClassifyError_DisplayLabels(t *testing.T) {
	// Verify display labels don't contain harness names for harness-agnostic
	// patterns.
	decision := ClassifyError([]string{"connection refused"}, "")
	if decision.DisplayLabel == "" {
		t.Error("expected non-empty display label")
	}
	// transient_infra label is "infra error"
	if decision.DisplayLabel != "infra error" {
		t.Errorf("expected display label %q, got %q", "infra error", decision.DisplayLabel)
	}
}
