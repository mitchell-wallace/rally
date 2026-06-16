## MODIFIED Requirements

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL emit a structured log event per try, model a relay as a trace whose runs and tries are spans, and report genuine failures as Sentry Issues. Issues SHALL be reserved for failures that warrant operator attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", and detection of `laps done` emitted as text. Lap-integrity mismatches (`wrong_lap_consumed` / `multi_lap_consumed`) SHALL be recorded as `LevelWarning` diagnostic events and SHALL carry `event_kind=lap_pin_mismatch` and `failure_category=lap_pin_mismatch`, but SHALL NOT be captured as Sentry Issues by default. Operator-cancelled attempts SHALL be recorded as cancellation telemetry or spans/logs only and SHALL NOT be captured as failure Issues. Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs, NOT Issues, to avoid alert noise. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

#### Scenario: Try emits a structured log
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit a structured log event for that try

#### Scenario: Relay emits a trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL produce a transaction for the relay with child spans for runs and tries

#### Scenario: Infra failure becomes an Issue
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL capture a Sentry Issue describing the failure

#### Scenario: Relay stall becomes an Issue
- **WHEN** a relay pass ends with all agent types frozen
- **THEN** the sink SHALL capture a Sentry Issue describing the stall, so the lockout is surfaced for operator attention

#### Scenario: Agent-class failure does not raise an Issue
- **WHEN** a try fails with an agent-class failure that remains retry-eligible
- **THEN** the sink SHALL record it as a span/log only and SHALL NOT capture an Issue

#### Scenario: Lap mismatch is warning telemetry
- **WHEN** a try records `wrong_lap_consumed` or `multi_lap_consumed`
- **THEN** the sink SHALL record a `LevelWarning` diagnostic event with `event_kind=lap_pin_mismatch` and `failure_category=lap_pin_mismatch`
- **AND** it SHALL NOT capture a Sentry Issue for the mismatch by default

#### Scenario: Operator cancellation is not failure telemetry
- **WHEN** a try is recorded with outcome `cancelled`
- **THEN** the sink SHALL record cancellation status and source on spans/logs
- **AND** it SHALL NOT capture a Sentry Issue for the cancelled attempt

#### Scenario: Cancelled unfinalized run is not incomplete finalization
- **WHEN** a laps-backed try is cancelled before finalizing with `laps done` or `laps handoff`
- **THEN** telemetry SHALL NOT capture an `incomplete_finalization` Issue for that cancelled try

### Requirement: Warning-level telemetry
The system SHALL support a warning telemetry level for diagnostic events that should be more visible than info events but should not automatically create Sentry Issues. The Sentry sink SHALL map this level to Sentry warning severity when emitting non-Issue diagnostic events.

#### Scenario: Warning event maps to warning severity
- **WHEN** telemetry emits a diagnostic event with `LevelWarning`
- **THEN** the Sentry sink SHALL send it with warning severity rather than info or error severity

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery log event and SHALL NOT capture it as an Issue (rotating to a backup runner is a healthy recovery, not an alert)
