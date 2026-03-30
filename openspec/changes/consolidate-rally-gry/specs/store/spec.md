## ADDED Requirements

### Requirement: JSONL source of truth
The system SHALL persist all durable state as JSONL files in the `.rally/` directory at the repository root. Each record type SHALL have its own file: `sessions.jsonl`, `messages.jsonl`, `relays.jsonl`.

#### Scenario: Record appended to JSONL
- **WHEN** a new record is created (session, message, or relay)
- **THEN** the system SHALL append a JSON line to the corresponding JSONL file and update the in-memory cache

#### Scenario: JSONL files are git-tracked
- **WHEN** the `.rally/` directory is committed to git
- **THEN** the JSONL files SHALL be included in version control, making state durable across container lifecycles

### Requirement: Record windowing
The system SHALL maintain a maximum of approximately 100 records per JSONL file. When the window is exceeded, the oldest records SHALL be truncated.

#### Scenario: Window exceeded triggers truncation
- **WHEN** a JSONL file exceeds 100 records after an append
- **THEN** the system SHALL truncate the file to retain only the most recent 100 records

#### Scenario: Historical data accessible via git
- **WHEN** records are truncated from a JSONL file
- **THEN** the truncated records SHALL remain accessible in git history

### Requirement: In-memory cache
The system SHALL load JSONL records into in-memory data structures on startup for fast reads. At ~100 records per file, in-memory is sufficient — no external database is needed.

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
The system SHALL use an event-sourced message model (carried forward from rally v0.1.x). Messages in `messages.jsonl` are event records with types: `message_created`, `message_updated`, `message_consumed`, `message_cancelled`. Each message has a `position` field for FIFO ordering. Messages can be scoped to a run (consumed by one run) or a relay (applied across all runs in a relay). The `position` field enables reordering of pending messages via the TUI.

#### Scenario: Message created with position
- **WHEN** a new message is created
- **THEN** it SHALL be appended with a `position` field set to the next available position in the pending queue

#### Scenario: Message reordered
- **WHEN** a user reorders a pending message via the TUI
- **THEN** the system SHALL append a `message_updated` event with the new position, and re-sequence other pending messages accordingly

#### Scenario: Message consumed by run
- **WHEN** a run consumes a pending message
- **THEN** the system SHALL append a `message_consumed` event referencing the run ID

#### Scenario: Message events replayed into cache
- **WHEN** the in-memory cache is loaded from `messages.jsonl`
- **THEN** the system SHALL replay all events to reconstruct current message state (pending, consumed, addressed, cancelled)

### Requirement: Unified store interface
The system SHALL provide a single `Store` interface that abstracts JSONL + in-memory operations, exposing write methods (which append to JSONL and update in-memory cache) and read methods (which query the in-memory cache).

#### Scenario: Write goes through JSONL first
- **WHEN** a caller writes a record via the Store interface
- **THEN** the record SHALL be appended to the JSONL file before the in-memory cache is updated

#### Scenario: Read queries in-memory cache
- **WHEN** a caller reads records via the Store interface
- **THEN** the query SHALL execute against the in-memory cache for performance
