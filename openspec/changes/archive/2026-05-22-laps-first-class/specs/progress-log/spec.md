## ADDED Requirements

### Requirement: Progress log location and format
The system SHALL maintain the human-readable progress log at `.rally/progress.yaml` (relative to workspace root). The file format SHALL remain YAML for v0.4.0; the format question is deferred for later review.

#### Scenario: Fresh workspace
- **WHEN** rally first writes progress data in a workspace that has no `.rally/progress.yaml`
- **THEN** the system SHALL create `.rally/progress.yaml` with a top-level `version`, `updated_at`, `history_window`, and `recent_runs: []`

### Requirement: Run-entry schema
Each entry in `recent_runs` SHALL use `run_id` as its identifier and SHALL be keyed/scoped per-run. The top-level array SHALL be named `recent_runs`.

#### Scenario: Entry structure
- **WHEN** a run completes and a progress entry is written
- **THEN** the entry SHALL contain at minimum `run_id`, `summary`, and `updated_at` fields

### Requirement: `laps_completed` field
Each entry in `recent_runs` SHALL include a `laps_completed` field if and only if the run was performed with laps enabled. The field's value SHALL be either a list of lap IDs closed during the run, or the literal string `"none"` if no laps were closed. The value SHALL NOT be `null` and SHALL NOT be an empty list `[]`. When laps is disabled, the field SHALL be omitted entirely.

#### Scenario: Laps closed during the run
- **WHEN** the agent calls `laps done` one or more times during a laps-enabled run
- **THEN** the entry's `laps_completed` SHALL be the ordered list of those lap IDs, e.g. `["lap-a3f2", "lap-b91c"]`

#### Scenario: No laps closed
- **WHEN** a laps-enabled run finishes without any `laps done` calls
- **THEN** the entry's `laps_completed` SHALL be the literal string `"none"`

#### Scenario: Laps disabled
- **WHEN** the run was performed with laps disabled
- **THEN** the entry SHALL omit the `laps_completed` field entirely

### Requirement: `handoff` field
Each entry where the run was finalised via the handoff path SHALL include a `handoff` field with sub-fields `summary`, `followups` (list of strings), and `created_lap_ids` (list of lap IDs created at the queue head during the handoff). Entries from runs that ended via normal completion (or stub finalisation) SHALL NOT include the `handoff` field.

#### Scenario: Handoff finalised
- **WHEN** `rally progress --handoff` completes successfully (routed from `laps wrapup` after `laps handoff` was called)
- **THEN** the entry SHALL include `handoff: { summary: "...", followups: [...], created_lap_ids: [...] }` where each followup was created as a lap at the queue head via `laps add head`

### Requirement: Stub entries for incomplete runs
The relay loop SHALL write a stub entry to `recent_runs` whenever an agent's run ends without calling `laps wrapup` (in laps-enabled mode) or `rally progress --complete` (in no-backend mode). The stub's `summary` SHALL be the first 160 characters of the agent's final console-printed output. `laps_completed` SHALL still reflect any IDs accumulated by the `laps done` after-hook during the run. This guarantees `recent_runs` grows monotonically across runs.

#### Scenario: Agent exits without wrapup
- **WHEN** an agent's session ends without finalising via `laps wrapup` or `rally progress --complete`
- **THEN** the relay loop SHALL append a stub entry whose `summary` is the first 160 characters of the agent's final console-printed output

#### Scenario: Stub entry preserves recorded lap closures
- **WHEN** an agent calls `laps done` during a run but exits without `laps wrapup`
- **THEN** the stub entry SHALL include the `laps_completed` IDs accumulated by the `laps done` after-hook

### Requirement: `rally progress` subcommand visibility
The system SHALL expose `rally progress` differently based on whether laps is enabled. When laps is enabled, `rally progress` SHALL be a private subcommand called only by the installed hook scripts; the agent prompt template SHALL NOT mention it. When laps is disabled, `rally progress --summary "..." --followup "..."` SHALL be a public, agent-facing CLI documented in the prompt template as the explicit exception to the "agents don't touch rally CLI" rule.

#### Scenario: Laps-enabled prompt
- **WHEN** rally builds the agent prompt with laps enabled
- **THEN** the prompt SHALL NOT contain the string `rally progress`

#### Scenario: Laps-disabled prompt
- **WHEN** rally builds the agent prompt with laps disabled
- **THEN** the prompt SHALL include explicit instructions to call `rally progress --summary "..." --followup "..."` at run-end
