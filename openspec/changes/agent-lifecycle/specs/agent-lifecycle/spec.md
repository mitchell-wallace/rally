## ADDED Requirements

### Requirement: Graceful subprocess shutdown
The system SHALL terminate agent subprocesses gracefully on cancellation. When an
attempt's context is cancelled, the system SHALL first send SIGINT to the subprocess
process group and SHALL escalate to SIGKILL only if the subprocess has not exited
within a bounded delay (`WaitDelay`). This behavior SHALL apply to every executor via
the shared process-group setup.

#### Scenario: Cancel sends SIGINT first
- **WHEN** an attempt context is cancelled
- **THEN** the system SHALL send SIGINT to the subprocess process group before any SIGKILL

#### Scenario: Escalates to SIGKILL after the grace window
- **WHEN** a subprocess has not exited within `WaitDelay` after SIGINT
- **THEN** the system SHALL send SIGKILL to terminate it

### Requirement: Pause-now and session resume
The system SHALL treat pause as an immediate action: it SHALL cancel the current
attempt (via graceful shutdown), capture the harness session ID, and store it in
run-state. On resume, when the harness declares resume support and a session ID is
available, the system SHALL pass `--resume <session-id>` to continue the session rather
than starting a fresh try. When the harness does not support resume or no session ID is
available, the system SHALL start a fresh try.

#### Scenario: Pause captures the session
- **WHEN** the operator pauses a running attempt
- **THEN** the system SHALL cancel the attempt via graceful shutdown and store the harness session ID in run-state

#### Scenario: Resume reuses the session when supported
- **WHEN** a paused run resumes, the harness declares resume support, and a session ID exists
- **THEN** the system SHALL pass `--resume <session-id>` instead of starting a fresh try

#### Scenario: Resume falls back when unsupported
- **WHEN** a paused run resumes but the harness does not support resume or no session ID exists
- **THEN** the system SHALL start a fresh try

#### Scenario: Retry with meaningful progress resumes
- **WHEN** a try that ran longer than the progress threshold (time or file-change count, excluding `.rally/` and log files) fails or errors and the run was not explicitly skipped
- **THEN** the system SHALL attempt to resume the session rather than starting fresh

#### Scenario: Skipped run starts fresh
- **WHEN** a run was explicitly skipped
- **THEN** the system SHALL start a fresh try and SHALL NOT resume

### Requirement: Shortcut label clarity
The system SHALL label the shutdown shortcuts to convey timing: the Ctrl+X shortcut
SHALL be labeled "graceful stop" (stops after the current try) and the Ctrl+C shortcut
SHALL be labeled "quit now" (immediate, via graceful shutdown).

#### Scenario: Labels state timing
- **WHEN** the shortcut hint is rendered
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
