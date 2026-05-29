## MODIFIED Requirements

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

## ADDED Requirements

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
