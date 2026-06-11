package telemetry

import (
	"strconv"
	"time"
)

// Failure-category string constants mirror the stable taxonomy values produced
// upstream by internal/reliability (improve-error-categorisation). Telemetry
// does not classify failures; it only recognizes these category strings to
// decide which extra tags/contexts to attach. They are duplicated as plain
// strings rather than imported so the telemetry package stays free of a
// reliability dependency (and the relay→telemetry import direction is not
// reversed).
const (
	categoryUsageLimit         = "usage_limit"
	categoryShortRateLimit     = "short_rate_limit"
	categoryProviderOverloaded = "provider_overloaded"
)

// limitCategories are the provider-limit signal categories. For these, reset
// fields are attached as tags (where present) and the bounded raw provider
// signal is attached as the `failure_evidence` context block. Every other
// category attaches neither.
var limitCategories = map[string]struct{}{
	categoryUsageLimit:         {},
	categoryShortRateLimit:     {},
	categoryProviderOverloaded: {},
}

func isLimitCategory(category string) bool {
	_, ok := limitCategories[category]
	return ok
}

// FailureState is the failure-state snapshot a caller supplies when capturing a
// failure. Every field is read straight off upstream FailureEvidence /
// StrategyDecision / resilience state by the caller — telemetry never
// re-classifies. Fields are plain scalars (not the reliability/relay types) to
// keep telemetry dependency-light; callers convert with string(...) at the call
// site.
type FailureState struct {
	// Attempt is the current attempt number (1-based). Zero omits the tag.
	Attempt int

	// MaxAttempts is the retry budget for the run. Zero omits the tag.
	MaxAttempts int

	// Category is the stable FailureCategory value (e.g. "usage_limit").
	Category string

	// AgentState is the resilience vocabulary value for the failing runner:
	// active / probation / frozen / benched.
	AgentState string

	// QuotaScope is the harness-aware bench key from FailureEvidence. Emitted
	// only for limit categories, where present.
	QuotaScope string

	// ResetAt is an absolute reset deadline from FailureEvidence, if known.
	// Emitted (RFC3339) only for limit categories.
	ResetAt *time.Time

	// ResetAfter is a relative reset duration from FailureEvidence, if known.
	// Emitted only for limit categories.
	ResetAfter time.Duration

	// RawSignal is the bounded raw provider limit-response text. Attached only
	// for limit categories, in the failure_evidence context, scrubbed.
	RawSignal string

	// Message is the bounded human-readable failure message. Attached only for
	// limit categories, in the failure_evidence context, scrubbed.
	Message string
}

// FailureStateTags builds the scalar tags for a failure-state snapshot:
// attempt, max_attempts, failure_category, and agent_state always (where set),
// plus quota_scope, reset_at, and reset_after for limit categories where the
// upstream evidence supplies them. Empty/zero values are omitted so filters are
// not polluted with blanks. This never includes raw signal or message text —
// those are free text and go into the failure_evidence context, not tags.
func FailureStateTags(fs FailureState) map[string]string {
	tags := make(map[string]string, 7)
	if fs.Attempt != 0 {
		tags["attempt"] = strconv.Itoa(fs.Attempt)
	}
	if fs.MaxAttempts != 0 {
		tags["max_attempts"] = strconv.Itoa(fs.MaxAttempts)
	}
	if fs.Category != "" {
		tags["failure_category"] = fs.Category
	}
	if fs.AgentState != "" {
		tags["agent_state"] = fs.AgentState
	}
	if isLimitCategory(fs.Category) {
		if fs.QuotaScope != "" {
			tags["quota_scope"] = fs.QuotaScope
		}
		if fs.ResetAt != nil {
			tags["reset_at"] = fs.ResetAt.UTC().Format(time.RFC3339)
		}
		if fs.ResetAfter > 0 {
			tags["reset_after"] = fs.ResetAfter.String()
		}
	}
	return tags
}

// FailureEvidenceContext builds the bounded `failure_evidence` context block for
// provider-limit categories (usage_limit, short_rate_limit, provider_overloaded),
// so the exact raw limit-response shapes accumulate for the harness parser
// normalization pass. It carries only raw_signal and message — never any
// prompt/transcript-looking field — and runs both through the scrubber (home
// path collapse + truncation) so a username embedded in provider text never
// leaves the machine.
//
// It returns nil for any non-limit category, so non-limit failures attach no
// raw-signal context at all. It also returns nil when neither raw_signal nor
// message is present, so an empty block is never attached.
func FailureEvidenceContext(fs FailureState) map[string]interface{} {
	if !isLimitCategory(fs.Category) {
		return nil
	}
	ctx := make(map[string]interface{}, 2)
	if fs.RawSignal != "" {
		ctx["raw_signal"] = truncateValue(collapseHomePaths(fs.RawSignal))
	}
	if fs.Message != "" {
		ctx["message"] = truncateValue(collapseHomePaths(fs.Message))
	}
	if len(ctx) == 0 {
		return nil
	}
	return ctx
}
