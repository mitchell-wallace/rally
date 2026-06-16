package reliability

import "testing"

func TestTryOutcomeSuccessAndFailureCategoryBoundary(t *testing.T) {
	tests := []struct {
		outcome       TryOutcome
		success       bool
		carriesCause  bool
		failureClass  FailureClass
		recovery      bool
		issueEligible bool
	}{
		{OutcomeCompleted, true, false, FailureAgent, false, false},
		{OutcomeHandoffRequested, true, false, FailureAgent, false, false},
		{OutcomeIncomplete, false, false, FailureIncomplete, false, false},
		{OutcomeRunTimeout, false, false, FailureAgent, false, false},
		{OutcomeHandoffTimeout, false, false, FailureAgent, true, false},
		{OutcomeFailed, false, true, FailureInfra, false, true},
		{OutcomeInterrupted, false, false, FailureAgent, false, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			if got := tt.outcome.IsSuccess(); got != tt.success {
				t.Fatalf("IsSuccess() = %v, want %v", got, tt.success)
			}
			if got := tt.outcome.CarriesFailureCategory(); got != tt.carriesCause {
				t.Fatalf("CarriesFailureCategory() = %v, want %v", got, tt.carriesCause)
			}
			if got := tt.outcome.FailureClass(CategoryShortRateLimit); got != tt.failureClass {
				t.Fatalf("FailureClass(short_rate_limit) = %q, want %q", got, tt.failureClass)
			}
			if got := tt.outcome.TriggersRecovery(); got != tt.recovery {
				t.Fatalf("TriggersRecovery() = %v, want %v", got, tt.recovery)
			}
			if got := tt.outcome.ShouldCaptureIssue(); got != tt.issueEligible {
				t.Fatalf("ShouldCaptureIssue() = %v, want %v", got, tt.issueEligible)
			}
		})
	}
}

func TestTryOutcomeTerminalForRun(t *testing.T) {
	tests := []struct {
		name     string
		outcome  TryOutcome
		category FailureCategory
		want     bool
	}{
		{"handoff_timeout", OutcomeHandoffTimeout, "", true},
		{"interrupted", OutcomeInterrupted, "", true},
		{"failed_usage_limit", OutcomeFailed, CategoryUsageLimit, true},
		{"failed_auth", OutcomeFailed, CategoryAuthOrProxy, true},
		{"failed_short_rate_limit", OutcomeFailed, CategoryShortRateLimit, false},
		{"run_timeout_observability_record", OutcomeRunTimeout, "", false},
		{"handoff_requested", OutcomeHandoffRequested, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.outcome.IsTerminalForRun(tt.category); got != tt.want {
				t.Fatalf("IsTerminalForRun(%q) = %v, want %v", tt.category, got, tt.want)
			}
		})
	}
}
