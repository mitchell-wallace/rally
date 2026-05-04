## ADDED Requirements

### Requirement: `rally tail` streams the active try log
The system SHALL provide a `rally tail` subcommand that streams the current try's log file with `tail -f` semantics. The subcommand SHALL work concurrently with a running relay (no lock contention with the writer). It SHALL accept an optional `--try N` flag to stream a specific try by its 1-based index in `.rally/tries.jsonl` instead of the latest.

#### Scenario: Tail the active try
- **WHEN** a relay is running in the workspace and the operator invokes `rally tail`
- **THEN** the system SHALL locate the most recent try's log file path from `.rally/tries.jsonl` and SHALL stream new content to stdout as it is appended

#### Scenario: Tail a specific past try
- **WHEN** the operator invokes `rally tail --try 3`
- **THEN** the system SHALL locate the third try's log file and SHALL stream from the start of the file, then continue to follow if still being written

#### Scenario: No active try
- **WHEN** the operator invokes `rally tail` in a workspace with no `.rally/tries.jsonl` or an empty one
- **THEN** the subcommand SHALL exit non-zero with the message `no tries recorded in this workspace`

#### Scenario: Specified try out of range
- **WHEN** the operator invokes `rally tail --try N` and N is greater than the number of recorded tries (or less than 1)
- **THEN** the subcommand SHALL exit non-zero with a message naming the valid range
