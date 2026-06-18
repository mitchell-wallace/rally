package reliability

// TryOutcome is the lifecycle result of one agent try. It is orthogonal to
// FailureCategory: only OutcomeFailed carries a failure-cause category.
type TryOutcome string

const (
	OutcomeCompleted        TryOutcome = "completed"
	OutcomeHandoffRequested TryOutcome = "handoff_requested"
	OutcomeIncomplete       TryOutcome = "incomplete"
	OutcomeRunTimeout       TryOutcome = "run_timeout"
	OutcomeHandoffTimeout   TryOutcome = "handoff_timeout"
	OutcomeFailed           TryOutcome = "failed"
	OutcomeInterrupted      TryOutcome = "interrupted"
	OutcomeCancelled        TryOutcome = "cancelled"
)

// IsSuccess reports outcomes that successfully finalized the current try's
// lifecycle, including successful handoffs that leave the lap for follow-up.
func (o TryOutcome) IsSuccess() bool {
	return o == OutcomeCompleted || o == OutcomeHandoffRequested
}

// CarriesFailureCategory reports whether the outcome may be paired with a
// FailureCategory cause.
func (o TryOutcome) CarriesFailureCategory() bool {
	return o == OutcomeFailed
}

// FailureClass returns the resilience class for this lifecycle outcome.
// Non-failed lifecycle outcomes never map to infra, so they cannot feed the
// pause/freeze cascade by themselves.
func (o TryOutcome) FailureClass(category FailureCategory) FailureClass {
	switch o {
	case OutcomeFailed:
		return CategoryToClass(category)
	case OutcomeIncomplete:
		return FailureIncomplete
	default:
		return FailureAgent
	}
}

// IsTerminalForRun reports whether the attempt loop should stop after this
// try. For failed outcomes, the existing terminal failure categories remain the
// source of truth.
func (o TryOutcome) IsTerminalForRun(category FailureCategory) bool {
	switch o {
	case OutcomeHandoffTimeout, OutcomeInterrupted, OutcomeCancelled:
		return true
	case OutcomeFailed:
		return category == CategoryUsageLimit || category == CategoryAuthOrProxy
	default:
		return false
	}
}

// ShouldCaptureIssue reports whether this outcome is eligible to become an
// operator Issue. Non-failed lifecycle outcomes stay span/log-only.
func (o TryOutcome) ShouldCaptureIssue() bool {
	return o == OutcomeFailed
}

// TriggersRecovery reports whether the outcome alone should route a later run
// to RECOVERY. Dirty handoffs are a separate persisted predicate.
func (o TryOutcome) TriggersRecovery() bool {
	return o == OutcomeHandoffTimeout
}
