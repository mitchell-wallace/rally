package reliability

import (
	"testing"
	"time"
)

func TestParseOpencodeError(t *testing.T) {
	tests := []struct {
		name               string
		stderr             string
		model              string
		expectNil          bool
		expectedCategory   FailureCategory
		expectedProvider   string
		expectedStatusCode int
		expectedRetryAfter time.Duration
		expectedResetAfter time.Duration
	}{
		{
			name:               "rate limit error",
			stderr:             `{"type":"error","timestamp":1780285834220,"sessionID":"ses_abc","error":{"name":"RateLimitError","data":{"message":"Rate limit exceeded. Retry after 120 seconds.","ref":"err_123"}}}`,
			model:              "openai/gpt-5",
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   "openai",
			expectedStatusCode: 429,
			expectedRetryAfter: 120 * time.Second,
		},
		{
			name:               "rate limit default retry",
			stderr:             `{"type":"error","error":{"name":"RateLimitError","data":{"message":"Too many requests.","ref":"err_456"}}}`,
			model:              "anthropic/claude-sonnet-4",
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   "anthropic",
			expectedStatusCode: 429,
			expectedRetryAfter: 60 * time.Second,
		},
		{
			name:               "usage limit error",
			stderr:             `{"type":"error","error":{"name":"UsageLimitError","data":{"message":"Usage limit reached. Resets in 24h.","ref":"err_789"}}}`,
			model:              "openai/gpt-5",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   "openai",
			expectedStatusCode: 429,
			expectedResetAfter: 24 * time.Hour,
		},
		{
			name:               "quota exceeded error",
			stderr:             `{"type":"error","error":{"name":"QuotaExceededError","data":{"message":"Quota exceeded for this account."}}}`,
			model:              "google/gemini-pro",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   "google",
			expectedStatusCode: 429,
		},
		{
			name:               "resource exhausted error",
			stderr:             `{"type":"error","error":{"name":"ResourceExhausted","data":{"message":"RESOURCE_EXHAUSTED: Quota exceeded. Resets in 7d."}}}`,
			model:              "google/gemini-flash",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   "google",
			expectedStatusCode: 429,
			expectedResetAfter: 7 * 24 * time.Hour,
		},
		{
			name:               "authentication error",
			stderr:             `{"type":"error","error":{"name":"AuthenticationError","data":{"message":"Invalid API key provided.","ref":"err_auth"}}}`,
			model:              "openai/gpt-5",
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   "openai",
			expectedStatusCode: 401,
		},
		{
			name:               "permission denied error",
			stderr:             `{"type":"error","error":{"name":"PermissionDeniedError","data":{"message":"Permission denied."}}}`,
			model:              "anthropic/claude-sonnet-4",
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   "anthropic",
			expectedStatusCode: 401,
		},
		{
			name:               "unauthorized error",
			stderr:             `{"type":"error","error":{"name":"UnauthorizedError","data":{"message":"Unauthorized access."}}}`,
			model:              "zai-coding-plan/glm-5.1",
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   "zai-coding-plan",
			expectedStatusCode: 401,
		},
		{
			name:               "model not found error",
			stderr:             `{"type":"error","error":{"name":"NotFoundError","data":{"message":"model not found: gpt-foo does not exist"}}}`,
			model:              "openai/gpt-foo",
			expectedCategory:   CategoryInvalidModel,
			expectedProvider:   "openai",
			expectedStatusCode: 404,
		},
		{
			name:               "overloaded error",
			stderr:             `{"type":"error","error":{"name":"OverloadedError","data":{"message":"Provider is overloaded. Try again later."}}}`,
			model:              "anthropic/claude-sonnet-4",
			expectedCategory:   CategoryProviderOverloaded,
			expectedProvider:   "anthropic",
			expectedStatusCode: 503,
			expectedRetryAfter: 30 * time.Second,
		},
		{
			name:               "server error 503",
			stderr:             `{"type":"error","error":{"name":"ServerError","data":{"message":"503 Service Unavailable"}}}`,
			model:              "openai/gpt-5",
			expectedCategory:   CategoryProviderOverloaded,
			expectedProvider:   "openai",
			expectedStatusCode: 503,
			expectedRetryAfter: 30 * time.Second,
		},
		{
			name:      "wrapper-only unknown error",
			stderr:    `{"type":"error","timestamp":1780285834220,"sessionID":"ses_17eb1fcb4ffeaM4Hrx1qJbTQHa","error":{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_e558e8ba"}}}`,
			model:     "zai-coding-plan/glm-5.1",
			expectNil: true,
		},
		{
			name:             "specific unknown error",
			stderr:           `{"type":"error","timestamp":1780285834220,"sessionID":"ses_agent","error":{"name":"UnknownError","data":{"message":"Agent runtime crashed while applying patch.","ref":"err_agent"}}}`,
			model:            "zai-coding-plan/glm-5.1",
			expectedCategory: CategoryAgentError,
			expectedProvider: "zai-coding-plan",
		},
		{
			name:             "missing error payload",
			stderr:           `{"type":"error"}`,
			model:            "openai/gpt-5",
			expectedCategory: CategoryUnidentifiedIssue,
			expectedProvider: "openai",
		},
		{
			name:      "empty input",
			stderr:    "",
			model:     "openai/gpt-5",
			expectNil: true,
		},
		{
			name:      "no error event",
			stderr:    `{"type":"text","part":{"type":"text","text":"hello"}}`,
			model:     "openai/gpt-5",
			expectNil: true,
		},
		{
			name:      "plain text no json",
			stderr:    "something went wrong",
			model:     "openai/gpt-5",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := ParseOpencodeError(tt.stderr, tt.model)
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
			if tt.expectedResetAfter != 0 && ev.ResetAfter != tt.expectedResetAfter {
				t.Errorf("resetAfter = %v, want %v", ev.ResetAfter, tt.expectedResetAfter)
			}
		})
	}
}

func TestParseOpencodeError_PopulatesFields(t *testing.T) {
	ev := ParseOpencodeError(
		`{"type":"error","error":{"name":"RateLimitError","data":{"message":"Rate limit exceeded.","ref":"err_abc"}}}`,
		"openai/gpt-5",
	)
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

func TestParseOpencodeError_ProviderFromModel(t *testing.T) {
	tests := []struct {
		model   string
		want    string
		wantNil bool
	}{
		{"openai/gpt-5", "openai", false},
		{"anthropic/claude-sonnet-4", "anthropic", false},
		{"zai-coding-plan/glm-5.1", "zai-coding-plan", false},
		{"google/gemini-pro", "google", false},
		{"no-slash-model", "no-slash-model", false},
		{"", "", false},
	}

	stderr := `{"type":"error","error":{"name":"UnknownError","data":{"message":"test"}}}`

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			ev := ParseOpencodeError(stderr, tt.model)
			if ev == nil {
				t.Fatal("expected non-nil evidence")
			}
			if ev.Provider != tt.want {
				t.Errorf("provider = %q, want %q", ev.Provider, tt.want)
			}
		})
	}
}

func TestParseOpencodeError_SubscriptionUsageLimitWrappers(t *testing.T) {
	tests := []struct {
		name               string
		stderr             string
		model              string
		expectedResetAfter time.Duration
		expectedResetAt    *time.Time
	}{
		{
			name:               "opencode-go structured AI_APICallError wrapper",
			stderr:             `{"type":"error","error":{"name":"UnknownError","data":{"message":"AI_APICallError: Monthly usage limit reached. Resets in 7 days."}}}`,
			model:              "opencode/opencode-go",
			expectedResetAfter: 7 * 24 * time.Hour,
		},
		{
			name:               "opencode-go structured AI_RetryError wrapper",
			stderr:             `{"type":"error","error":{"name":"UnknownError","data":{"message":"AI_RetryError: Failed after 3 attempts. Last error: Monthly usage limit reached. Resets in 7 days."}}}`,
			model:              "opencode/opencode-go",
			expectedResetAfter: 7 * 24 * time.Hour,
		},
		{
			name:               "opencode-go flat AI_APICallError carrier",
			stderr:             `timestamp=2026-06-16T20:58:00Z level=ERROR run=r1 message="stream error" providerID=opencode-go modelID=foo session.id=ses_1 small=false agent=build mode=primary error.error="AI_APICallError: Monthly usage limit reached. Resets in 7 days."`,
			model:              "opencode-go/kimi",
			expectedResetAfter: 7 * 24 * time.Hour,
		},
		{
			name:               "opencode-go flat AI_RetryError carrier",
			stderr:             `timestamp=2026-06-16T20:58:00Z level=ERROR run=r1 message="stream error" providerID=opencode-go modelID=foo session.id=ses_1 small=false agent=title mode=primary error.error="AI_RetryError: Failed after 3 attempts. Last error: Monthly usage limit reached. Resets in 7 days."`,
			model:              "opencode-go/kimi",
			expectedResetAfter: 7 * 24 * time.Hour,
		},
		{
			name:               "opencode-go flat UnknownError carrier",
			stderr:             `timestamp=2026-06-16T20:58:00Z level=ERROR run=r1 message="stream error" providerID=opencode-go modelID=foo session.id=ses_1 small=false agent=build mode=primary error.error="UnknownError: Monthly usage limit reached. Resets in 7 days."`,
			model:              "opencode-go/kimi",
			expectedResetAfter: 7 * 24 * time.Hour,
		},
		{
			name:            "zai flat AI_APICallError carrier",
			stderr:          `timestamp=2026-06-16T20:58:00Z level=ERROR run=r1 message="stream error" providerID=zai-coding-plan modelID=glm-5.2 session.id=ses_1 small=false agent=build mode=primary error.error="AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51"`,
			model:           "zai-coding-plan/glm-5.2",
			expectedResetAt: ptrTime(time.Date(2026, 6, 16, 18, 29, 51, 0, time.Local)),
		},
		{
			name:            "zai flat AI_RetryError carrier",
			stderr:          `timestamp=2026-06-16T20:58:00Z level=ERROR run=r1 message="stream error" providerID=zai-coding-plan modelID=glm-5.2 session.id=ses_1 small=false agent=title mode=primary error.error="AI_RetryError: Failed after 3 attempts. Last error: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51"`,
			model:           "zai-coding-plan/glm-5.2",
			expectedResetAt: ptrTime(time.Date(2026, 6, 16, 18, 29, 51, 0, time.Local)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := ParseOpencodeError(tt.stderr, tt.model)
			if ev == nil {
				t.Fatal("expected non-nil evidence")
			}
			if ev.Category != CategoryUsageLimit {
				t.Errorf("category = %q, want %q", ev.Category, CategoryUsageLimit)
			}
			if ev.StatusCode != 429 {
				t.Errorf("statusCode = %d, want 429", ev.StatusCode)
			}
			if tt.expectedResetAfter > 0 && ev.ResetAfter != tt.expectedResetAfter {
				t.Errorf("resetAfter = %v, want %v", ev.ResetAfter, tt.expectedResetAfter)
			}
			if tt.expectedResetAt != nil {
				if ev.ResetAt == nil {
					t.Fatal("expected non-nil ResetAt")
				}
				if !ev.ResetAt.Equal(*tt.expectedResetAt) {
					t.Errorf("resetAt = %v, want %v", ev.ResetAt, *tt.expectedResetAt)
				}
				if ev.ResetAfter != 0 {
					t.Errorf("resetAfter = %v, want 0 (absolute timestamp wins)", ev.ResetAfter)
				}
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestParseOpencodeError_ResetSpanParsing(t *testing.T) {
	tests := []struct {
		name   string
		msg    string
		expect time.Duration
	}{
		{"days span", "Monthly usage limit reached. Resets in 7 days.", 7 * 24 * time.Hour},
		{"hour span", "Usage limit reached for 5 hour.", 5 * time.Hour},
		{"minutes span", "Usage limit reached. Resets in 30 minutes.", 30 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr := `{"type":"error","error":{"name":"UnknownError","data":{"message":"AI_APICallError: ` + tt.msg + `"}}}`
			ev := ParseOpencodeError(stderr, "opencode/opencode-go")
			if ev == nil {
				t.Fatal("expected non-nil evidence")
			}
			if ev.Category != CategoryUsageLimit {
				t.Fatalf("category = %q, want %q", ev.Category, CategoryUsageLimit)
			}
			if ev.ResetAfter != tt.expect {
				t.Errorf("resetAfter = %v, want %v", ev.ResetAfter, tt.expect)
			}
			if ev.ResetAt != nil {
				t.Errorf("resetAt = %v, want nil", ev.ResetAt)
			}
		})
	}
}

func TestParseOpencodeError_AbsoluteResetTimestamp(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
	}{
		{
			name:   "structured wrapper message",
			stderr: `{"type":"error","error":{"name":"UnknownError","data":{"message":"AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51"}}}`,
		},
		{
			name:   "flat server-log wrapper message",
			stderr: `timestamp=2026-06-16T20:58:00Z level=ERROR run=r1 message="stream error" providerID=zai-coding-plan modelID=glm-5.2 session.id=ses_1 small=false agent=build mode=primary error.error="AI_RetryError: Failed after 3 attempts. Last error: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// "reached for 5 hour" also matches the span regex, but the absolute
			// "will reset at ..." timestamp is the authoritative reset and must win.
			ev := ParseOpencodeError(tt.stderr, "zai-coding-plan/glm-5.2")
			if ev == nil {
				t.Fatal("expected non-nil evidence")
			}
			if ev.Category != CategoryUsageLimit {
				t.Fatalf("category = %q, want %q", ev.Category, CategoryUsageLimit)
			}
			if ev.ResetAt == nil {
				t.Fatal("expected non-nil ResetAt")
			}
			want := time.Date(2026, 6, 16, 18, 29, 51, 0, time.Local)
			if !ev.ResetAt.Equal(want) {
				t.Errorf("resetAt = %v, want %v", ev.ResetAt, want)
			}
			if ev.ResetAfter != 0 {
				t.Errorf("resetAfter = %v, want 0 (absolute timestamp wins)", ev.ResetAfter)
			}
		})
	}
}

func TestParseOpencodeError_UsageLimitPriorityOverRateLimit(t *testing.T) {
	ev := ParseOpencodeError(
		`{"type":"error","error":{"name":"RateLimitError","data":{"message":"Usage limit reached for your account. Resets in 24h."}}}`,
		"openai/gpt-5",
	)
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryUsageLimit {
		t.Errorf("category = %q, want %q (usage limit in message should take priority)", ev.Category, CategoryUsageLimit)
	}
}
