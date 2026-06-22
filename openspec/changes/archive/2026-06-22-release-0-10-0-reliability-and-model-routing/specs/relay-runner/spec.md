## MODIFIED Requirements

### Requirement: Lap-ID pinning
The system SHALL pin the assigned lap ID when a run starts and SHALL verify, when the run finalizes, that the lap recorded as completed matches the pinned ID. A mismatch SHALL be recorded with a distinct reason and SHALL NOT advance the work queue on unassigned laps. The mismatch SHALL be treated as a warning-level handoff/route-fallback signal rather than an operator-grade hard failure or retryable cleanup path. The system SHALL record every lap completion attempt (with timestamp) on the try record so multi-lap consumption is traceable.

#### Scenario: Completed lap matches pinned lap
- **WHEN** a run finalizes and the lap recorded as completed equals the lap pinned at run start
- **THEN** the system SHALL accept the completion and advance the queue normally

#### Scenario: Wrong lap consumed
- **WHEN** a run finalizes recording a completed lap different from the pinned lap
- **THEN** the system SHALL record reason `wrong_lap_consumed`, SHALL NOT mark the pinned lap done, SHALL NOT advance past it, and SHALL route to the next scheduler candidate without emitting an operator-grade failure

#### Scenario: Multiple laps consumed in one run
- **WHEN** a run records more completed laps than the single lap it was assigned
- **THEN** the system SHALL record reason `multi_lap_consumed`, SHALL NOT advance the queue on the unassigned laps, and SHALL route to the next scheduler candidate without emitting an operator-grade failure

#### Scenario: Attempted laps recorded
- **WHEN** a run records a lap completion attempt
- **THEN** the system SHALL record the lap ID and timestamp on the try record, not only the lap(s) accepted as done, so multi-lap consumption is traceable

### Requirement: Failure detection
The system SHALL consider a try failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes), unless an explicit operator-cancelled outcome applies. Operator cancellation SHALL take precedence over normal executor success or error handling after a cancellation request has been issued. The system SHALL assign each failure a stable `FailureCategory` (see "Failure taxonomy and evidence") and SHALL map that category onto one of three resilience classes:
- **infra-class**: short rate limit, provider overload, harness/launch error (e.g. `argument list too long`, `fork/exec`), transient infrastructure error (`transient_infra`: API timeout, network/connection/TLS failure, non-overload 5xx), or liveness stall detection.
- **agent-class**: ordinary agent error or short no-op.
- **incomplete**: file changes were produced but the agent did not finalize the lap (`laps done` or `laps handoff`).

Long usage/quota exhaustion (`usage_limit`), invalid-model/config (`invalid_model`), and authentication/proxy (`auth_or_proxy`) failures SHALL NOT be classified infra-class; they are handled by benching/routing and SHALL NOT increment the pause/freeze counter. A try whose outcome is `run_timeout`, `handoff_timeout`, or `cancelled` SHALL likewise NOT be classified infra-class and SHALL NOT increment the pause/freeze counter.

Only repeated infra-class failures SHALL drive the per-agent-type resilience cascade; a single infra-class failure retries without escalation. Agent-class and incomplete failures SHALL fail the try and be retry-eligible but SHALL NOT increment the pause/freeze counter. Rate-limit flags SHALL be tracked per harness-model pair (`harness:model`) using a `ResilienceKey` type, not per harness alone, so that an opencode runner using multiple providers does not freeze wholesale when only one provider hits its rate limit. All resilience methods (`getState`, `PauseAgent`, `RecordHourlyFailure`, `FreezeAgent`, `UnpauseAgent`, `SelectActiveAgent`) and their callers in `runner.go` and `route_runtime.go` SHALL use the `ResilienceKey`.

#### Scenario: Short no-op try detected as failure
- **WHEN** a try produces no file changes and completes in under 3 minutes
- **THEN** the system SHALL treat it as a failed, retry-eligible try, classified agent-class, and SHALL NOT count it toward pause/freeze

#### Scenario: Agent error exit detected as failure
- **WHEN** the agent subprocess exits with a non-zero exit code matching an agent-class pattern
- **THEN** the system SHALL treat it as a failed, retry-eligible try and SHALL NOT count it toward pause/freeze

#### Scenario: Single infra failure does not pause
- **WHEN** a run has exactly one attempt classified as infra-class and the remaining attempts (if any) are agent-class or incomplete
- **THEN** the system SHALL NOT call `PauseAgent` and SHALL NOT increment the freeze counter

#### Scenario: Repeated infra failures drive the cascade
- **WHEN** >1 attempt within a run is classified as infra-class
- **THEN** the system SHALL call `PauseAgent` and count it toward the resilience cascade

#### Scenario: Incomplete try does not escalate
- **WHEN** a try produces file changes but the agent did not finalize
- **THEN** the system SHALL classify it as incomplete, suppress auto-commit, retry the run, and SHALL NOT count it toward pause/freeze

#### Scenario: Usage-limit failure is not infra-class
- **WHEN** a try fails with a `usage_limit`, `invalid_model`, or `auth_or_proxy` category
- **THEN** the system SHALL NOT classify it infra-class and SHALL NOT increment the pause/freeze counter

#### Scenario: Lifecycle outcomes are not infra-class
- **WHEN** a try resolves with a `run_timeout`, `handoff_timeout`, or `cancelled` outcome
- **THEN** the system SHALL NOT classify it infra-class, SHALL NOT increment the pause/freeze counter, and SHALL NOT treat it as a usage-limit or harness failure

#### Scenario: opencode subscription-provider limit benches the quota scope
- **WHEN** an opencode runner on a subscription provider (e.g. `zai-coding-plan`, `opencode-go`) fails because that provider's 5h/weekly/monthly usage limit is reached
- **THEN** the system SHALL classify the failure `usage_limit` (not `agent_error`) and SHALL bench the runner's quota scope until the parsed reset, even when the limit was surfaced from opencode's server log after an internal-retry stall rather than from the JSON error event

#### Scenario: Operator cancellation overrides failure detection
- **WHEN** a try exits after Ctrl+S skip, Ctrl+X graceful stop, or quit-now cancelled the attempt
- **THEN** the system SHALL record the try as `cancelled` rather than successful or failed and SHALL NOT classify it into a `FailureCategory`

## ADDED Requirements

### Requirement: Operator cancellation outcome
The system SHALL represent operator-driven attempt termination as a first-class `cancelled` `TryOutcome` with a stable source value. Supported sources SHALL include `skip`, `graceful_stop`, and `quit_now`. A cancelled try SHALL be persisted for audit with `TryRecord.Outcome = "cancelled"` and `TryRecord.CancellationSource` set to the source. Legacy `TryRecord.Completed` SHALL be false for cancelled records, but downstream consumers SHALL use `Outcome` to distinguish cancellation from failure. A cancelled try SHALL NOT be classified as a failure, retried, counted toward pause/freeze resilience, counted as a failed run in summaries, shown as a harness error, or converted into a post-loop `incomplete_finalization` failure.

#### Scenario: Skip records cancelled outcome
- **WHEN** the operator presses Ctrl+S and the active attempt exits through the cancellation path
- **THEN** the system SHALL persist the try with outcome `cancelled` and source `skip`
- **AND** the skipped run SHALL advance according to existing skip routing semantics
- **AND** the try SHALL NOT be retried or classified as `harness error`

#### Scenario: Quit now records cancelled outcome
- **WHEN** the operator triggers quit-now and the active attempt exits through the cancellation path
- **THEN** the system SHALL persist the try with outcome `cancelled` and source `quit_now`
- **AND** the relay SHALL abort after recording the cancellation
- **AND** the try SHALL NOT increment retry, pause, freeze, or failure counters

#### Scenario: Graceful stop records cancelled outcome
- **WHEN** the operator triggers Ctrl+X graceful stop while an attempt is running
- **THEN** the system SHALL persist the try with outcome `cancelled` and source `graceful_stop`
- **AND** the relay SHALL stop without starting a new run
- **AND** the try SHALL NOT be rendered or recorded as a failed harness error

#### Scenario: Cancelled run excluded from failure tally
- **WHEN** a relay summary or run tally includes a cancelled try
- **THEN** the system SHALL NOT count that cancelled try as a failed run

#### Scenario: Cancelled laps-backed run suppresses unfinalized failure
- **WHEN** a laps-backed attempt is cancelled before `laps done` or `laps handoff`
- **THEN** the system SHALL NOT synthesize an `incomplete_finalization` failure capture for that cancelled attempt

### Requirement: Run-oriented relay header context
The system SHALL label non-laps relay progress with run semantics and SHALL include role, harness, and model context in the live run header. Non-laps counters SHALL render as `run: X/Y`; lap-backed displays MAY retain lap-specific labels where they represent lap bookkeeping.

#### Scenario: Non-laps header labels runs
- **WHEN** a non-laps relay run starts
- **THEN** the live header SHALL display `run: X/Y` rather than a bare `[X/Y]` counter

#### Scenario: Header includes role and model context
- **WHEN** a run has an assigned role and resolved runner model
- **THEN** the live header SHALL include the role label, harness, and model on the run header line

### Requirement: Active try metadata for live tailing
The system SHALL make the active try targetable while it is still running by writing active try metadata before executor invocation and clearing it after the try is recorded. The metadata SHALL include enough information for `rally tail --try 0` to identify the active try log, including active relay ID, active run ID, active try ID, active log path, and active start time.

#### Scenario: Active metadata written before executor
- **WHEN** a try is about to invoke an executor
- **THEN** the system SHALL persist active try metadata containing the active relay ID, active run ID, active try ID, active log path, and active start time before the executor starts

#### Scenario: Active metadata cleared after persistence
- **WHEN** the try has been appended to durable try history
- **THEN** the system SHALL clear the active try metadata so future default tailing can fall back to completed try history
