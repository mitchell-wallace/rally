package reliability

import (
	"strings"
	"testing"
	"time"
)

func TestParseAntigravityError(t *testing.T) {
	tests := []struct {
		name               string
		stderr             string
		expectNil          bool
		expectedCategory   FailureCategory
		expectedProvider   string
		expectedResetAfter time.Duration
		expectedStatusCode int
	}{
		{
			name:               "RESOURCE_EXHAUSTED",
			stderr:             "Error: RESOURCE_EXHAUSTED: Quota exceeded",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 0,
		},
		{
			name:               "RESOURCE_EXHAUSTED with Resets in 7d",
			stderr:             "Error: RESOURCE_EXHAUSTED: Individual quota reached. Resets in 7d.",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 7 * 24 * time.Hour,
		},
		{
			name:               "RESOURCE_EXHAUSTED with Resets in 5h30m",
			stderr:             "Error: RESOURCE_EXHAUSTED: Quota exceeded for this API key. Resets in 5h30m.",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 5*time.Hour + 30*time.Minute,
		},
		{
			name:               "Individual quota reached with Resets in 2h",
			stderr:             "Individual quota reached for generativelanguage.googleapis.com. Resets in 2h.",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 2 * time.Hour,
		},
		{
			name:               "Individual quota reached with Resets in 30m",
			stderr:             "Individual quota reached. Resets in 30m.",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 30 * time.Minute,
		},
		{
			name:               "Individual quota reached without duration",
			stderr:             "Individual quota reached for this API key.",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 0,
		},
		{
			name:               "HTTP 429 without RESOURCE_EXHAUSTED",
			stderr:             "HTTP 429: Too Many Requests",
			expectedCategory:   CategoryShortRateLimit,
			expectedProvider:   ProviderGemini,
			expectedStatusCode: 429,
		},
		{
			name:               "RESOURCE_EXHAUSTED with HTTP 429",
			stderr:             "HTTP 429: RESOURCE_EXHAUSTED: Quota exceeded",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedStatusCode: 429,
		},
		{
			name:               "JSON error with RESOURCE_EXHAUSTED",
			stderr:             `{"error":{"code":429,"message":"Individual quota reached. Resets in 7d.","status":"RESOURCE_EXHAUSTED"}}`,
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 7 * 24 * time.Hour,
			expectedStatusCode: 429,
		},
		{
			name:      "empty input",
			stderr:    "",
			expectNil: true,
		},
		{
			name:      "unrelated error",
			stderr:    "something went wrong with the process",
			expectNil: true,
		},
		{
			name:             "Gemini Code Assist unsupported client",
			stderr:           "Error authenticating: IneligibleTierError: This client is no longer supported for Gemini Code Assist for individuals. reasonCode: 'UNSUPPORTED_CLIENT'",
			expectedCategory: CategoryAuthOrProxy,
			expectedProvider: ProviderGemini,
		},
		{
			name:             "case insensitive resource_exhausted",
			stderr:           "resource_exhausted: quota exceeded",
			expectedCategory: CategoryUsageLimit,
			expectedProvider: ProviderGemini,
		},
		{
			name:             "case insensitive individual quota",
			stderr:           "INDIVIDUAL QUOTA REACHED for this key",
			expectedCategory: CategoryUsageLimit,
			expectedProvider: ProviderGemini,
		},
		{
			name:               "Resets in 1.5d",
			stderr:             "RESOURCE_EXHAUSTED: Quota exceeded. Resets in 1.5d.",
			expectedCategory:   CategoryUsageLimit,
			expectedProvider:   ProviderGemini,
			expectedResetAfter: 36 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := ParseAntigravityError(tt.stderr)
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
			if tt.expectedResetAfter != 0 && ev.ResetAfter != tt.expectedResetAfter {
				t.Errorf("resetAfter = %v, want %v", ev.ResetAfter, tt.expectedResetAfter)
			}
			if tt.expectedStatusCode != 0 && ev.StatusCode != tt.expectedStatusCode {
				t.Errorf("statusCode = %d, want %d", ev.StatusCode, tt.expectedStatusCode)
			}
		})
	}
}

func TestParseAntigravityError_PopulatesFields(t *testing.T) {
	ev := ParseAntigravityError("Error: RESOURCE_EXHAUSTED: Individual quota reached. Resets in 7d.")
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

func TestParseAntigravityError_CurrentAuthShapes(t *testing.T) {
	capturedStdout := `Authentication required. Please visit the URL to log in:
  https://accounts.google.com/o/oauth2/auth?access_type=offline&client_id=1071006060591-...

Waiting for authentication (timeout 30s)...
Or, paste the authorization code here and press Enter:
Error: authentication timed out.`

	capturedGlog := `E0705 04:50:45.506231 93404 log.go:398] Failed to poll ListExperiments: error getting token source: You are not logged into Antigravity.
W0705 04:50:45.507736 93404 log_context.go:117] Cache(loadCodeAssistResponse): Singleflight refresh failed: error getting token source: You are not logged into Antigravity.
E0705 04:50:45.507827 93404 log.go:398] error getting token source: You are not logged into Antigravity.
I0705 04:50:45.529891 93404 server.go:2404] Auth succeeded, refreshing features and managers`

	tests := []struct {
		name          string
		text          string
		wantRawSignal string
	}{
		{
			name:          "stdout prompt",
			text:          capturedStdout,
			wantRawSignal: "Authentication required. Please visit the URL to log in",
		},
		{
			name:          "stdout timeout",
			text:          "prefix\nError: authentication timed out.",
			wantRawSignal: "Error: authentication timed out.",
		},
		{
			name:          "glog token source",
			text:          capturedGlog,
			wantRawSignal: "error getting token source",
		},
		{
			name:          "glog not logged in",
			text:          "E0705 04:50:45.507827 93404 log.go:398] You are not logged into Antigravity.",
			wantRawSignal: "You are not logged into Antigravity.",
		},
		{
			name: "auth succeeded hazard still uses error shape",
			text: `I0705 04:50:45.529891 93404 server.go:2404] Auth succeeded, refreshing features and managers
E0705 04:50:45.507827 93404 log.go:398] error getting token source: You are not logged into Antigravity.`,
			wantRawSignal: "error getting token source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := ParseAntigravityError(tt.text)
			if ev == nil {
				t.Fatal("expected auth evidence, got nil")
			}
			if ev.Category != CategoryAuthOrProxy {
				t.Fatalf("Category = %q, want %q", ev.Category, CategoryAuthOrProxy)
			}
			if ev.Provider != ProviderGemini {
				t.Fatalf("Provider = %q, want %q", ev.Provider, ProviderGemini)
			}
			if ev.Harness != "antigravity" {
				t.Fatalf("Harness = %q, want antigravity", ev.Harness)
			}
			if ev.Message != antigravityAuthMessage {
				t.Fatalf("Message = %q, want %q", ev.Message, antigravityAuthMessage)
			}
			if !strings.Contains(ev.RawSignal, tt.wantRawSignal) {
				t.Fatalf("RawSignal = %q, want substring %q", ev.RawSignal, tt.wantRawSignal)
			}
		})
	}
}

func TestParseAntigravityError_CurrentAuthNegative(t *testing.T) {
	if ev := ParseAntigravityError("Implemented the requested change and all checks passed."); ev != nil {
		t.Fatalf("ordinary completion text matched auth evidence: %+v", ev)
	}
}
