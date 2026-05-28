package reliability

import (
	"testing"
	"time"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name             string
		logLines         []string
		expectedStrategy RetryStrategy
		expectedCooldown time.Duration
		expectedClass    FailureClass
	}{
		// ── Infra-class: rate limits ──
		{
			name: "claude rate-limit interrupt with retry-after",
			logLines: []string{
				"sending to claude...",
				"error 429 Too Many Requests",
				"retry-after: 120",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 120 * time.Second,
			expectedClass:    FailureInfra,
		},
		{
			name: "claude rate-limit interrupt without retry-after",
			logLines: []string{
				"sending to claude...",
				"rate-limit exceeded",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 60 * time.Second, // default
			expectedClass:    FailureInfra,
		},
		{
			name: "generic rate limit",
			logLines: []string{
				"error: rate limit reached for this API key",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 60 * time.Second,
			expectedClass:    FailureInfra,
		},
		{
			name: "too many requests generic",
			logLines: []string{
				"HTTP 429: Too Many Requests - please slow down",
			},
			expectedStrategy: StrategyWaitResume,
			// Matches "429 Too Many Requests" in the claude rate-limit pattern first
			expectedCooldown: 60 * time.Second,
			expectedClass:    FailureInfra,
		},
		{
			name: "usage limit hit",
			logLines: []string{
				"You've hit your usage limit. Upgrade to Pro.",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 120 * time.Second,
			expectedClass:    FailureInfra,
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
		},
		{
			name: "argument list too long standalone",
			logLines: []string{
				"error: argument list too long when spawning process",
			},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureInfra,
		},
		{
			name: "fork/exec error",
			logLines: []string{
				"fork/exec /usr/local/bin/agent: permission denied",
			},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureInfra,
		},
		{
			name: "exec format error",
			logLines: []string{
				"exec format error: cannot run binary",
			},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureInfra,
		},
		{
			name: "harness not found",
			logLines: []string{
				"exec: \"claude\": executable file not found in $PATH",
			},
			expectedStrategy: StrategyRotate,
			expectedClass:    FailureInfra,
		},

		// ── Infra-class: API timeout / network stall ──
		{
			name: "request timed out",
			logLines: []string{
				"API call failed: request timed out after 30s",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "context deadline exceeded",
			logLines: []string{
				"error: context deadline exceeded",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "deadline exceeded generic",
			logLines: []string{
				"grpc: deadline exceeded waiting for response",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "connection refused",
			logLines: []string{
				"dial tcp 127.0.0.1:8080: connection refused",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "connection reset",
			logLines: []string{
				"read tcp: connection reset by peer",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "network unreachable",
			logLines: []string{
				"dial tcp: network is unreachable",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "no route to host",
			logLines: []string{
				"dial tcp: no route to host",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "TLS handshake timeout",
			logLines: []string{
				"net/http: TLS handshake timeout",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "certificate verify failed",
			logLines: []string{
				"x509: certificate verify failed",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "500 internal server error",
			logLines: []string{
				"HTTP 500 Internal Server Error from API",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "502 bad gateway",
			logLines: []string{
				"error: 502 Bad Gateway",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "503 service unavailable",
			logLines: []string{
				"error: 503 Service Unavailable",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "504 gateway timeout",
			logLines: []string{
				"error: 504 Gateway Timeout",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},

		// ── Infra-class: stall-detection signals ──
		{
			name: "stall detected",
			logLines: []string{
				"relay 1 run 1 attempt 1: stall detected, killing process",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},
		{
			name: "stall recovery",
			logLines: []string{
				"relay 1 run 1 attempt 1 stall recovery: files committed, treating as success",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureInfra,
		},

		// ── Agent-class patterns ──
		{
			name: "opencode API bad request",
			logLines: []string{
				"some output",
				"error: API bad request from provider",
			},
			expectedStrategy: StrategyRotate,
			expectedClass:    FailureAgent,
		},
		{
			name: "gemini-cli exit 1",
			logLines: []string{
				"running gemini-cli...",
				"fatal error",
				"exit status 1",
			},
			expectedStrategy: StrategyResume,
			expectedClass:    FailureAgent,
		},
		{
			name: "codex completion despite limit warning",
			logLines: []string{
				"warning: limit warning reached",
				"completion generated",
			},
			expectedStrategy: StrategyNoOp,
			expectedClass:    FailureAgent,
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
		},
		{
			name: "empty log defaults to agent class",
			logLines:         []string{},
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureAgent,
		},
		{
			name: "nil log defaults to agent class",
			logLines:         nil,
			expectedStrategy: StrategyFreshRestart,
			expectedClass:    FailureAgent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyError(tt.logLines)
			if decision.Strategy != tt.expectedStrategy {
				t.Errorf("expected strategy %q, got %q (reason: %s)", tt.expectedStrategy, decision.Strategy, decision.Reason)
			}
			if decision.Cooldown != tt.expectedCooldown {
				t.Errorf("expected cooldown %v, got %v", tt.expectedCooldown, decision.Cooldown)
			}
			if decision.FailureClass != tt.expectedClass {
				t.Errorf("expected failure class %q, got %q (reason: %s)", tt.expectedClass, decision.FailureClass, decision.Reason)
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
			name:     "nil context uses pattern matching",
			logLines: []string{"fork/exec /bin/agent: permission denied"},
			ctx:      nil,
			expectedClass: FailureInfra,
			expectedName:  "fork/exec error",
		},
		{
			name:     "incomplete takes priority over pattern match",
			logLines: []string{"error 429 Too Many Requests"},
			ctx: &ClassifyContext{
				HasFileChanges: true,
				Finalized:      false,
			},
			expectedClass: FailureIncomplete,
			expectedName:  "incomplete: file changes without finalization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyError(tt.logLines, tt.ctx)
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
	// Verify that calling ClassifyError without context works (variadic).
	decision := ClassifyError([]string{"fork/exec /bin/agent: error"})
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
	// Every pattern in the table must have a non-empty FailureClass.
	for _, p := range ErrorPatterns {
		if p.FailureClass == "" {
			t.Errorf("pattern %q has no FailureClass set", p.Name)
		}
	}
}
