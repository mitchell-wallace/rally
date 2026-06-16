## ADDED Requirements

### Requirement: Try outcome status
The system SHALL persist each try's outcome using a stable status field suitable for machine consumers. The status SHALL distinguish at least successful completion, failed attempts, incomplete attempts, and operator-cancelled attempts. For backwards compatibility, existing completed/success booleans MAY remain, but consumers SHALL NOT need to parse human-readable failure text to identify a cancelled try.

#### Scenario: Cancelled try persisted with source
- **WHEN** a try is cancelled by an operator action
- **THEN** the try record SHALL include status `cancelled` and a cancellation source such as `skip`, `graceful_stop`, or `quit_now`
- **AND** the human-readable reason SHALL NOT be the only place cancellation is represented

#### Scenario: Legacy completed boolean remains derivable
- **WHEN** a cancelled try is persisted
- **THEN** any legacy completed/success boolean SHALL indicate the try did not complete successfully
- **AND** the stable status SHALL still distinguish cancellation from failure

### Requirement: Active try run-state metadata
The system SHALL persist transient active try metadata in run-state so live CLI commands can target an in-flight try before its final record is appended to try history. The metadata SHALL include the active try ID and active log path, and SHALL be removed or cleared once the try becomes a completed historical record.

#### Scenario: Active try metadata available during execution
- **WHEN** an executor is running for a try
- **THEN** run-state SHALL expose the active try ID and active log path

#### Scenario: Active try metadata removed after append
- **WHEN** the try is appended to try history
- **THEN** run-state SHALL no longer expose that try as active
