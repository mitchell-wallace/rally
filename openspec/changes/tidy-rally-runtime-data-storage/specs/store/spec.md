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
The system SHALL maintain per-type maximum record counts: 200 for tries, 50 for relays, 50 for agent status events. Messages SHALL only be windowed when resolved (consumed + addressed) or cancelled — pending messages are never truncated.

#### Scenario: Window exceeded triggers local truncate
- **WHEN** a JSONL file under `.rally/state/` exceeds its window limit after an append
- **THEN** the system SHALL truncate the file in place to retain only the most recent N records, without committing to git

#### Scenario: Pending messages exempt from windowing
- **WHEN** the message window limit is checked
- **THEN** only resolved (consumed + addressed) and cancelled messages SHALL count toward the window limit; pending messages SHALL never be truncated

#### Scenario: Durable history available via telemetry
- **WHEN** records are truncated from a JSONL file
- **THEN** the truncated records SHALL NOT be recoverable from git history; durable retention of try/relay history SHALL instead be provided by the telemetry sink when enabled

## ADDED Requirements

### Requirement: Try commit history
The system SHALL persist, per try, the full ordered list of commits made during that try rather than only a single final commit hash, so causal chains across tries (e.g. blocker → fix → follow-up) are recoverable from the try record.

#### Scenario: Multiple commits in a try preserved
- **WHEN** a try produces more than one commit
- **THEN** the try record SHALL retain all commit hashes from that try, in order

#### Scenario: Single commit still recorded
- **WHEN** a try produces exactly one commit
- **THEN** the try record SHALL retain that commit as a single-element list, preserving existing behavior

### Requirement: Runtime data layout migration
The system SHALL migrate a legacy `.rally/` directory to the new layout on initialization and idempotently on first write. Migration SHALL move flat machine-managed files (`tries.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl`, `hook-audit.jsonl`, `run-state.json`, `current_task.md`) into `.rally/state/`, convert `progress.yaml` into `summary.jsonl`, and remove the legacy `batches/` directory, the legacy top-level `relays/` log directory, and `config.toml.bak`.

#### Scenario: Legacy flat files relocated
- **WHEN** rally initializes a workspace containing flat `.rally/tries.jsonl` (and peers)
- **THEN** the system SHALL move each file into `.rally/state/`, creating the directory if needed, without overwriting an existing target

#### Scenario: progress.yaml converted to summary.jsonl
- **WHEN** a legacy `.rally/progress.yaml` exists and no `.rally/summary.jsonl` is present
- **THEN** the system SHALL emit one JSON line per `recent_runs` entry into `.rally/summary.jsonl` and only then remove `progress.yaml`

#### Scenario: Legacy artifacts removed
- **WHEN** migration runs and finds `.rally/batches/`, a legacy `.rally/relays/` log directory, or `.rally/config.toml.bak`
- **THEN** the system SHALL remove them

#### Scenario: Migration is idempotent
- **WHEN** migration runs on an already-migrated `.rally/`
- **THEN** the system SHALL make no changes and report success
