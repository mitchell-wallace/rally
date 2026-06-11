package relay

import "time"

// Resilience constants control the per-agent-type circuit breaker timing
// (pause/freeze durations and retry counts). They are intentionally hardcoded
// rather than configurable; see the harden-relay-run-lifecycle change design.
const (
	// FreezeDuration is how long a frozen agent stays frozen before getState
	// decays it to StateProbation for a one-shot recovery attempt.
	FreezeDuration = 5 * time.Hour

	// PauseDuration is the cool-off after a single relay pauses an agent.
	// After this window an hourly retry is eligible.
	PauseDuration = time.Hour

	// HourlyRetriesBeforeFreeze is the number of consecutive failed hourly
	// retries that escalates a paused agent to frozen.
	HourlyRetriesBeforeFreeze = 5

	// HourlyRetryMaxAttempts is the per-try retry budget granted to hourly
	// retries and probation runs.
	HourlyRetryMaxAttempts = 3

	// BenchDefaultDuration is the fallback bench window applied on a
	// usage_limit that carries no parsed reset deadline. Five hours matches
	// the most common short usage window (Anthropic's rolling five-hour cap);
	// longer caps that didn't parse a reset are caught by the re-probe-once
	// semantics — if the probe hits the limit again the scope is simply
	// re-benched for another window.
	BenchDefaultDuration = 5 * time.Hour
)
