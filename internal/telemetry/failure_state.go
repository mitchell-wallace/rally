package telemetry

import (
	"strconv"
	"strings"
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

	// Outcome is the upstream TryOutcome lifecycle value. Empty omits the tag.
	Outcome string

	// Category is the stable FailureCategory value (e.g. "usage_limit"). It is
	// emitted only for Outcome == "failed"; lifecycle outcomes are not failure
	// taxonomy values.
	Category string

	// RecoveryClassification is the RECOVERY role classification recorded
	// upstream. Empty omits the tag.
	RecoveryClassification string

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

	// EvidenceMessage is bounded non-limit failure evidence supplied by the
	// caller. Unlike Message, it may be attached for ordinary non-limit
	// categories when emitted on non-alerting try/span telemetry.
	EvidenceMessage string

	// EvidenceRawSignal is bounded non-limit raw evidence supplied by the
	// caller. Callers must pass only scrub-safe excerpts; this layer still
	// strips transcript-shaped sections and applies normal scrub/truncation.
	EvidenceRawSignal string

	// EvidenceSource describes where the evidence came from, e.g.
	// "executor_evidence" or "safe_exec_error".
	EvidenceSource string
}

// FailureStateTags builds the scalar tags for a failure-state snapshot:
// attempt, max_attempts, outcome, failure_category for failed outcomes,
// recovery_classification, and agent_state where set, plus quota_scope, reset_at,
// and reset_after for failed limit categories where the upstream evidence
// supplies them. Empty/zero values are omitted so filters are not polluted with
// blanks. This never includes raw signal or message text — those are free text
// and go into the failure_evidence context, not tags.
func FailureStateTags(fs FailureState) map[string]string {
	tags := make(map[string]string, 9)
	if fs.Attempt != 0 {
		tags["attempt"] = strconv.Itoa(fs.Attempt)
	}
	if fs.MaxAttempts != 0 {
		tags["max_attempts"] = strconv.Itoa(fs.MaxAttempts)
	}
	if fs.Outcome != "" {
		tags["outcome"] = fs.Outcome
	}
	if fs.Outcome == "failed" && fs.Category != "" {
		tags["failure_category"] = fs.Category
	}
	if fs.RecoveryClassification != "" {
		tags["recovery_classification"] = fs.RecoveryClassification
	}
	if fs.AgentState != "" {
		tags["agent_state"] = fs.AgentState
	}
	if fs.Outcome == "failed" && isLimitCategory(fs.Category) {
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

// FailureEvidenceContext builds the bounded `failure_evidence` context block.
// Limit categories use RawSignal/Message from provider evidence. Non-limit
// categories only attach explicit Evidence* fields supplied by the caller, so
// ordinary failures do not accidentally ship transcript/log payloads. Values run
// through home-path collapse, transcript-section stripping, and truncation.
func FailureEvidenceContext(fs FailureState) map[string]interface{} {
	rawSignal := fs.RawSignal
	message := fs.Message
	if !isLimitCategory(fs.Category) {
		rawSignal = fs.EvidenceRawSignal
		message = fs.EvidenceMessage
	}
	if !isLimitCategory(fs.Category) && rawSignal == "" && message == "" && fs.EvidenceSource == "" {
		return nil
	}
	ctx := make(map[string]interface{}, 5)
	if rawSignal != "" {
		ctx["raw_signal"] = sanitizeEvidenceValue(rawSignal)
	}
	if message != "" {
		ctx["message"] = sanitizeEvidenceValue(message)
	}
	if fs.EvidenceSource != "" {
		ctx["source"] = truncateValue(collapseHomePaths(fs.EvidenceSource))
	}
	shape := evidenceShape(rawSignal, message)
	if shape != "" {
		ctx["evidence_shape"] = shape
	}
	if providerSignal := providerSignal(rawSignal, message); providerSignal != "" {
		ctx["provider_signal"] = sanitizeEvidenceValue(providerSignal)
	}
	if len(ctx) == 0 {
		return nil
	}
	return ctx
}

// FailureEvidenceFields returns the failure_evidence context flattened with a
// failure_evidence. prefix, for sinks/events that do not support nested
// contexts. It intentionally reuses FailureEvidenceContext so filtering,
// scrubbing, shape detection, and key policy stay in one place.
func FailureEvidenceFields(fs FailureState) map[string]interface{} {
	ctx := FailureEvidenceContext(fs)
	if len(ctx) == 0 {
		return nil
	}
	fields := make(map[string]interface{}, len(ctx))
	for k, v := range ctx {
		fields["failure_evidence."+k] = v
	}
	return fields
}

func sanitizeEvidenceValue(value string) string {
	value = stripTranscriptSections(value)
	return truncateValue(collapseHomePaths(value))
}

func stripTranscriptSections(value string) string {
	lower := strings.ToLower(value)
	cut := len(value)
	for _, marker := range []string{"\noutput:", "\nstderr:", " output:", " stderr:"} {
		if idx := strings.Index(lower, marker); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	return strings.TrimSpace(value[:cut])
}

func evidenceShape(rawSignal, message string) string {
	rawSignal = strings.TrimSpace(rawSignal)
	message = strings.TrimSpace(message)
	if rawSignal == "" && message == "" {
		return ""
	}
	combined := firstNonEmpty(message, rawSignal)
	lower := strings.ToLower(combined)
	if isTranscriptTailEvidence(lower) {
		return "transcript_tail"
	}
	for _, value := range []string{message, rawSignal} {
		if isProviderObjectEvidence(value) {
			return "provider_object"
		}
	}
	return "plain_text"
}

func isTranscriptTailEvidence(lower string) bool {
	return strings.Contains(lower, "reading additional input from stdin") ||
		strings.Contains(lower, `"item.completed"`) ||
		strings.Contains(lower, `"command_execution"`) ||
		strings.Contains(lower, "transcript=") ||
		strings.Contains(lower, "\noutput:") ||
		strings.Contains(lower, "\nstderr:")
}

func isProviderObjectEvidence(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if isTranscriptTailEvidence(lower) {
		return false
	}
	return strings.Contains(lower, `"type":"error"`) ||
		strings.Contains(lower, `"error"`) ||
		strings.Contains(lower, "ai_apicallerror") ||
		strings.Contains(lower, "ai_retryerror")
}

func providerSignal(rawSignal, message string) string {
	for _, value := range []string{message, rawSignal} {
		if isProviderObjectEvidence(value) {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
