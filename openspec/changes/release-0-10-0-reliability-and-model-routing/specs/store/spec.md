## ADDED Requirements

### Requirement: Try outcome status
The system SHALL persist each try's outcome using `TryRecord.Status string json:"status,omitempty"` suitable for machine consumers. The status SHALL distinguish at least `success`, `failed`, `incomplete`, and `cancelled`. Operator-cancelled attempts SHALL also persist `TryRecord.CancellationSource string json:"cancellation_source,omitempty"` with values such as `skip`, `graceful_stop`, or `quit_now`. For backwards compatibility, existing completed/success booleans MAY remain, but consumers SHALL NOT need to parse human-readable failure text to identify a cancelled try.

#### Scenario: Cancelled try persisted with source
- **WHEN** a try is cancelled by an operator action
- **THEN** the try record SHALL include status `cancelled` and a cancellation source such as `skip`, `graceful_stop`, or `quit_now`
- **AND** the human-readable reason SHALL NOT be the only place cancellation is represented

#### Scenario: Legacy completed boolean remains derivable
- **WHEN** a cancelled try is persisted
- **THEN** any legacy completed/success boolean SHALL indicate the try did not complete successfully
- **AND** the stable status SHALL still distinguish cancellation from failure

### Requirement: Active try run-state metadata
The system SHALL persist transient active try metadata in run-state so live CLI commands can target an in-flight try before its final record is appended to try history. The metadata SHALL include active relay ID, active run ID, active try ID, active log path, and active start time, and SHALL be removed or cleared once the try becomes a completed historical record. Active metadata cleanup SHALL clear only the active metadata fields and SHALL preserve existing run-state fields used by laps/handoff/resume handling, including run ID, pinned lap ID, recorded laps, lap attempts, handoff state, and session ID.

#### Scenario: Active try metadata available during execution
- **WHEN** an executor is running for a try
- **THEN** run-state SHALL expose the active relay ID, active run ID, active try ID, active log path, and active start time

#### Scenario: Active try metadata removed after append
- **WHEN** the try is appended to try history
- **THEN** run-state SHALL no longer expose that try as active
- **AND** the cleanup SHALL NOT remove run-state fields unrelated to active-tail targeting

#### Scenario: Active metadata cleanup preserves laps state
- **WHEN** active try metadata is cleared while run-state contains recorded laps, lap attempts, handoff state, pinned lap ID, or session ID
- **THEN** those non-active fields SHALL remain unchanged

#### Scenario: Stale active metadata ignored
- **WHEN** active try metadata is stale, belongs to no unfinished relay/run, or points at a missing log path
- **THEN** consumers SHALL ignore the active metadata and fall back to completed try history with a warning where user-facing output is available
