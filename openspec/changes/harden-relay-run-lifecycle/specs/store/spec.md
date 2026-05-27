## MODIFIED Requirements

### Requirement: Agent status store
The system SHALL maintain agent status in a dedicated `agent_status.jsonl` file, separate from relay records. Each event records: `agent_type`, `event_type` (paused, unfrozen, frozen, active), `timestamp`, `relay_id`, and optional `reason`. This store persists across relays with a 50-event window. Frozen state SHALL carry its `timestamp` so that freeze decay can be computed; a freeze that is older than the configured `FreezeDuration` SHALL be treated as expired when reconstructing state. The system SHALL support an explicit reset of pause/freeze state, used by `rally start --new`.

#### Scenario: Agent paused event recorded
- **WHEN** an agent type is paused after infra-failure exhaustion
- **THEN** the system SHALL append a `paused` event to `agent_status.jsonl`

#### Scenario: Agent status restored on startup
- **WHEN** rally starts
- **THEN** the system SHALL replay `agent_status.jsonl` to reconstruct current pause/freeze state per agent type, including timestamps for hourly retry scheduling and freeze-decay evaluation

#### Scenario: Expired freeze treated as active on reconstruction
- **WHEN** the most recent frozen event for an agent type is older than `FreezeDuration`
- **THEN** state reconstruction SHALL treat that agent type as active (or probation) rather than frozen

#### Scenario: New relay re-evaluates rather than inherits freeze
- **WHEN** a new relay starts (without `--new`)
- **THEN** the system SHALL re-evaluate frozen agents against `FreezeDuration` rather than unconditionally inheriting a frozen state from a previous relay

#### Scenario: Explicit reset clears pause/freeze
- **WHEN** `rally start --new` is invoked
- **THEN** the system SHALL reset pause/freeze state so all agent types start active, independent of prior `agent_status.jsonl` history
