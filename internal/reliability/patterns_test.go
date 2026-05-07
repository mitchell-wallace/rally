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
	}{
		{
			name: "opencode API bad request",
			logLines: []string{
				"some output",
				"error: API bad request from provider",
			},
			expectedStrategy: StrategyRotate,
		},
		{
			name: "gemini-cli exit 1",
			logLines: []string{
				"running gemini-cli...",
				"fatal error",
				"exit status 1",
			},
			expectedStrategy: StrategyResume,
		},
		{
			name: "claude rate-limit interrupt with retry-after",
			logLines: []string{
				"sending to claude...",
				"error 429 Too Many Requests",
				"retry-after: 120",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 120 * time.Second,
		},
		{
			name: "claude rate-limit interrupt without retry-after",
			logLines: []string{
				"sending to claude...",
				"rate-limit exceeded",
			},
			expectedStrategy: StrategyWaitResume,
			expectedCooldown: 60 * time.Second, // default
		},
		{
			name: "codex completion despite limit warning",
			logLines: []string{
				"warning: limit warning reached",
				"completion generated",
			},
			expectedStrategy: StrategyNoOp,
		},
		{
			name: "unknown failure",
			logLines: []string{
				"some unexpected error",
				"segfault",
			},
			expectedStrategy: StrategyFreshRestart,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyError(tt.logLines)
			if decision.Strategy != tt.expectedStrategy {
				t.Errorf("expected strategy %q, got %q", tt.expectedStrategy, decision.Strategy)
			}
			if decision.Cooldown != tt.expectedCooldown {
				t.Errorf("expected cooldown %v, got %v", tt.expectedCooldown, decision.Cooldown)
			}
		})
	}
}
