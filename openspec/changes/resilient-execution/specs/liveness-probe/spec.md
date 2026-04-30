## ADDED Requirements

### Requirement: Opt-in liveness probe with per-adapter capability gate
The system SHALL provide an opt-in liveness probe that sends a side-channel "respond with OK" prompt to a running session when the freeze signal is ambiguous. The probe is gated by `[reliability].liveness_probe = true` (default `false`) AND the active adapter declaring `LivenessProbeSupported() bool == true`. When either condition is false, the probe SHALL be skipped silently.

#### Scenario: Probe disabled by config
- **WHEN** `[reliability].liveness_probe = false` (or unset)
- **THEN** the probe SHALL never run, regardless of adapter capability

#### Scenario: Probe enabled and adapter supports
- **WHEN** `[reliability].liveness_probe = true` AND the active adapter declares `LivenessProbeSupported() == true`
- **THEN** rally MAY invoke `ProbeLiveness(ctx) (bool, error)` on the adapter when the freeze heuristic flags ambiguity

#### Scenario: Probe enabled but adapter does not support
- **WHEN** `[reliability].liveness_probe = true` AND the active adapter declares `LivenessProbeSupported() == false`
- **THEN** the probe SHALL be skipped silently (no error, no warning); freeze detection falls back to passive monitoring alone

### Requirement: Probe outcome drives retry decision
The system SHALL interpret a successful probe (the agent responded with the expected OK marker within the timeout) as evidence of liveness; the freeze flag SHALL NOT trip in that case. A failed or timed-out probe SHALL be treated as confirmation that the try is unresponsive; the freeze handler SHALL proceed with graceful-kill and retry.

#### Scenario: Probe succeeds
- **WHEN** `ProbeLiveness` returns `(true, nil)` within the configured timeout
- **THEN** the freeze flag SHALL be cleared for the current evaluation; the try continues running

#### Scenario: Probe fails or times out
- **WHEN** `ProbeLiveness` returns `(false, _)` or exceeds its timeout
- **THEN** the relay-runner SHALL graceful-kill the try and proceed via the resume-aware retry path
