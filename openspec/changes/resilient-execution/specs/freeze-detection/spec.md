## ADDED Requirements

### Requirement: Stalled-try detection from monitoring signals
The system SHALL classify an active try as frozen when ALL of the following are true: (a) the active log file mtime has not advanced for `[reliability].freeze_threshold_secs` seconds (default 180), (b) the agent process group has zero active TCP connections (Linux only; on macOS this clause SHALL be treated as satisfied by default), (c) IO byte counters have not advanced for the same window. The detection runs on the v0.3.0 live-monitor's tick cadence (no separate poll loop).

#### Scenario: All freeze signals trip
- **WHEN** the active try's log mtime is 200 seconds stale, conn count is 0, and IO byte counter is unchanged for 200 seconds (with `freeze_threshold_secs = 180`)
- **THEN** the freeze detector SHALL flag the try frozen

#### Scenario: Log silent but IO active
- **WHEN** the log mtime has not advanced but IO byte counters are still increasing
- **THEN** the freeze detector SHALL NOT flag the try frozen (some progress signal is present)

#### Scenario: Threshold below 180s via config
- **WHEN** `[reliability].freeze_threshold_secs = 90` is set in config
- **THEN** the threshold for log/IO silence SHALL be 90 seconds for all evaluations

### Requirement: Graceful kill on freeze
The system SHALL graceful-kill a frozen try by sending SIGTERM to the agent process group, waiting up to 5 seconds for shutdown, then sending SIGKILL if the process group has not exited. The killed try SHALL be counted as a retry-eligible failure and SHALL emit `OnAgentFailed(currentEntry, "freeze")` to the v0.6.0 quota-scheduler.

#### Scenario: Frozen try graceful-kill
- **WHEN** the freeze detector flags a try frozen
- **THEN** the relay-runner SHALL send SIGTERM to the agent process group, wait up to 5 seconds, send SIGKILL if needed, and treat the resulting failure as retry-eligible via the resume-aware retry path

#### Scenario: Scheduler informed of freeze
- **WHEN** a try is killed for freeze
- **THEN** the relay-runner SHALL invoke `OnAgentFailed(entry, "freeze")` on the scheduler, marking the entry frozen for the current cycle

### Requirement: Freeze detection respects platform availability
The system SHALL gracefully degrade on platforms where one or more freeze signals are unavailable. Linux platforms SHALL use all three signals (log-mtime, conn count, IO bytes). macOS SHALL use log-mtime alone (treating conn-count and IO-byte clauses as satisfied by default). Windows SHALL skip freeze detection entirely (log-mtime alone is too noisy without the corroborating signals).

#### Scenario: Linux uses all three signals
- **WHEN** rally runs on Linux
- **THEN** freeze detection requires all three signals to trip; partial silence (e.g. log silent but conns nonzero) SHALL NOT flag frozen

#### Scenario: macOS uses log-mtime alone
- **WHEN** rally runs on macOS
- **THEN** freeze detection requires only log-mtime to trip the threshold; conn-count and IO-byte clauses SHALL be treated as satisfied by default

#### Scenario: Windows skips freeze detection
- **WHEN** rally runs on Windows
- **THEN** freeze detection SHALL NOT trip regardless of log activity; freezes on Windows are surfaced only via retry-budget exhaustion and explicit operator interruption
