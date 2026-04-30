## MODIFIED Requirements

### Requirement: Live monitor reports try-progress signals
The system SHALL display a periodically-updating status line during try execution showing agent runtime, dirty-file count, last-activity time, and a token estimate. The last-activity value SHALL be derived from the active try's log file mtime (not from a workspace-file mtime scan). Connection count and I/O byte counters SHALL be displayed only when the concern heuristic flags a possible stall, not as steady-state fields.

#### Scenario: Steady-state live monitor display
- **WHEN** an agent is executing and the concern heuristic has not triggered
- **THEN** the live monitor line SHALL include agent runtime, dirty-file count from `git status --porcelain`, last activity derived from log file mtime, and the token estimate
- **AND** the line SHALL NOT include connection count or I/O byte counters

#### Scenario: Concern heuristic triggers
- **WHEN** the active try's log file mtime has not advanced for more than 30 seconds AND the agent process group has zero active TCP connections (Linux only)
- **THEN** the live monitor line SHALL additionally render `🔗 N conns` and `📡 N MB I/O` to surface the diagnostic data

#### Scenario: Last-activity uses log mtime
- **WHEN** the live monitor refreshes during an active try
- **THEN** the `last activity` value SHALL reflect seconds elapsed since the most recent modification of the active try's log file, not since the most recent modification of any workspace file

#### Scenario: Non-Linux hosts skip connection/IO entirely
- **WHEN** the host is not Linux (e.g. macOS) and the concern heuristic would otherwise trigger
- **THEN** the connection-count and I/O fields SHALL be omitted from the rendered line; the rest of the concern indicators (e.g. `last activity` value) SHALL still appear
