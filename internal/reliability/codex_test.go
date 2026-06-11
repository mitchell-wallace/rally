package reliability

import (
	"testing"
	"time"
)

func TestParseCodexError(t *testing.T) {
	tests := []struct {
		name               string
		stderr             string
		expectNil          bool
		expectedCategory   FailureCategory
		expectedProvider   string
		expectedStatusCode int
		expectedRetryAfter time.Duration
		expectResetAt      bool
		expectedResetHour  int
		expectedResetMin   int
	}{
		{
			name:              "usage limit with try again at",
			stderr:            `{"type":"error","message":"You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at 5:22 AM."}`,
			expectedCategory:  CategoryUsageLimit,
			expectedProvider:  ProviderOpenAI,
			expectResetAt:     true,
			expectedResetHour: 5,
			expectedResetMin:  22,
		},
		{
			name:              "usage limit with try again at PM",
			stderr:            `{"type":"error","message":"You've hit your usage limit. Try again at 11:30 PM."}`,
			expectedCategory:  CategoryUsageLimit,
			expectedProvider:  ProviderOpenAI,
			expectResetAt:     true,
			expectedResetHour: 23,
			expectedResetMin:  30,
		},
		{
			name:             "usage limit without try again at",
			stderr:           `{"type":"error","message":"You've hit your usage limit. Upgrade to Pro."}`,
			expectedCategory: CategoryUsageLimit,
			expectedProvider: ProviderOpenAI,
			expectResetAt:    false,
		},
		{
			name: "usage limit multi-line event stream",
			stderr: `Reading additional input from stdin...
{"type":"thread.started","thread_id":"019e814d-8eec-79f0-9993-9fbd32c5b9e1"}
{"type":"turn.started"}
{"type":"error","message":"You've hit your usage limit. Try again at 5:22 AM."}
{"type":"turn.failed","error":{"message":"You've hit your usage limit. Try again at 5:22 AM."}}`,
			expectedCategory:  CategoryUsageLimit,
			expectedProvider:  ProviderOpenAI,
			expectResetAt:     true,
			expectedResetHour: 5,
			expectedResetMin:  22,
		},
		{
			name:               "authentication failed",
			stderr:             `{"type":"error","message":"Invalid API key provided"}`,
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   ProviderOpenAI,
			expectedStatusCode: 401,
		},
		{
			name:               "authentication failed explicit",
			stderr:             `{"type":"error","message":"Authentication failed: invalid credentials"}`,
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   ProviderOpenAI,
			expectedStatusCode: 401,
		},
		{
			name:               "unauthorized",
			stderr:             `{"type":"error","message":"Unauthorized: check your API key"}`,
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   ProviderOpenAI,
			expectedStatusCode: 401,
		},
		{
			name:               "model not found",
			stderr:             `{"type":"error","message":"Model not found: gpt-foo"}`,
			expectedCategory:   CategoryInvalidModel,
			expectedProvider:   ProviderOpenAI,
			expectedStatusCode: 404,
		},
		{
			name:               "rate limit",
			stderr:             `{"type":"error","message":"Rate limit exceeded. Please try again later."}`,
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   ProviderOpenAI,
			expectedStatusCode: 429,
			expectedRetryAfter: 60 * time.Second,
		},
		{
			name:               "too many requests",
			stderr:             `{"type":"error","message":"Too many requests. Slow down."}`,
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   ProviderOpenAI,
			expectedStatusCode: 429,
			expectedRetryAfter: 60 * time.Second,
		},
		{
			name:      "empty input",
			stderr:    "",
			expectNil: true,
		},
		{
			name:      "unrelated error",
			stderr:    `{"type":"error","message":"Something went wrong with processing"}`,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := ParseCodexError(tt.stderr)
			if tt.expectNil {
				if ev != nil {
					t.Fatalf("expected nil, got %+v", ev)
				}
				return
			}
			if ev == nil {
				t.Fatalf("expected non-nil evidence, got nil")
			}
			if ev.Category != tt.expectedCategory {
				t.Errorf("category = %q, want %q", ev.Category, tt.expectedCategory)
			}
			if ev.Provider != tt.expectedProvider {
				t.Errorf("provider = %q, want %q", ev.Provider, tt.expectedProvider)
			}
			if tt.expectedStatusCode != 0 && ev.StatusCode != tt.expectedStatusCode {
				t.Errorf("statusCode = %d, want %d", ev.StatusCode, tt.expectedStatusCode)
			}
			if tt.expectedRetryAfter != 0 && ev.RetryAfter != tt.expectedRetryAfter {
				t.Errorf("retryAfter = %v, want %v", ev.RetryAfter, tt.expectedRetryAfter)
			}
			if tt.expectResetAt {
				if ev.ResetAt == nil {
					t.Fatal("expected ResetAt to be set, got nil")
				}
				h, m, _ := ev.ResetAt.Clock()
				if h != tt.expectedResetHour {
					t.Errorf("ResetAt hour = %d, want %d", h, tt.expectedResetHour)
				}
				if m != tt.expectedResetMin {
					t.Errorf("ResetAt minute = %d, want %d", m, tt.expectedResetMin)
				}
			} else if ev.ResetAt != nil {
				t.Errorf("expected ResetAt to be nil, got %v", ev.ResetAt)
			}
		})
	}
}

func TestParseCodexError_PopulatesFields(t *testing.T) {
	ev := ParseCodexError(`{"type":"error","message":"You've hit your usage limit. Try again at 5:22 AM."}`)
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.RawSignal == "" {
		t.Error("expected non-empty RawSignal")
	}
	if ev.Message == "" {
		t.Error("expected non-empty Message")
	}
}

func TestParseCodexError_PriorityAuthOverRateLimit(t *testing.T) {
	ev := ParseCodexError("Authentication failed: rate limit endpoint unreachable")
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryAuthOrProxy {
		t.Errorf("category = %q, want %q (auth should take priority)", ev.Category, CategoryAuthOrProxy)
	}
}
