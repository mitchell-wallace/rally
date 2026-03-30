## ADDED Requirements

### Requirement: JSONL source of truth
The system SHALL persist all durable state as JSONL files in the `.rally/` directory at the repository root. Each record type SHALL have its own file: `runs.jsonl`, `messages.jsonl`, `relays.jsonl`.

#### Scenario: Record appended to JSONL
- **WHEN** a new record is created (run, message, or relay)
- **THEN** the system SHALL append a JSON line to the corresponding JSONL file and then update the SQLite cache

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

### Requirement: SQLite cache
The system SHALL maintain a SQLite database at `~/.local/share/rally/rally.db` as a read-optimized cache derived entirely from the JSONL source of truth.

#### Scenario: Cache rebuilt on startup
- **WHEN** rally starts and the SQLite cache does not exist or is stale
- **THEN** the system SHALL replay all JSONL files into the SQLite database, creating/updating all records

#### Scenario: Cache updated on write
- **WHEN** a record is appended to a JSONL file
- **THEN** the corresponding SQLite table SHALL be updated in the same operation

#### Scenario: Cache loss is non-destructive
- **WHEN** the SQLite database is deleted or corrupted
- **THEN** the system SHALL rebuild it from JSONL on next startup with no data loss

### Requirement: Unified store interface
The system SHALL provide a single `Store` interface that abstracts JSONL + SQLite operations, exposing write methods (which write to JSONL and update cache) and read methods (which query the SQLite cache).

#### Scenario: Write goes through JSONL first
- **WHEN** a caller writes a record via the Store interface
- **THEN** the record SHALL be appended to the JSONL file before the SQLite cache is updated

#### Scenario: Read queries SQLite cache
- **WHEN** a caller reads records via the Store interface
- **THEN** the query SHALL execute against the SQLite cache for performance
