# store Specification

## Purpose
TBD - created by archiving change consolidate-rally-gry. Update Purpose after archive.
## Requirements
### Requirement: JSONL source of truth
The system SHALL persist machine-managed state as JSONL files inside the `.rally/state/` subdirectory at the repository root. Each record type SHALL have its own file: `tries.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl`. The `.rally/state/` directory SHALL be gitignored and is NOT version-controlled; durability for these records is provided by local retention and the opt-in telemetry sink rather than git history.

#### Scenario: Record appended to JSONL
- **WHEN** a new record is created (try, message, relay, or agent status event)
- **THEN** the system SHALL append a JSON line to the corresponding `.rally/state/<type>.jsonl` file and update the in-memory cache

#### Scenario: State directory is gitignored
- **WHEN** rally initializes a workspace
- **THEN** the generated `.rally/.gitignore` SHALL ignore the entire `state/` directory, so JSONL records are excluded from version control

#### Scenario: Only the run summary is tracked
- **WHEN** the `.rally/` directory is committed to git
- **THEN** the tracked data files SHALL be limited to `summary.jsonl` (plus `config.toml`, `agents/`, and `README.md`); the `state/` JSONL records SHALL NOT be committed

### Requirement: Record windowing
Append-only log files (`tries.jsonl`, `relays.jsonl`) SHALL NOT be pruned — they grow unbounded locally. Read-oriented state files SHALL maintain per-type maximum record counts: 500 for agent status events and 200 for resolved messages. Messages SHALL only be windowed when resolved (consumed + addressed) or cancelled — pending messages are never truncated. The agent status window (500) and its truncation semantics (synthesizing a summary event to preserve effective frozen/probation state) are defined by `harden-relay-run-lifecycle`; this change relocates the file and preserves those semantics rather than re-specifying them.

#### Scenario: Window exceeded triggers local truncate (state files only)
- **WHEN** an `agent_status.jsonl` or `messages.jsonl` file under `.rally/state/` exceeds its window limit after an append
- **THEN** the system SHALL truncate the file in place to retain only the most recent N records, without committing to git

#### Scenario: Append-only log files never truncated
- **WHEN** `tries.jsonl` or `relays.jsonl` grows beyond any size
- **THEN** the system SHALL NOT truncate or prune those files; they SHALL grow unbounded

#### Scenario: Pending messages exempt from windowing
- **WHEN** the message window limit is checked
- **THEN** only resolved (consumed + addressed) and cancelled messages SHALL count toward the window limit; pending messages SHALL never be truncated

#### Scenario: Durable history available via telemetry
- **WHEN** records are truncated from agent_status or messages JSONL files
- **THEN** the truncated records SHALL NOT be recoverable from git history; durable retention of try/relay history SHALL instead be provided by the unbounded local append-only files and the telemetry sink when enabled

### Requirement: In-memory cache
The system SHALL load JSONL records into in-memory data structures on startup for fast reads. At current record volumes, in-memory is sufficient — no external database is needed.

#### Scenario: Cache loaded on startup
- **WHEN** rally starts
- **THEN** the system SHALL read all JSONL files into memory, parsing each line into typed record structs

#### Scenario: Cache updated on write
- **WHEN** a record is appended to a JSONL file
- **THEN** the in-memory cache SHALL be updated in the same operation

#### Scenario: Cache loss is non-destructive
- **WHEN** rally restarts (e.g., container wipe)
- **THEN** the system SHALL reload from JSONL files with no data loss

### Requirement: Message model
The system SHALL store messages as JSON objects in `messages.jsonl`, one object per message, updated in place (not event-sourced). Each message has fields: `id`, `body`, `status` (pending, addressed, cancelled), `position` (FIFO ordering), `created_at`, `updated_at`, and optional `consumed_by_run_id` and `relay_id` (for relay-scoped messages). Messages can be scoped to a run (consumed by one run) or a relay (applied across all runs).

#### Scenario: Message created with position
- **WHEN** a new message is created
- **THEN** it SHALL be appended with a `position` field set to the next available position in the pending queue and `status: "pending"`

#### Scenario: Message reordered
- **WHEN** a user reorders a pending message
- **THEN** the system SHALL rewrite the messages file with updated position values for affected messages

#### Scenario: Message consumed by run
- **WHEN** a run consumes a pending message
- **THEN** the system SHALL update the message's `consumed_by_run_id` field and rewrite the file

#### Scenario: Message addressed
- **WHEN** a try result indicates the message was addressed
- **THEN** the system SHALL update the message's `status` to `"addressed"` and rewrite the file

#### Scenario: Messages loaded into cache
- **WHEN** the in-memory cache is loaded from `messages.jsonl`
- **THEN** the system SHALL parse each line as a complete message object to reconstruct current message state

### Requirement: Relay record
The system SHALL store relay records in `relays.jsonl` with fields: `id`, `target_iterations`, `completed_iterations`, `agent_mix`, `started_at`, `ended_at`, `first_try_id`, `last_try_id`, and `consumed_message_ids` (relay-level messages consumed during this relay).

#### Scenario: Relay record tracks try range
- **WHEN** tries execute within a relay
- **THEN** the relay record SHALL track `first_try_id` (set on first try) and `last_try_id` (updated after each try)

#### Scenario: Relay-level message consumption tracked
- **WHEN** a relay-level message is consumed
- **THEN** the relay record SHALL include the message ID in `consumed_message_ids`

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

### Requirement: Unified store interface
The system SHALL provide a single `Store` interface that abstracts JSONL + in-memory operations, exposing write methods (which append/rewrite JSONL and update in-memory cache) and read methods (which query the in-memory cache).

#### Scenario: Write goes through JSONL first
- **WHEN** a caller writes a record via the Store interface
- **THEN** the record SHALL be persisted to the JSONL file before the in-memory cache is updated

#### Scenario: Read queries in-memory cache
- **WHEN** a caller reads records via the Store interface
- **THEN** the query SHALL execute against the in-memory cache for performance

### Requirement: Try commit history
The system SHALL persist, per try, the full ordered list of commits made during that try (as a `CommitHistory []string` field on `TryRecord`) rather than only a single final commit hash, so causal chains across tries (e.g. blocker → fix → follow-up) are recoverable from the try record. The existing `CommitHash string` field SHALL be retained for backward compatibility and set to the last element of `CommitHistory`.

#### Scenario: Multiple commits in a try preserved
- **WHEN** a try produces more than one commit
- **THEN** the try record SHALL retain all commit hashes from that try in `CommitHistory`, in order, and `CommitHash` SHALL be set to the last element

#### Scenario: Single commit still recorded
- **WHEN** a try produces exactly one commit
- **THEN** `CommitHistory` SHALL contain that commit as a single-element list, and `CommitHash` SHALL be set to that commit, preserving existing behavior

### Requirement: Runtime data layout migration
The system SHALL migrate a legacy `.rally/` directory to the new layout on initialization (`runInit`). Migration SHALL move flat machine-managed files (`tries.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl`, `hook-audit.jsonl`, `run-state.json`, `current_task.md`) into `.rally/state/`, and remove the legacy `batches/` directory and the legacy top-level `relays/` log directory if present. Existing `progress.yaml` SHALL be left as-is (NOT converted to `summary.jsonl` — new writes go to `summary.jsonl` only). `config.toml.bak` is user-managed and SHALL NOT be touched.

#### Scenario: Legacy flat files relocated
- **WHEN** rally initializes a workspace containing flat `.rally/tries.jsonl` (and peers)
- **THEN** the system SHALL move each file into `.rally/state/`, creating the directory if needed, without overwriting an existing target

#### Scenario: Legacy artifacts removed
- **WHEN** migration runs and finds `.rally/batches/` or a legacy `.rally/relays/` log directory
- **THEN** the system SHALL remove them

#### Scenario: Migration is idempotent
- **WHEN** migration runs on an already-migrated `.rally/`
- **THEN** the system SHALL make no changes and report success

### Requirement: Persisted text fields are length-capped
When persisting final-snippet text, the system SHALL cap each free-text field to a bounded maximum length of 3000 runes before writing. Truncation SHALL preserve both the head and tail of the content with an explicit truncation marker, consistent with the recent-context truncation used when building prompts. The cap SHALL apply regardless of which harness or code path produced the text, serving as a durable backstop against runaway output in `tries.jsonl` and `summary.jsonl`.

#### Scenario: Oversized summary capped on write
- **WHEN** a try record whose `summary` exceeds the cap is persisted
- **THEN** the stored `summary` SHALL be truncated to at most 3000 runes with a head+tail truncation marker

#### Scenario: Small summary unchanged
- **WHEN** a try record whose `summary` is within the cap is persisted
- **THEN** the stored `summary` SHALL be written verbatim with no truncation marker

#### Scenario: Cap applies to try record final-snippet fields
- **WHEN** a try record is persisted
- **THEN** `summary` and `remaining_work` SHALL be subject to the 3000-rune cap

#### Scenario: Cap applies to summary log fields
- **WHEN** a finalized run or handoff summary is appended to `summary.jsonl`
- **THEN** `RunEntry.Summary`, `HandoffEntry.Summary`, and each free-text `HandoffEntry.Followups` string SHALL be subject to the same 3000-rune cap

