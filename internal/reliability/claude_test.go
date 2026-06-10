package reliability

import (
	"testing"
	"time"
)

func TestParseClaudeError(t *testing.T) {
	tests := []struct {
		name               string
		stderr             string
		expectNil          bool
		expectedCategory   FailureCategory
		expectedProvider   string
		expectedResetAfter time.Duration
		expectedRetryAfter time.Duration
		expectedStatusCode int
	}{
		{
			name:               "rate_limit_event five-hour",
			stderr:             `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the five-hour rate limit for your organization."}}`,
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   ProviderAnthropic,
			expectedRetryAfter: 5 * time.Hour,
			expectedStatusCode: 429,
		},
		{
			name:               "rate_limit_event seven-day",
			stderr:             `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the seven-day rate limit for your organization."}}`,
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderAnthropic,
			expectedResetAfter: 7 * 24 * time.Hour,
			expectedStatusCode: 429,
		},
		{
			name:               "rate_limit_event five hour no hyphen",
			stderr:             "error: rate limit: five hour cap exceeded",
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   ProviderAnthropic,
			expectedRetryAfter: 5 * time.Hour,
			expectedStatusCode: 429,
		},
		{
			name:               "rate_limit_event seven day no hyphen",
			stderr:             "error: rate limit: seven day cap exceeded",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderAnthropic,
			expectedResetAfter: 7 * 24 * time.Hour,
			expectedStatusCode: 429,
		},
		{
			name:               "rate_limit generic without window",
			stderr:             "error: rate_limit_error: too many requests",
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   ProviderAnthropic,
			expectedRetryAfter: 60 * time.Second,
			expectedStatusCode: 429,
		},
		{
			name:               "model_not_found",
			stderr:             `{"type":"error","error":{"type":"not_found_error","message":"model_not_found: The model 'claude-foo' does not exist."}}`,
			expectedCategory:   CategoryInvalidModel,
			expectedProvider:   ProviderAnthropic,
			expectedStatusCode: 404,
		},
		{
			name:               "model not found case insensitive",
			stderr:             "Model Not Found: requested model is unavailable",
			expectedCategory:   CategoryInvalidModel,
			expectedProvider:   ProviderAnthropic,
			expectedStatusCode: 404,
		},
		{
			name:               "authentication_failed",
			stderr:             `{"type":"error","error":{"type":"authentication_error","message":"authentication_failed: invalid x-api-key"}}`,
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   ProviderAnthropic,
			expectedStatusCode: 401,
		},
		{
			name:               "authentication failed case insensitive",
			stderr:             "Authentication Failed: permission denied",
			expectedCategory:   CategoryAuthOrProxy,
			expectedProvider:   ProviderAnthropic,
			expectedStatusCode: 401,
		},
		{
			name:               "529 overload",
			stderr:             "HTTP 529: Overloaded",
			expectedCategory:   CategoryProviderOverloaded,
			expectedProvider:   ProviderAnthropic,
			expectedStatusCode: 529,
			expectedRetryAfter: 30 * time.Second,
		},
		{
			name:               "overloaded_error",
			stderr:             `{"type":"error","error":{"type":"api_error","message":"Overloaded"}}`,
			expectedCategory:   CategoryProviderOverloaded,
			expectedProvider:   ProviderAnthropic,
			expectedStatusCode: 529,
			expectedRetryAfter: 30 * time.Second,
		},
		{
			name:               "HTTP 529 without overload word",
			stderr:             "Received HTTP 529 from upstream",
			expectedCategory:   CategoryProviderOverloaded,
			expectedProvider:   ProviderAnthropic,
			expectedStatusCode: 529,
			expectedRetryAfter: 30 * time.Second,
		},
		{
			name:      "empty input",
			stderr:    "",
			expectNil: true,
		},
		{
			name:      "unrelated error",
			stderr:    "some generic error from claude",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := ParseClaudeError(tt.stderr)
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
			if tt.expectedRetryAfter != 0 && ev.RetryAfter != tt.expectedRetryAfter {
				t.Errorf("retryAfter = %v, want %v", ev.RetryAfter, tt.expectedRetryAfter)
			}
			if tt.expectedResetAfter != 0 && ev.ResetAfter != tt.expectedResetAfter {
				t.Errorf("resetAfter = %v, want %v", ev.ResetAfter, tt.expectedResetAfter)
			}
			if tt.expectedStatusCode != 0 && ev.StatusCode != tt.expectedStatusCode {
				t.Errorf("statusCode = %d, want %d", ev.StatusCode, tt.expectedStatusCode)
			}
		})
	}
}

func TestParseClaudeError_PopulatesFields(t *testing.T) {
	ev := ParseClaudeError(`{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the five-hour rate limit for your organization."}}`)
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

func TestParseClaudeError_PriorityModelNotFoundOverRateLimit(t *testing.T) {
	ev := ParseClaudeError("model_not_found: rate limit was not the issue")
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryInvalidModel {
		t.Errorf("category = %q, want %q (model_not_found should take priority)", ev.Category, CategoryInvalidModel)
	}
}

func TestParseClaudeError_PriorityAuthOverRateLimit(t *testing.T) {
	ev := ParseClaudeError("authentication_failed: rate limit endpoint unreachable")
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryAuthOrProxy {
		t.Errorf("category = %q, want %q (auth should take priority)", ev.Category, CategoryAuthOrProxy)
	}
}
