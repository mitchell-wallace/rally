## ADDED Requirements

### Requirement: Relay exposes an interrupt control channel
The system SHALL maintain a control channel for an active relay process consisting of a PID file and a Unix-domain socket inside the workspace's rally data directory (`.rally/relay.pid` and `.rally/relay.sock`). The relay SHALL bind the socket on startup, accept short text commands from connecting clients, and apply each command at the next safe iteration boundary.

#### Scenario: Relay startup creates control files
- **WHEN** `rally relay` starts and no other relay is running in the same workspace
- **THEN** the system SHALL write `.rally/relay.pid` containing the relay PID and SHALL bind a UDS at `.rally/relay.sock`

#### Scenario: Stale control files from crashed relay
- **WHEN** `rally relay` starts and `.rally/relay.pid` exists but its recorded PID is not running (or is not a `rally` process)
- **THEN** the system SHALL remove the stale PID file and socket and proceed with normal startup

#### Scenario: Concurrent relay attempted
- **WHEN** `rally relay` starts and `.rally/relay.pid` references a live `rally` process
- **THEN** startup SHALL exit non-zero with the message `another rally relay is already running in this workspace`

### Requirement: `rally skip` advances to the next iteration
The system SHALL provide a `rally skip` subcommand that requests a graceful stop of the current try and advance to the next iteration of the relay. The subcommand SHALL connect to the active relay's UDS, send the skip command, print the action taken, and exit immediately without waiting for acknowledgement. The relay SHALL apply the skip at the next safe boundary (between try invocations or at the next monitor tick).

#### Scenario: Skip current try
- **WHEN** the operator invokes `rally skip` while a relay is running and currently inside a try
- **THEN** the subcommand SHALL print a message such as `skip requested — relay will advance after current try` and SHALL exit zero
- **AND** the relay SHALL terminate the current try gracefully and proceed to the next iteration; the retry budget SHALL not be consumed by this skip

#### Scenario: No relay running
- **WHEN** the operator invokes `rally skip` in a workspace with no active relay (no PID file or stale PID)
- **THEN** the subcommand SHALL exit non-zero with the message `no active rally relay in this workspace`

### Requirement: `rally stop` ends after current try
The system SHALL provide a `rally stop` subcommand that requests a graceful halt of the relay after the currently-running try completes. No further iterations SHALL be started. The subcommand SHALL print the action taken and exit immediately without waiting for the relay to finish.

#### Scenario: Stop after current try
- **WHEN** the operator invokes `rally stop` while a relay is running with iterations remaining
- **THEN** the subcommand SHALL print a message such as `stop requested — relay will halt after current try` and SHALL exit zero
- **AND** the relay SHALL complete the current try normally, write its progress entry, then exit cleanly without starting the next iteration

#### Scenario: Stop between tries
- **WHEN** the operator invokes `rally stop` while the relay is between tries (no try active)
- **THEN** the relay SHALL exit cleanly at the next iteration boundary without starting another try

### Requirement: Skip and stop are unsupported on Windows
The system SHALL detect the host OS at runtime. On Windows, `rally skip` and `rally stop` SHALL exit non-zero with a message indicating the feature is not supported on this platform. The relay startup path SHALL NOT bind a UDS on Windows; absence of the socket SHALL not block normal relay execution.

#### Scenario: Skip/stop on Windows
- **WHEN** the operator invokes `rally skip` or `rally stop` on a Windows host
- **THEN** the subcommand SHALL exit non-zero with a message such as `rally skip/stop is not supported on Windows`

#### Scenario: Relay startup on Windows
- **WHEN** `rally relay` starts on a Windows host
- **THEN** startup SHALL proceed without binding a UDS or writing a PID file; the relay SHALL otherwise function normally
