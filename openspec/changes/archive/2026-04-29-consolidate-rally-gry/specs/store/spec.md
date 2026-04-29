## ADDED Requirements

### Requirement: JSONL source of truth
The system SHALL persist all durable state as JSONL files in the `.rally/` directory at the repository root. Each record type SHALL have its own file: `tries.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl`.

#### Scenario: Record appended to JSONL
- **WHEN** a new record is created (try, message, relay, or agent status event)
- **THEN** the system SHALL append a JSON line to the corresponding JSONL file and update the in-memory cache

#### Scenario: JSONL files are git-tracked
- **WHEN** the `.rally/` directory is committed to git
- **THEN** the JSONL files SHALL be included in version control, making state durable across container lifecycles

### Requirement: Record windowing
The system SHALL maintain per-type maximum record counts: 200 for tries, 50 for relays, 50 for agent status events. Messages SHALL only be windowed when resolved (consumed + addressed) or cancelled — pending messages are never truncated.

#### Scenario: Window exceeded triggers commit-then-truncate
- **WHEN** a JSONL file exceeds its window limit after an append
- **THEN** the system SHALL first commit the current file to git, then truncate to retain only the most recent N records, then commit the truncated file — ensuring all records are preserved in git history

#### Scenario: Pending messages exempt from windowing
- **WHEN** the message window limit is checked
- **THEN** only resolved (consumed + addressed) and cancelled messages SHALL count toward the window limit; pending messages SHALL never be truncated

#### Scenario: Historical data accessible via git
- **WHEN** records are truncated from a JSONL file
- **THEN** the truncated records SHALL remain accessible in git history via the pre-truncation commit

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
The system SHALL maintain agent status in a dedicated `agent_status.jsonl` file, separate from relay records. Each event records: `agent_type`, `event_type` (paused, unfrozen, frozen, active), `timestamp`, `relay_id`, and optional `reason`. This store persists across relays with a 50-event window.

#### Scenario: Agent paused event recorded
- **WHEN** an agent type is paused after retry exhaustion
- **THEN** the system SHALL append a `paused` event to `agent_status.jsonl`

#### Scenario: Agent status restored on startup
- **WHEN** rally starts
- **THEN** the system SHALL replay `agent_status.jsonl` to reconstruct current pause/freeze state per agent type, including timestamps for hourly retry scheduling

#### Scenario: Agent status persists across relays
- **WHEN** a new relay starts
- **THEN** the system SHALL check `agent_status.jsonl` for any agents that are still frozen from a previous relay

### Requirement: Unified store interface
The system SHALL provide a single `Store` interface that abstracts JSONL + in-memory operations, exposing write methods (which append/rewrite JSONL and update in-memory cache) and read methods (which query the in-memory cache).

#### Scenario: Write goes through JSONL first
- **WHEN** a caller writes a record via the Store interface
- **THEN** the record SHALL be persisted to the JSONL file before the in-memory cache is updated

#### Scenario: Read queries in-memory cache
- **WHEN** a caller reads records via the Store interface
- **THEN** the query SHALL execute against the in-memory cache for performance
