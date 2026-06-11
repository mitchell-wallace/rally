package reliability

import (
	"testing"
	"time"
)

func TestParseGeminiError(t *testing.T) {
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
			ev := ParseGeminiError(tt.stderr)
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

func TestParseGeminiError_PopulatesFields(t *testing.T) {
	ev := ParseGeminiError("Error: RESOURCE_EXHAUSTED: Individual quota reached. Resets in 7d.")
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
