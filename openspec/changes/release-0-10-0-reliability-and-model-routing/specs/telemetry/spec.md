## MODIFIED Requirements

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL emit a structured custom event per try, model relay/run/try timing through the active telemetry sink's span abstraction, and report genuine failures as backend-neutral operator-worthy failure events. Operator-worthy failures SHALL be reserved for failures that warrant attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and lap-integrity violations that genuinely leave the queue unsafe. Lap-integrity mismatches (`wrong_lap_consumed` / `multi_lap_consumed`) SHALL be recorded as `LevelWarning` diagnostic events and SHALL carry `event_kind=lap_pin_mismatch` and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`, but SHALL NOT be captured as operator-worthy failures by default and SHALL NOT attach `failure_category` because 0.9.x reserves `failure_category` for failed lifecycle outcomes. Operator-cancelled attempts SHALL be recorded as cancellation telemetry or spans/logs only and SHALL NOT be captured as operator-worthy failures. The timeout/handoff lifecycle outcomes (`TryOutcome` `run_timeout`, `handoff_requested`, and `handoff_timeout`) SHALL be recorded as spans/logs/custom events and SHALL NOT be captured as operator-worthy failures. A `needs_user` recovery classification or relay-synthesized cap signal MAY be captured as an operator-worthy failure, while the other recovery classifications SHALL remain spans/logs/custom events. Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs/custom events, NOT operator-worthy failures, to avoid alert noise. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

#### Scenario: Try emits a structured log
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit a structured event for that try

#### Scenario: Relay emits a trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL produce a relay/run/try timing hierarchy through its span abstraction

#### Scenario: Infra failure becomes operator-worthy telemetry
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL capture an operator-worthy failure event describing the failure

#### Scenario: Relay stall becomes operator-worthy telemetry
- **WHEN** a relay pass ends with all agent types frozen
- **THEN** the sink SHALL capture an operator-worthy failure event describing the stall, so the lockout is surfaced for operator attention

#### Scenario: Agent-class failure does not raise operator telemetry
- **WHEN** a try fails with an agent-class failure that remains retry-eligible
- **THEN** the sink SHALL record it as a span/log/custom event only and SHALL NOT capture an operator-worthy failure

#### Scenario: Lap mismatch is warning telemetry
- **WHEN** a try records `wrong_lap_consumed` or `multi_lap_consumed`
- **THEN** the sink SHALL record a `LevelWarning` diagnostic event with `event_kind=lap_pin_mismatch` and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`
- **AND** it SHALL NOT capture an operator-worthy failure for the mismatch by default
- **AND** it SHALL NOT attach `failure_category` unless the try also has a failed lifecycle outcome with a real `FailureCategory`

#### Scenario: Operator cancellation is not failure telemetry
- **WHEN** a try is recorded with outcome `cancelled`
- **THEN** the sink SHALL record cancellation status and source on spans/logs
- **AND** it SHALL NOT capture an operator-worthy failure for the cancelled attempt

#### Scenario: Cancelled unfinalized run is not incomplete finalization
- **WHEN** a laps-backed try is cancelled before finalizing with `laps done` or `laps handoff`
- **THEN** telemetry SHALL NOT capture an `incomplete_finalization` operator-worthy failure for that cancelled try

### Requirement: Warning-level telemetry
The system SHALL support a warning telemetry level for diagnostic events that should be more visible than info events but should not automatically create operator-worthy failures. The New Relic Event API sink SHALL emit `level=warning` on the resulting `RallyDiagnostic` custom event, and the legacy Sentry fallback SHALL map this level to Sentry warning severity when emitting non-Issue diagnostic events.

#### Scenario: Warning event maps to warning severity
- **WHEN** telemetry emits a diagnostic event with `LevelWarning`
- **THEN** the active sink SHALL send it with warning severity rather than info or error severity

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery log/custom event and SHALL NOT capture it as an operator-worthy failure (rotating to a backup runner is a healthy recovery, not an alert)
