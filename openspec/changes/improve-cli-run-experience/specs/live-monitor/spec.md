## ADDED Requirements

### Requirement: Live status line during try execution
The system SHALL display a periodically-updating status line during try execution showing agent runtime, dirty-file count, and last-activity time derived from the active try's log file mtime. The status line SHALL update every 5 seconds and SHALL be cleared when the try completes.

#### Scenario: Steady-state display
- **WHEN** an agent is executing and no concern heuristic has triggered
- **THEN** the status line SHALL show agent runtime (wall-clock elapsed), dirty-file count from `git status --porcelain`, and last activity derived from the active try's log file mtime
- **AND** the line SHALL NOT include network connection count or I/O byte counters

#### Scenario: Last activity uses log file mtime
- **WHEN** the status line refreshes during an active try
- **THEN** the `last activity` value SHALL reflect seconds elapsed since the most recent modification of the active try's log file, NOT since the most recent modification of any workspace file

### Requirement: Network warnings with smoothing
The system SHALL monitor TCP connection count and I/O throughput for the agent's process group on Linux using `/proc/` interfaces. These metrics SHALL NOT be displayed in steady state. They SHALL surface as warning text appended to the status line only when the smoothed heuristic triggers.

#### Scenario: No TCP connections for 30 seconds
- **WHEN** the agent process group has had zero TCP connections continuously for 30 seconds
- **THEN** the status line SHALL append a warning such as `No TCP… (30s)`

#### Scenario: Connected but no I/O for 30 seconds
- **WHEN** the agent process group has active TCP connections but cumulative I/O bytes have not advanced for 30 seconds
- **THEN** the status line SHALL append a warning such as `No network I/O… (30s)`

#### Scenario: Activity resumes
- **WHEN** TCP connections appear or I/O bytes advance after a warning was displayed
- **THEN** the warning SHALL be removed from the status line on the next refresh

#### Scenario: Non-Linux hosts
- **WHEN** the host is not Linux (e.g. macOS) or `/proc/` paths are unreadable at runtime
- **THEN** network monitoring SHALL be silently disabled; no network warnings SHALL appear; all other status line fields SHALL display normally

### Requirement: Process group tracking
The system SHALL set `Setpgid: true` on agent subprocesses and SHALL enumerate PIDs in the process group for connection and I/O monitoring.
