package reliability

import "time"

// FailureCategory is a stable taxonomy of failure causes. Each category maps
// to exactly one FailureClass (via CategoryToClass) and carries a short
// display label (via CategoryDisplayLabel). The nine categories come from
// the improve-error-categorisation design Decision 2.
type FailureCategory string

const (
	// CategoryUsageLimit indicates a long-duration usage/quota exhaustion
	// (e.g. Anthropic daily cap, Gemini RESOURCE_EXHAUSTED). Benches the
	// quota scope until reset; routes away; one attempt then bench.
	CategoryUsageLimit FailureCategory = "usage_limit"

	// CategoryShortRateLimit indicates a short, transient rate limit
	// (e.g. 429 with a Retry-After of a few minutes). Waits the parsed
	// duration, then resumes within the retry budget.
	CategoryShortRateLimit FailureCategory = "short_rate_limit"

	// CategoryProviderOverloaded indicates a provider overload signal
	// (HTTP 529, overloaded_error, 503 "Overloaded"). Resumes within
	// the retry budget.
	CategoryProviderOverloaded FailureCategory = "provider_overloaded"

	// CategoryTransientInfra covers API timeouts, network/connection/TLS
	// failures, and non-overload 5xx errors. Resumes within the retry budget.
	CategoryTransientInfra FailureCategory = "transient_infra"

	// CategoryInvalidModel indicates the requested model does not exist or
	// is not available. Exhausts the entry and routes away.
	CategoryInvalidModel FailureCategory = "invalid_model"

	// CategoryAuthOrProxy indicates an authentication or proxy configuration
	// error. Routes away; one attempt then route.
	CategoryAuthOrProxy FailureCategory = "auth_or_proxy"

	// CategoryHarnessLaunch indicates a process-level launch failure
	// (fork/exec, not found, exec format error). Fresh restart or rotate.
	CategoryHarnessLaunch FailureCategory = "harness_launch"

	// CategoryIncompleteFinalization indicates the agent produced file
	// changes but did not finalize the lap. Resume + retry with guidance.
	CategoryIncompleteFinalization FailureCategory = "incomplete_finalization"

	// CategoryAgentError is the default for unrecognised errors.
	// Existing retry/fresh restart behavior.
	CategoryAgentError FailureCategory = "agent_error"
)

// AllCategories is the complete, ordered list of FailureCategory values.
// Used by tests to assert exhaustive coverage.
var AllCategories = []FailureCategory{
	CategoryUsageLimit,
	CategoryShortRateLimit,
	CategoryProviderOverloaded,
	CategoryTransientInfra,
	CategoryInvalidModel,
	CategoryAuthOrProxy,
	CategoryHarnessLaunch,
	CategoryIncompleteFinalization,
	CategoryAgentError,
}

// categoryDisplayLabels maps each FailureCategory to a short, human-readable
// display label. Labels carry no harness name unless the category is
// intentionally harness-specific (none currently are).
var categoryDisplayLabels = map[FailureCategory]string{
	CategoryUsageLimit:             "usage limit",
	CategoryShortRateLimit:         "rate limit",
	CategoryProviderOverloaded:     "provider overloaded",
	CategoryTransientInfra:         "infra error",
	CategoryInvalidModel:           "invalid model",
	CategoryAuthOrProxy:            "auth/proxy error",
	CategoryHarnessLaunch:          "harness launch error",
	CategoryIncompleteFinalization: "incomplete: file changes without finalization",
	CategoryAgentError:             "agent error",
}

// CategoryDisplayLabel returns the short display label for a category.
// Returns the category string itself if the category is unknown.
func CategoryDisplayLabel(c FailureCategory) string {
	if label, ok := categoryDisplayLabels[c]; ok {
		return label
	}
	return string(c)
}

// categoryToClass maps each FailureCategory to its FailureClass.
//
// This mapping is load-bearing (design Decision 3): it is the single source
// of truth for what feeds the freeze counter. Categories mapped to
// FailureInfra increment infraFailures; categories mapped to FailureAgent
// do NOT. Specifically:
//   - usage_limit, invalid_model, auth_or_proxy → FailureAgent (NOT infra)
//   - short_rate_limit, provider_overloaded, transient_infra, harness_launch → FailureInfra
//   - incomplete_finalization → FailureIncomplete
//   - agent_error → FailureAgent
var categoryToClass = map[FailureCategory]FailureClass{
	CategoryUsageLimit:             FailureAgent,
	CategoryShortRateLimit:         FailureInfra,
	CategoryProviderOverloaded:     FailureInfra,
	CategoryTransientInfra:         FailureInfra,
	CategoryInvalidModel:           FailureAgent,
	CategoryAuthOrProxy:            FailureAgent,
	CategoryHarnessLaunch:          FailureInfra,
	CategoryIncompleteFinalization: FailureIncomplete,
	CategoryAgentError:             FailureAgent,
}

// CategoryToClass returns the FailureClass for a given FailureCategory.
// Unknown categories default to FailureAgent (the does-not-freeze side).
func CategoryToClass(c FailureCategory) FailureClass {
	if class, ok := categoryToClass[c]; ok {
		return class
	}
	return FailureAgent
}

// FailureEvidence carries structured information about a failure, populated
// by executors where they can observe structured error output, or by the
// runner-side fallback parser from log tails. Shape follows design Decision 1.
type FailureEvidence struct {
	// Category is the resolved FailureCategory for this evidence.
	Category FailureCategory

	// Harness is the harness that produced the failure (e.g. "claude", "codex").
	Harness string

	// Provider is the provider/account segment where known (e.g. "anthropic",
	// "openai", the segment before '/' in opencode model names).
	Provider string

	// QuotaScope is the harness-aware bench key from the QuotaScope resolver.
	QuotaScope string

	// Message is a human-readable, bounded description of the failure.
	Message string

	// StatusCode is the HTTP status code if the failure was an HTTP error.
	StatusCode int

	// ResetAfter is the parsed "resets in …" duration for quota exhaustion.
	ResetAfter time.Duration

	// ResetAt is an absolute reset time, if available.
	ResetAt *time.Time

	// RetryAfter is the parsed Retry-After duration for rate limits.
	RetryAfter time.Duration

	// RawSignal is a bounded raw match string for debugging/triage.
	RawSignal string
}
