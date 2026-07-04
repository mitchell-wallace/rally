## MODIFIED Requirements

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL keep existing New Relic custom event names and telemetry taxonomy stable while internal code adopts outing/driver vocabulary. The custom event names SHALL remain `RallyTry`, `RallyRoute`, `RallyDiagnostic`, and `RallyFailure`. Routing decisions SHALL remain `RallyRoute` events and try outcomes SHALL remain `RallyTry` events.

#### Scenario: Try emits existing RallyTry event
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit a `RallyTry` structured event for that try
- **AND** it SHALL NOT dual-emit a differently named outing event

#### Scenario: Route fallback remains RallyRoute
- **WHEN** routing falls back from one driver to another
- **THEN** the sink SHALL emit the existing `RallyRoute` custom event
- **AND** it SHALL NOT emit a second renamed custom event for the same decision

### Requirement: Telemetry tagging and correlation
Every telemetry event SHALL keep the existing external correlation attribute names: `relay_id`, `run_id`, `try_id`, `role`, `runner`, `repo`, and `lap_id` where applicable. The historical `run_id` telemetry attribute SHALL carry the outing identity, and the historical `runner` telemetry attribute SHALL carry the driver identity (`harness:model`). Internal code MAY use outing/driver names, but emitted telemetry keys SHALL remain stable for dashboard continuity.

#### Scenario: Events carry stable correlation tags
- **WHEN** any telemetry event is emitted during a try
- **THEN** it SHALL include the available existing tags such as `relay_id`, `run_id`, `try_id`, `role`, `runner`, `repo`, and `lap_id`
- **AND** it SHALL NOT replace those external keys with `outing_id` or `driver`

#### Scenario: Runner tag carries the resolved driver model
- **WHEN** a try runs on a route configured with a bare alias and the executor resolved a default model
- **THEN** the `runner` tag SHALL remain `<harness>:<resolved-model>`
- **AND** internal driver terminology SHALL NOT collapse the external tag to the bare harness name
