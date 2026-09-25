package reliability

import (
	"fmt"
	"os"
	"path/filepath"
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
			// The five-hour window is quota exhaustion, not a short rate
			// limit: it benches the quota scope (default 5h) instead of
			// sleeping inline in the attempt loop.
			name:               "rate_limit_event five-hour",
			stderr:             `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the five-hour rate limit for your organization."}}`,
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderAnthropic,
			expectedResetAfter: 5 * time.Hour,
			expectedStatusCode: 429,
		},
		{
			name:               "five-hour with parsed reset span",
			stderr:             "Five-hour limit reached · resets in 3h 24m",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderAnthropic,
			expectedResetAfter: 3*time.Hour + 24*time.Minute,
			expectedStatusCode: 429,
		},
		{
			name:               "rate_limit with reset span but no named window",
			stderr:             "rate_limit: usage cap hit, resets in 2h 5m",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderAnthropic,
			expectedResetAfter: 2*time.Hour + 5*time.Minute,
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
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderAnthropic,
			expectedResetAfter: 5 * time.Hour,
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

func TestParseClaudeError_ClockReset(t *testing.T) {
	ev := ParseClaudeError("rate_limit: usage limit reached · resets at 14:30")
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryUsageLimit {
		t.Errorf("category = %q, want %q", ev.Category, CategoryUsageLimit)
	}
	if ev.ResetAt == nil {
		t.Fatal("expected ResetAt to be populated from clock time")
	}
	if got := ev.ResetAt.Minute(); got != 30 {
		t.Errorf("ResetAt minute = %d, want 30", got)
	}
	if got := ev.ResetAt.Hour(); got != 14 {
		t.Errorf("ResetAt hour = %d, want 14", got)
	}
	if !ev.ResetAt.After(time.Now()) {
		t.Error("ResetAt should be the next occurrence of the clock time")
	}
}

func TestParseClaudeError_ClockResetPM(t *testing.T) {
	ev := ParseClaudeError("five-hour limit · resets at 2:30 PM")
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryUsageLimit {
		t.Errorf("category = %q, want %q", ev.Category, CategoryUsageLimit)
	}
	if ev.ResetAt == nil {
		t.Fatal("expected ResetAt to be populated")
	}
	if got := ev.ResetAt.Hour(); got != 14 {
		t.Errorf("ResetAt hour = %d, want 14 (2 PM)", got)
	}
}

func TestParseClaudeError_PlainRateLimitStaysShort(t *testing.T) {
	ev := ParseClaudeError("error: rate_limit_error: too many requests")
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryShortRateLimit {
		t.Errorf("category = %q, want %q (no reset timing, no named window)", ev.Category, CategoryShortRateLimit)
	}
	if ev.RetryAfter != 60*time.Second {
		t.Errorf("retryAfter = %v, want %v", ev.RetryAfter, 60*time.Second)
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

// TestParseClaudeError_CutStreamWithAllowedRateLimitEvents reproduces the
// 0041 evidence shape: every rate_limit_event reports status=ALLOWED (one
// naming a seven_day window), and the stream ends mid-response with no result
// record and no error record. The verdict must be retryable transient, not a
// usage_limit pause, and must carry no seven-day reset.
func TestParseClaudeError_CutStreamWithAllowedRateLimitEvents(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "claude-cut-stream-allowed-events.log"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ev := ParseClaudeError(string(raw))
	if ev == nil {
		t.Fatal("expected non-nil evidence for cut stream")
	}
	if ev.Category != CategoryTransientInfra {
		t.Errorf("category = %q, want %q (a cut stream is retryable, not a cap)", ev.Category, CategoryTransientInfra)
	}
	if ev.Category == CategoryUsageLimit {
		t.Fatal("cut stream with only ALLOWED rate_limit_events must not classify usage_limit")
	}
	if ev.ResetAfter == 7*24*time.Hour {
		t.Error("resetAfter = 168h, want no canned seven-day reset")
	}
	if ev.ResetAfter != 0 {
		t.Errorf("resetAfter = %v, want 0", ev.ResetAfter)
	}
	if ev.ResetAt != nil {
		t.Errorf("resetAt = %v, want nil", ev.ResetAt)
	}
}

// TestParseClaudeError_BlockedRateLimitEventCarriesReset covers a genuine
// limit: a rate_limit_event whose status is not ALLOWED and whose reset is
// 2.5h away. The verdict is usage_limit with a reset of at most 2.5h — never
// the canned 168h.
func TestParseClaudeError_BlockedRateLimitEventCarriesReset(t *testing.T) {
	resetsAt := time.Now().Add(150 * time.Minute).Truncate(time.Second).Unix()
	stderr := fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":%d,"rateLimitType":"five_hour"}}`+"\n"+
		`{"type":"rate_limit_event","rate_limit_info":{"status":"blocked","resetsAt":%d,"rateLimitType":"five_hour"}}`+"\n", resetsAt-3600, resetsAt)
	ev := ParseClaudeError(stderr)
	if ev == nil {
		t.Fatal("expected non-nil evidence for blocked rate_limit_event")
	}
	if ev.Category != CategoryUsageLimit {
		t.Fatalf("category = %q, want %q", ev.Category, CategoryUsageLimit)
	}
	if ev.ResetAfter == 7*24*time.Hour {
		t.Error("resetAfter = 168h, want reset from the record")
	}
	if ev.ResetAfter != 0 {
		t.Errorf("resetAfter = %v, want 0 (record reset is absolute)", ev.ResetAfter)
	}
	if ev.ResetAt == nil {
		t.Fatal("expected ResetAt from the record's resetsAt")
	}
	remaining := time.Until(*ev.ResetAt)
	if remaining <= 0 || remaining > 150*time.Minute {
		t.Errorf("reset = %v from now, want within (0, 2.5h]", remaining)
	}
}

// TestParseClaudeError_BlockedEventWithoutResetKeepsWindowDefault is the
// negative control: a genuine limit record that carries no parsable reset
// keeps the existing canned window default.
func TestParseClaudeError_BlockedEventWithoutResetKeepsWindowDefault(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   time.Duration
	}{
		{"seven_day", "seven_day", 7 * 24 * time.Hour},
		{"five_hour", "five_hour", 5 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stderr := fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"blocked","rateLimitType":%q}}`+"\n", tt.window)
			ev := ParseClaudeError(stderr)
			if ev == nil {
				t.Fatal("expected non-nil evidence")
			}
			if ev.Category != CategoryUsageLimit {
				t.Fatalf("category = %q, want %q", ev.Category, CategoryUsageLimit)
			}
			if ev.ResetAfter != tt.want {
				t.Errorf("resetAfter = %v, want default %v", ev.ResetAfter, tt.want)
			}
			if ev.ResetAt != nil {
				t.Errorf("resetAt = %v, want nil", ev.ResetAt)
			}
		})
	}
}

// TestParseClaudeError_LimitTextCappedAtEventRecordReset covers the pause cap
// when the CLI's own limit message carries no parsed reset timing but the
// stream's rate-limit records do: the pause comes from the latest record's
// reset (40m away), not the five-hour canned default.
func TestParseClaudeError_LimitTextCappedAtEventRecordReset(t *testing.T) {
	resetsAt := time.Now().Add(40 * time.Minute).Truncate(time.Second).Unix()
	stderr := fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":%d,"rateLimitType":"five_hour"}}`+"\n"+
		"error: rate limit: five hour cap exceeded\n", resetsAt)
	ev := ParseClaudeError(stderr)
	if ev == nil {
		t.Fatal("expected non-nil evidence")
	}
	if ev.Category != CategoryUsageLimit {
		t.Fatalf("category = %q, want %q", ev.Category, CategoryUsageLimit)
	}
	if ev.ResetAfter == 5*time.Hour {
		t.Error("resetAfter = 5h canned default, want cap at the record's reset")
	}
	if ev.ResetAt == nil {
		t.Fatal("expected ResetAt from the latest record's resetsAt")
	}
	remaining := time.Until(*ev.ResetAt)
	if remaining <= 0 || remaining > 40*time.Minute+5*time.Second {
		t.Errorf("reset = %v from now, want within (0, 40m]", remaining)
	}
}
