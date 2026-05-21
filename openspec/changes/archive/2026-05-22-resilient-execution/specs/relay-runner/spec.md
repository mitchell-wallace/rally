## MODIFIED Requirements

### Requirement: Retry logic
The system SHALL retry failed tries up to `[reliability].retry_budget` times within a single run (default 5; configurable). Retries do NOT count against the relay's iteration count. The retry strategy for each failure SHALL be selected per the `error-classification` capability table; the resume-aware retry path (per `resume-retries`) SHALL be used when the active adapter supports resume.

#### Scenario: Retry with previous summary
- **WHEN** a try fails and retries remain
- **THEN** the system SHALL pass the previous try's summary as `PreviousSummary` in the next attempt's RunOptions

#### Scenario: Default retry budget is 5
- **WHEN** `[reliability].retry_budget` is not set in config
- **THEN** the relay-runner SHALL allow up to 5 retries per try

#### Scenario: Configured retry budget honoured
- **WHEN** `[reliability].retry_budget = 3` is set
- **THEN** the relay-runner SHALL allow up to 3 retries per try

#### Scenario: Retry exhaustion triggers route advance
- **WHEN** all retries are exhausted for a try
- **THEN** the relay-runner SHALL invoke `OnAgentFailed(entry, "retry-budget-exhausted")` on the v0.6.0 quota-scheduler, which advances to the next route entry; the run continues from the new entry

#### Scenario: Resume on retry when supported
- **WHEN** a try fails AND the active adapter declares `ResumeSupported() == true` AND a session-id was captured
- **THEN** the retry SHALL pass the harness-specific resume parameters and SHALL preserve `.rally/run-state.json`

#### Scenario: Fresh start when resume unsupported
- **WHEN** a try fails AND the active adapter declares `ResumeSupported() == false` (or no session-id was captured)
- **THEN** the retry SHALL restart fresh and `.rally/run-state.json` SHALL be cleared

## ADDED Requirements

### Requirement: Cheap-rotation path for same-harness advances
The system SHALL detect when the scheduler-selected next entry uses the same harness as the current entry and invoke `RotateModel` on the existing adapter instead of full teardown/respawn, when the adapter declares `RotateSupported() == true`. Cross-harness advances SHALL continue to use teardown/respawn.

#### Scenario: Same-harness cheap rotation
- **WHEN** the scheduler advances same-harness and the adapter supports rotation
- **THEN** the relay-runner SHALL call `RotateModel(newModel)` on the existing adapter; the adapter process SHALL NOT be torn down

#### Scenario: Same-harness without rotation support
- **WHEN** the scheduler advances same-harness but the adapter returns `RotateSupported() == false`
- **THEN** the relay-runner SHALL fall back to teardown/respawn

### Requirement: Freeze-detection integration
The system SHALL run the freeze-detection logic (per `freeze-detection` capability) on every live-monitor tick during try execution. When freeze is flagged, the relay-runner SHALL graceful-kill the agent process group and route the resulting failure through the resume-aware retry path with `OnAgentFailed(entry, "freeze")` emitted to the scheduler.

#### Scenario: Freeze flagged during try
- **WHEN** the freeze detector flags the active try frozen during a monitor tick
- **THEN** the relay-runner SHALL graceful-kill the agent process group, emit `OnAgentFailed(entry, "freeze")`, and proceed to the resume-aware retry path

### Requirement: Liveness-probe integration
The system SHALL invoke the liveness probe (per `liveness-probe` capability) when the freeze heuristic flags ambiguity AND `[reliability].liveness_probe = true` AND the active adapter supports probing. A successful probe SHALL clear the freeze flag for that evaluation; a failed/timed-out probe SHALL confirm freeze and trigger graceful-kill.

#### Scenario: Probe disambiguates ambiguous freeze
- **WHEN** the freeze heuristic flags ambiguity, config enables probe, and the adapter supports it
- **THEN** the relay-runner SHALL call `ProbeLiveness(ctx)`; on `(true, nil)` the freeze flag clears and the try continues; on failure/timeout the relay-runner proceeds with graceful-kill
