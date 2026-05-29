## MODIFIED Requirements

### Requirement: Agent status store
The system SHALL maintain agent status in a dedicated `agent_status.jsonl` file, separate from relay records. Each event records: `agent_type`, optional `model`, `event_type` (paused, unfrozen, frozen, active, probation), `timestamp`, `relay_id`, and optional `reason`. This store persists across relays with a 500-event window. On window truncation, the system SHALL synthesize a summary event preserving the latest effective state and timestamp for any active frozen or probation entries, so `getState` always reconstructs correctly.

Per-harness-model granularity SHALL be supported via the `model` field: rate-limit and freeze state are keyed on `harness:model` using a `ResilienceKey` type. `GetAgentStatus` SHALL accept model as a parameter and filter on both `agent_type` and `model` when model is present. Frozen state SHALL carry its `timestamp` so that freeze decay to probation can be computed; a freeze older than the configured `FreezeDuration` (5h, hardcoded constant) SHALL decay to probation when reconstructing state. `getState` SHALL remain a pure read function; the probation event SHALL be persisted exactly once by `syncRecoverySignals` when it first observes the transition.

The system SHALL support an explicit reset via `ResetAgentStatus()`, which truncates agent status history so all harness-model pairs start active.

#### Scenario: Agent paused event recorded
- **WHEN** a harness-model pair is paused after repeated infra-failure exhaustion (>1 infra-class failure within a run)
- **THEN** the system SHALL append a `paused` event to `agent_status.jsonl`, with the `model` field set when applicable

#### Scenario: Agent status restored on startup
- **WHEN** rally starts
- **THEN** the system SHALL replay `agent_status.jsonl` to reconstruct current pause/freeze/probation state per harness-model pair, including timestamps for hourly retry scheduling and freeze-decay evaluation

#### Scenario: Expired freeze decays to probation on reconstruction
- **WHEN** the most recent frozen event for a harness-model pair is older than `FreezeDuration`
- **THEN** state reconstruction SHALL treat that pair as probation (not active), requiring a tentative run before full restoration

#### Scenario: Probation event persisted
- **WHEN** a frozen harness-model pair decays to probation
- **THEN** `syncRecoverySignals` SHALL append a `probation` event to `agent_status.jsonl` exactly once per transition

#### Scenario: Window truncation preserves effective state
- **WHEN** the 500-event window truncates and active frozen or probation entries would be lost
- **THEN** the system SHALL synthesize a summary event preserving the latest effective state and timestamp

#### Scenario: New relay re-evaluates rather than inherits freeze
- **WHEN** a new relay starts (without `--new`)
- **THEN** the system SHALL re-evaluate frozen agents against `FreezeDuration` via the pure `getState` rather than unconditionally inheriting a frozen state from a previous relay

#### Scenario: Explicit reset clears pause/freeze/probation
- **WHEN** `rally start --new` is invoked
- **THEN** the system SHALL truncate agent status history so all harness-model pairs start active, independent of prior `agent_status.jsonl` history

#### Scenario: Rate-limit tracked per harness-model
- **WHEN** an opencode harness with model A hits a rate limit but opencode with model B is healthy
- **THEN** only `opencode:model-A` SHALL be paused/frozen; `opencode:model-B` SHALL remain active
