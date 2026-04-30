## ADDED Requirements

### Requirement: Progress log location and format
The system SHALL maintain the human-readable progress log at `.rally/progress.yaml` (relative to workspace root). The legacy location `docs/orchestration/rally-progress.yaml` SHALL no longer be read or written. The file format SHALL remain YAML for v0.4.0; the format question is deferred for later review.

#### Scenario: Fresh workspace
- **WHEN** rally first writes progress data in a workspace that has no `.rally/progress.yaml`
- **THEN** the system SHALL create `.rally/progress.yaml` with a top-level `version`, `updated_at`, `history_window`, and `recent_runs: []`

#### Scenario: One-shot copy from legacy location
- **WHEN** rally starts in a workspace where `docs/orchestration/rally-progress.yaml` exists and `.rally/progress.yaml` does not
- **THEN** the system SHALL copy the legacy file's contents to `.rally/progress.yaml`, leave the legacy file in place untouched, and proceed to write subsequent updates only to the new location

### Requirement: Run-entry schema
Each entry in `recent_runs` SHALL use `run_id` (not `session_id`) as its identifier and SHALL be keyed/scoped per-run, not per-session. The top-level array SHALL be named `recent_runs`. The legacy keys `recent_sessions` and `session_id` SHALL be renamed in place on first post-upgrade write.

#### Scenario: Field renames applied on first write
- **WHEN** rally writes to `.rally/progress.yaml` for the first time after upgrade and the loaded structure contains `recent_sessions` or `session_id` keys
- **THEN** the system SHALL emit `recent_runs` and `run_id` in the written output; the legacy keys SHALL not appear in the new file

### Requirement: `beads_completed` field
Each entry in `recent_runs` SHALL include a `beads_completed` field if and only if the run was performed in microbeads-backed mode. The field's value SHALL be either a list of bead IDs closed during the run, or the literal string `"none"` if no beads were closed. The value SHALL NOT be `null` and SHALL NOT be an empty list `[]`. In no-backend mode, the field SHALL be omitted entirely.

#### Scenario: Beads closed during the run
- **WHEN** the agent calls `mb done` one or more times during a microbeads-backed run
- **THEN** the entry's `beads_completed` SHALL be the ordered list of those bead IDs, e.g. `["mb-a3f2", "mb-b91c"]`

#### Scenario: No beads closed in microbeads mode
- **WHEN** a microbeads-backed run finishes without any `mb done` calls
- **THEN** the entry's `beads_completed` SHALL be the literal string `"none"`

#### Scenario: No-backend mode
- **WHEN** the run was performed in no-backend mode
- **THEN** the entry SHALL omit the `beads_completed` field entirely

### Requirement: `handoff` field
Each entry where `mb handoff` was finalised SHALL include a `handoff` field with sub-fields `reason`, `followups` (list of strings), and `created_bead_ids` (list of bead IDs created at the queue head during the handoff). Entries from runs that ended via `mb wrapup` (or stub finalisation) SHALL NOT include the `handoff` field.

#### Scenario: Handoff finalised
- **WHEN** the agent invoked `mb handoff` and the second call (with `--reason` and at least zero `--followup` arguments) completed successfully
- **THEN** the entry SHALL include `handoff: { reason: "...", followups: [...], created_bead_ids: [...] }`

### Requirement: Stub entries for incomplete runs
The relay loop SHALL write a stub entry to `recent_runs` whenever an agent's run ends without calling `mb wrapup` and without finalising via the second `mb handoff` call. The stub's `summary` SHALL be the first 160 characters of the agent's final console-printed output. `beads_completed` SHALL still reflect any IDs accumulated by the `mb done` after-hook during the run. This guarantees `recent_runs` grows monotonically across runs.

#### Scenario: Agent exits without wrapup or handoff
- **WHEN** an agent's session ends without finalising via `mb wrapup` or the second `mb handoff` call
- **THEN** the relay loop SHALL append a stub entry whose `summary` is the first 160 characters of the agent's final console-printed output (the same text rally prints back to the operator at run-end)

#### Scenario: Stub entry preserves recorded bead closures
- **WHEN** an agent calls `mb done` during a run but exits without `mb wrapup`
- **THEN** the stub entry SHALL include the `beads_completed` IDs accumulated by the `mb done` after-hook

### Requirement: `rally progress` subcommand visibility
The system SHALL expose `rally progress` differently per mode. In microbeads-backed mode, `rally progress` SHALL be a private subcommand called only by the installed hook scripts; the agent prompt template SHALL NOT mention it. In no-backend mode, `rally progress --summary "..." --followup "..."` SHALL be a public, agent-facing CLI documented in the prompt template as the explicit exception to the "agents don't touch rally CLI" rule.

#### Scenario: Microbeads-backed mode prompt
- **WHEN** rally builds the agent prompt in microbeads-backed mode
- **THEN** the prompt SHALL NOT contain the string `rally progress`

#### Scenario: No-backend mode prompt
- **WHEN** rally builds the agent prompt in no-backend mode
- **THEN** the prompt SHALL include explicit instructions to call `rally progress --summary "..." --followup "..."` at run-end
