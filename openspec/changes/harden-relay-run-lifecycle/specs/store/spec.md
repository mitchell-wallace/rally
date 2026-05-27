## ADDED Requirements

### Requirement: Verify reports store
The system SHALL maintain a verification verdict store in `.rally/state/verify-reports.jsonl` (append-only JSONL). Each record SHALL contain: `lap_id`, `verdict` (string: `pass` or `fail`), `timestamp`, `relay_id`, and optional `summary`. This store is used by the stall-recovery logic to determine whether a stalled VERIFY try produced a valid verdict.

#### Scenario: Verdict recorded
- **WHEN** a VERIFY agent produces a verification verdict
- **THEN** the system SHALL append a record to `verify-reports.jsonl`

#### Scenario: Verdict absent on stalled VERIFY try
- **WHEN** a VERIFY try is stalled and no verdict record exists for its lap in `verify-reports.jsonl`
- **THEN** stall-recovery SHALL NOT treat the try as success regardless of file commits

## MODIFIED Requirements

### Requirement: Agent status store
The system SHALL maintain agent status in a dedicated `agent_status.jsonl` file, separate from relay records. Each event records: `agent_type`, optional `model`, `event_type` (paused, unfrozen, frozen, active, probation), `timestamp`, `relay_id`, and optional `reason`. This store persists across relays with a 50-event window. Per-harness-model granularity SHALL be supported via the optional `model` field: rate-limit and freeze state are keyed on `harness:model` rather than harness alone, so a harness using multiple models (e.g. opencode with kimi + gemini) does not freeze wholesale when one provider hits its rate limit. Frozen state SHALL carry its `timestamp` so that freeze decay to probation can be computed; a freeze older than the configured `FreezeDuration` (default 5h) SHALL decay to probation when reconstructing state. Probation SHALL be recorded as a distinct `event_type` and SHALL be handled by `getState`. The system SHALL support an explicit reset via `ResetAgentStatus()`, which truncates agent status history so all harness-model pairs start active.

#### Scenario: Agent paused event recorded
- **WHEN** an agent type is paused after repeated infra-failure exhaustion (>1 infra-class failure within a run)
- **THEN** the system SHALL append a `paused` event to `agent_status.jsonl`, with the `model` field set when applicable

#### Scenario: Agent status restored on startup
- **WHEN** rally starts
- **THEN** the system SHALL replay `agent_status.jsonl` to reconstruct current pause/freeze/probation state per harness-model pair, including timestamps for hourly retry scheduling and freeze-decay evaluation

#### Scenario: Expired freeze decays to probation on reconstruction
- **WHEN** the most recent frozen event for a harness-model pair is older than `FreezeDuration`
- **THEN** state reconstruction SHALL treat that pair as probation (not active), requiring a tentative run before full restoration

#### Scenario: Probation event persisted
- **WHEN** a frozen harness-model pair decays to probation
- **THEN** the system SHALL append a `probation` event to `agent_status.jsonl`

#### Scenario: New relay re-evaluates rather than inherits freeze
- **WHEN** a new relay starts (without `--new`)
- **THEN** the system SHALL re-evaluate frozen agents against `FreezeDuration` rather than unconditionally inheriting a frozen state from a previous relay

#### Scenario: Explicit reset clears pause/freeze/probation
- **WHEN** `rally start --new` is invoked
- **THEN** the system SHALL truncate agent status history so all harness-model pairs start active, independent of prior `agent_status.jsonl` history

#### Scenario: Rate-limit tracked per harness-model
- **WHEN** an opencode harness with model A hits a rate limit but opencode with model B is healthy
- **THEN** only `opencode:model-A` SHALL be paused/frozen; `opencode:model-B` SHALL remain active
