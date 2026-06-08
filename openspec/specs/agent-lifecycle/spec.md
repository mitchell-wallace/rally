# agent-lifecycle Specification

## Purpose
TBD - created by archiving change agent-lifecycle. Update Purpose after archive.
## Requirements
### Requirement: Graceful subprocess shutdown
The system SHALL terminate agent subprocesses gracefully on cancellation. When an
attempt's context is cancelled, the system SHALL first send SIGINT to the subprocess
process group and SHALL escalate to SIGKILL only if the subprocess has not exited
within a bounded delay (`WaitDelay`). This behavior SHALL apply to every executor via
the shared process-group setup. The stall-detector kill path SHALL use the same
graceful-interrupt signal (SIGINT) as the cancel path.

#### Scenario: Cancel sends SIGINT to the process group first
- **WHEN** an attempt context is cancelled
- **THEN** the system SHALL send SIGINT to the subprocess **process group** (not only the leader process) before any SIGKILL

#### Scenario: Escalates to SIGKILL after the grace window
- **WHEN** a subprocess has not exited within `WaitDelay` after SIGINT
- **THEN** the system SHALL send SIGKILL to terminate it

#### Scenario: Stall kill uses the same signal
- **WHEN** the stall detector terminates a stalled agent's process group
- **THEN** it SHALL send SIGINT first and escalate to SIGKILL after the drain, matching the cancel path

### Requirement: Responsive stop and quit shortcuts
The system SHALL act on the shutdown shortcuts according to their stated timing.
"quit now" (Ctrl+C) SHALL cancel the currently running attempt immediately via graceful
shutdown and then abort the relay, without waiting for the attempt to complete.
"graceful stop" (Ctrl+X) SHALL allow the current attempt to finish and then stop the
relay. While a cancel is draining, the system SHALL remain responsive to a further
"quit now" press and SHALL escalate to immediate termination.

#### Scenario: Quit now cancels the running attempt immediately
- **WHEN** the operator triggers "quit now" while an attempt is running
- **THEN** the system SHALL cancel the attempt via graceful shutdown and abort the relay without waiting for the attempt to finish

#### Scenario: Quit now ends a stalled agent promptly
- **WHEN** the operator triggers "quit now" while the agent is stalled
- **THEN** the system SHALL terminate the attempt within the graceful-shutdown window rather than waiting for the stall threshold

#### Scenario: Graceful stop finishes the current attempt
- **WHEN** the operator triggers "graceful stop" while an attempt is running
- **THEN** the system SHALL let the current attempt finish and then stop the relay

### Requirement: Pause-now and honest session resume
The system SHALL treat pause as an immediate action: it SHALL cancel the current
attempt (via graceful shutdown), capture the harness session ID, and store it in
run-state. On resume — and on any retry that has a tracked session ID — when the harness
declares resume support, the system SHALL pass the harness's resume flag to continue the
session rather than starting a fresh try. A harness whose `ResumeSupported()` returns
true SHALL actually pass its resume flag when a session ID is set; a harness that cannot
resume SHALL report `ResumeSupported()` as false. When no session ID is available, or the
run was explicitly reset (fresh restart), the system SHALL start a fresh try.

#### Scenario: Pause captures the session
- **WHEN** the operator pauses a running attempt
- **THEN** the system SHALL cancel the attempt via graceful shutdown and store the harness session ID in run-state

#### Scenario: Resume reuses the session when supported
- **WHEN** a paused run resumes, the harness declares resume support, and a session ID exists
- **THEN** the system SHALL pass the harness's resume flag instead of starting a fresh try

#### Scenario: Resume support implies the flag is passed
- **WHEN** a harness reports `ResumeSupported()` as true and a session ID is set
- **THEN** the built subprocess command SHALL include that harness's resume flag

#### Scenario: Resume falls back when unsupported
- **WHEN** a paused run resumes but the harness does not support resume or no session ID exists
- **THEN** the system SHALL start a fresh try

#### Scenario: Fresh restart starts fresh
- **WHEN** the retry strategy is a fresh restart or the run was explicitly skipped
- **THEN** the system SHALL clear any tracked session and start a fresh try

### Requirement: Shortcut label clarity
The system SHALL label the shutdown shortcuts to convey timing: the Ctrl+X shortcut
SHALL be labeled "graceful stop" (stops after the current try) and the Ctrl+C shortcut
SHALL be labeled "quit now" (immediate, via graceful shutdown). The labels SHALL render
across all width tiers, abbreviating only as width requires.

#### Scenario: Labels state timing
- **WHEN** the shortcut hint is rendered at a width that fits full labels
- **THEN** Ctrl+X SHALL read "graceful stop" and Ctrl+C SHALL read "quit now"

### Requirement: Single-runner lane warning
At relay start the system SHALL warn when a routing lane has only one runner entry, so
the operator knows a single failing harness can stall that lane with no fallback to
rotate to.

#### Scenario: Single-entry lane warns
- **WHEN** a relay starts and a lane has exactly one runner entry
- **THEN** the system SHALL emit a warning that the lane has no fallback runner

#### Scenario: Multi-entry lane does not warn
- **WHEN** a relay starts and every lane has more than one runner entry
- **THEN** the system SHALL NOT emit the single-runner-lane warning

### Requirement: VERIFY role default boundary
The default VERIFY role SHALL be reporting-focused: it MAY apply trivial,
clearly-correct fixes (a few lines), but substantial fixes, unclear follow-up, or
work deserving its own implementation pass SHALL become a new head lap rather than
being done inline. The generic VERIFY role document SHALL remain OpenSpec-agnostic;
OpenSpec-specific behavior such as marking off `tasks.md` SHALL be injected per-lap by
the `prepare-laps` skill only when a lap has a related OpenSpec change, and SHALL NOT be
baked into rally core or the default role document.

#### Scenario: Default VERIFY routes substantial gaps to a head lap
- **WHEN** the default VERIFY role finds a substantial gap (beyond a trivial fix)
- **THEN** it SHALL record a new head lap rather than doing the work inline

#### Scenario: Default VERIFY may apply a trivial fix
- **WHEN** the default VERIFY role finds an issue fixable by a few clearly-correct lines
- **THEN** it MAY apply that fix directly rather than creating a head lap

#### Scenario: tasks.md updating is OpenSpec-scoped
- **WHEN** a lap has no related OpenSpec change
- **THEN** the generic VERIFY role SHALL NOT receive any "mark off tasks.md" instruction

