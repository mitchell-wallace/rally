package reliability

import (
	"testing"
)

func TestRegression_AntigravityQuotaZeroChangedFiles_UsageLimit(t *testing.T) {
	stderr := "Error: RESOURCE_EXHAUSTED: Individual quota reached for generativelanguage.googleapis.com. Resets in 7d."
	ev := ParseGeminiError(stderr)
	if ev == nil {
		t.Fatal("ParseGeminiError: expected non-nil evidence")
	}
	if ev.Category != CategoryUsageLimit {
		t.Fatalf("ParseGeminiError: category = %q, want %q", ev.Category, CategoryUsageLimit)
	}
	if ev.Provider != ProviderGemini {
		t.Errorf("provider = %q, want %q", ev.Provider, ProviderGemini)
	}

	decision := ClassifyError(nil, "antigravity",
		&ClassifyContext{HasFileChanges: false, Finalized: false},
		ev,
	)
	if decision.Category != CategoryUsageLimit {
		t.Errorf("ClassifyError: category = %q, want %q", decision.Category, CategoryUsageLimit)
	}
	if decision.FailureClass != FailureAgent {
		t.Errorf("ClassifyError: class = %q, want %q (usage_limit must not freeze)", decision.FailureClass, FailureAgent)
	}
	if decision.Strategy != StrategyWaitResume {
		t.Errorf("ClassifyError: strategy = %q, want %q", decision.Strategy, StrategyWaitResume)
	}
}

func TestRegression_AntigravityQuotaSettingsDirty_UsageLimitNotIncomplete(t *testing.T) {
	stderr := "RESOURCE_EXHAUSTED: Individual quota reached. Resets in 2h."
	ev := ParseGeminiError(stderr)
	if ev == nil {
		t.Fatal("ParseGeminiError: expected non-nil evidence")
	}

	decision := ClassifyError(nil, "antigravity",
		&ClassifyContext{HasFileChanges: true, Finalized: false},
		ev,
	)
	if decision.Category != CategoryUsageLimit {
		t.Errorf("category = %q, want %q (evidence must beat dirty-tree check)", decision.Category, CategoryUsageLimit)
	}
	if decision.Category == CategoryIncompleteFinalization {
		t.Error("must NOT be incomplete_finalization — structured evidence from harness parser wins")
	}
	if decision.FailureClass != FailureAgent {
		t.Errorf("class = %q, want %q", decision.FailureClass, FailureAgent)
	}
}

func TestRegression_CodexVerifyRateLimitProse_NotClaudeRateLimit(t *testing.T) {
	logLines := []string{
		"VERIFY lap 3: checking prior work",
		"The upstream provider returned a rate-limit warning; retries may succeed later",
		"codex exited with code 1",
	}
	decision := ClassifyError(logLines, "codex", nil, nil)
	if decision.Category == CategoryShortRateLimit {
		t.Errorf("codex harness must NOT match claude-scoped rate-limit pattern; got category %q, reason %q",
			decision.Category, decision.Reason)
	}
	if decision.FailureClass != FailureAgent {
		t.Errorf("class = %q, want %q (default for unmatched on codex)", decision.FailureClass, FailureAgent)
	}
	if decision.Category != CategoryAgentError {
		t.Errorf("category = %q, want %q", decision.Category, CategoryAgentError)
	}
}

func TestRegression_ClaudeInvalidModelSettingsDirty_InvalidModelNotIncomplete(t *testing.T) {
	stderr := `{"type":"error","error":{"type":"not_found_error","message":"model_not_found: The model 'claude-foo' does not exist."}}`
	ev := ParseClaudeError(stderr)
	if ev == nil {
		t.Fatal("ParseClaudeError: expected non-nil evidence")
	}
	if ev.Category != CategoryInvalidModel {
		t.Fatalf("ParseClaudeError: category = %q, want %q", ev.Category, CategoryInvalidModel)
	}

	decision := ClassifyError(nil, "claude",
		&ClassifyContext{HasFileChanges: true, Finalized: false},
		ev,
	)
	if decision.Category != CategoryInvalidModel {
		t.Errorf("category = %q, want %q (evidence must beat dirty-tree check)", decision.Category, CategoryInvalidModel)
	}
	if decision.Category == CategoryIncompleteFinalization {
		t.Error("must NOT be incomplete_finalization — structured invalid_model evidence wins")
	}
	if decision.FailureClass != FailureAgent {
		t.Errorf("class = %q, want %q", decision.FailureClass, FailureAgent)
	}
	if decision.Strategy != StrategyRotate {
		t.Errorf("strategy = %q, want %q", decision.Strategy, StrategyRotate)
	}
}

func TestRegression_TaskFileDirtyNoFinalization_IncompleteFinalization(t *testing.T) {
	logLines := []string{
		"lap 5: implementing feature X",
		"wrote src/feature.go",
		"updated tests in src/feature_test.go",
		"agent process exited without calling laps done",
	}
	decision := ClassifyError(logLines, "opencode",
		&ClassifyContext{HasFileChanges: true, Finalized: false},
		nil,
	)
	if decision.Category != CategoryIncompleteFinalization {
		t.Errorf("category = %q, want %q", decision.Category, CategoryIncompleteFinalization)
	}
	if decision.FailureClass != FailureIncomplete {
		t.Errorf("class = %q, want %q", decision.FailureClass, FailureIncomplete)
	}
	if decision.Strategy != StrategyResume {
		t.Errorf("strategy = %q, want %q", decision.Strategy, StrategyResume)
	}
	if decision.DisplayLabel != CategoryDisplayLabel(CategoryIncompleteFinalization) {
		t.Errorf("displayLabel = %q, want %q", decision.DisplayLabel, CategoryDisplayLabel(CategoryIncompleteFinalization))
	}
}

func TestRegression_ShortRateLimit_IsInfraClassNotAgentError(t *testing.T) {
	logLines := []string{
		"some agent output...",
		"rate limit exceeded, please try again later",
		"process exited with code 1",
	}
	decision := ClassifyError(logLines, "any-harness", nil, nil)
	if decision.Category != CategoryShortRateLimit {
		t.Errorf("category = %q, want %q", decision.Category, CategoryShortRateLimit)
	}
	if decision.FailureClass != FailureInfra {
		t.Errorf("class = %q, want %q (rate-limit is infra, not agent)", decision.FailureClass, FailureInfra)
	}
	if decision.Strategy != StrategyWaitResume {
		t.Errorf("strategy = %q, want %q", decision.Strategy, StrategyWaitResume)
	}
	if decision.Cooldown == 0 {
		t.Error("cooldown must be > 0 for rate-limit errors")
	}
}

func TestRegression_APITimeoutConnectionReset_TransientInfraNotAgentError(t *testing.T) {
	tests := []struct {
		name     string
		logLines []string
		harness  string
	}{
		{
			name: "connection reset by peer",
			logLines: []string{
				"sending request to API...",
				"read tcp 10.0.1.5:49152->34.120.100.20:443: read: connection reset by peer",
				"process exited with code 1",
			},
			harness: "antigravity",
		},
		{
			name: "context deadline exceeded",
			logLines: []string{
				"waiting for API response...",
				"error: context deadline exceeded after 120s",
			},
			harness: "claude",
		},
		{
			name: "request timed out",
			logLines: []string{
				"posting to provider endpoint...",
				"request timed out after 60s",
			},
			harness: "opencode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ClassifyError(tt.logLines, tt.harness, nil, nil)
			if decision.Category != CategoryTransientInfra {
				t.Errorf("category = %q, want %q (reason: %s)", decision.Category, CategoryTransientInfra, decision.Reason)
			}
			if decision.FailureClass != FailureInfra {
				t.Errorf("class = %q, want %q (infra-class, not agent_error)", decision.FailureClass, FailureInfra)
			}
			if decision.Category == CategoryAgentError {
				t.Error("must NOT be agent_error — transient infra failures must classify as infra-class")
			}
			if decision.Strategy != StrategyResume {
				t.Errorf("strategy = %q, want %q", decision.Strategy, StrategyResume)
			}
			if decision.Cooldown != 0 {
				t.Errorf("cooldown = %v, want 0 for non-rate-limit infra", decision.Cooldown)
			}
		})
	}
}
