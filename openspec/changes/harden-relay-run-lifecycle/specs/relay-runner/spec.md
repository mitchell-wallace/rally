## ADDED Requirements

### Requirement: Lap-ID pinning
The system SHALL pin the assigned lap ID when a run starts and SHALL verify, when the run finalizes, that the lap recorded as completed matches the pinned ID. A mismatch SHALL fail the run with a distinct reason and SHALL NOT advance the work queue. The system SHALL record every lap completion attempt (with timestamp) on the try record so multi-lap consumption is traceable.

#### Scenario: Completed lap matches pinned lap
- **WHEN** a run finalizes and the lap recorded as completed equals the lap pinned at run start
- **THEN** the system SHALL accept the completion and advance the queue normally

#### Scenario: Wrong lap consumed
- **WHEN** a run finalizes recording a completed lap different from the pinned lap
- **THEN** the system SHALL fail the run with reason `wrong_lap_consumed`, SHALL NOT mark the pinned lap done, and SHALL NOT advance past it

#### Scenario: Multiple laps consumed in one run
- **WHEN** a run records more completed laps than the single lap it was assigned
- **THEN** the system SHALL fail the run with reason `multi_lap_consumed` and SHALL NOT advance the queue on the unassigned laps

#### Scenario: Attempted laps recorded
- **WHEN** a run records a lap completion attempt
- **THEN** the system SHALL record the lap ID and timestamp on the try record, not only the lap(s) accepted as done, so multi-lap consumption is traceable

### Requirement: Incomplete failure class
The system SHALL classify a try as "incomplete" rather than "failed" when file changes were produced (dirty working tree) but the agent neither finalized the lap (`laps done`) nor handed off (`laps handoff`). An incomplete try SHALL have its auto-commit suppressed, leaving changes uncommitted. The retry run SHALL inherit the uncommitted changes and SHALL receive prompt guidance: "The last run was incomplete. Check any current git changes, finish anything not done, verify correctness, commit when good, then run `laps done`." An incomplete try SHALL be retried but SHALL NOT count toward the pause/freeze resilience cascade.

#### Scenario: Agent produces file changes without finalizing
- **WHEN** a try produces file changes in the working tree but the agent does not call `laps done` or `laps handoff`
- **THEN** the system SHALL classify the try as incomplete, suppress auto-commit, retry the run with prompt guidance, and SHALL NOT call `PauseAgent` or `RecordHourlyFailure`

#### Scenario: No file changes and no finalization
- **WHEN** a try produces no file changes and the agent does not finalize
- **THEN** the system SHALL classify as a normal agent-class failure (retry-eligible, does not escalate)

### Requirement: Role-aware stall-recovery
The system SHALL NOT treat "files were committed" as sufficient to convert a stalled try (one killed by the liveness stall detector) into a success for a VERIFY run. A stalled VERIFY try SHALL remain a retry-eligible failure regardless of committed files (a VERIFY run may legitimately commit only a trivial fix, which is not evidence that verification occurred); it is retried or resumed rather than accepted. Implementation roles SHALL retain files-committed stall-recovery.

#### Scenario: Stalled VERIFY try is not auto-accepted
- **WHEN** a VERIFY try is killed for a stall and files were committed
- **THEN** the system SHALL NOT treat the try as success and SHALL keep it a retry-eligible failure

#### Scenario: Stalled implementation try with commits
- **WHEN** a non-VERIFY implementation try is killed for a stall and files were committed
- **THEN** the system SHALL retain the existing stall-recovery and may treat the committed work as success

### Requirement: Bounded prompt context
The system SHALL bound the recent-try context included in the assembled prompt by a configurable run count (default 5, under `[reliability]` config) and by per-summary and overall character budgets, truncating sensibly when a budget is exceeded.

#### Scenario: Verbose summaries truncated
- **WHEN** recent-try summaries exceed the per-summary or overall character budget
- **THEN** the system SHALL truncate them (head/tail) so the assembled prompt stays within the budget

#### Scenario: Run count configurable
- **WHEN** a run count is configured for recent-try context
- **THEN** the system SHALL include at most that many recent tries (defaulting to 5 when unset)

## MODIFIED Requirements

### Requirement: Failure detection
The system SHALL consider a try failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes). The system SHALL classify each failure as one of three classes:
- **infra-class**: rate limit, harness/launch error (e.g. `argument list too long`, `fork/exec`), API timeout / network stall, or liveness stall detection.
- **agent-class**: ordinary agent error or short no-op.
- **incomplete**: file changes were produced but the agent did not finalize the lap (`laps done` or `laps handoff`).

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

### Requirement: Retry logic
The system SHALL retry failed tries up to the configured budget within a single run. Retries do NOT count against the relay's iteration count. The previous try's summary is passed to the next attempt. Hourly retries of a paused agent SHALL allow up to 3 attempts so transient failures do not escalate the agent toward freeze.

#### Scenario: Retry with previous summary
- **WHEN** a try fails and retries remain
- **THEN** the system SHALL pass the previous try's summary as `PreviousSummary` in the next attempt's RunOptions

#### Scenario: Retry exhaustion triggers error cascade
- **WHEN** a run's tries fail their full budget with >1 infra-class failure
- **THEN** the system SHALL trigger the error resilience cascade for that agent type (NOT halt the relay)

#### Scenario: Hourly retry allows up to 3 attempts
- **WHEN** a paused agent type's hourly retry runs
- **THEN** the system SHALL allow up to 3 attempts before recording an hourly failure toward freeze

### Requirement: Error resilience cascade
The system SHALL implement a per-harness-model error resilience cascade driven by repeated infra-class failures (>1 within a run). After the threshold, the harness-model pair is paused for 1 hour. The system retries hourly. After continued infra-failures the pair is frozen, but the freeze SHALL NOT be terminal: a frozen pair SHALL decay to probation (a tentative-active state) after a bounded `FreezeDuration` (5h, hardcoded constant), and the decay SHALL be re-evaluated on resume/start rather than re-applied verbatim.

A probationary agent:
- Gets at most one run per probation cycle. The one-shot is enforced by `syncRecoverySignals`: the scheduler entry is unbenched for the run, then immediately re-benched so it cannot be re-selected.
- Gets `maxAttempts=3` (same as hourly retries).
- On success or incomplete: promoted to active (incomplete is a progress issue, not a model-availability issue).
- On failure (agent or infra): re-frozen with a fresh timestamp, restarting the decay window.
- The probation event (`event_type: "probation"`) is persisted exactly once when the transition is first observed.

If all harness-model pairs are paused, the system waits for the next hourly check. If all pairs are frozen, the relay ends as a failure for the current pass but the freeze remains subject to decay for subsequent starts.

#### Scenario: Agent paused after repeated infra-failure
- **WHEN** a harness-model pair's tries within a run have >1 infra-class failure
- **THEN** the system SHALL mark that pair as paused, skip it in the agent mix, and schedule an hourly retry

#### Scenario: Agent unfreezes after hourly retry succeeds
- **WHEN** a paused pair's hourly retry try succeeds
- **THEN** the system SHALL restore the pair to active status in the mix

#### Scenario: Frozen agent decays to probation
- **WHEN** a harness-model pair has been frozen for longer than `FreezeDuration`
- **THEN** `getState` SHALL report it as probation, making it eligible for a single tentative run

#### Scenario: Probation run succeeds, promotes to active
- **WHEN** a probationary pair's run succeeds (not failed)
- **THEN** the system SHALL promote the pair to active status

#### Scenario: Probation run incomplete, moves to active
- **WHEN** a probationary pair's run is classified as incomplete
- **THEN** the system SHALL promote the pair to active (incomplete is progress, not availability)

#### Scenario: Probation run fails, re-freezes
- **WHEN** a probationary pair's run fails (agent-class or infra-class)
- **THEN** the system SHALL re-freeze the pair with a fresh timestamp, restarting the decay window

#### Scenario: Probation one-shot enforced
- **WHEN** a probationary pair's scheduler entry is unbenched for a run
- **THEN** `syncRecoverySignals` SHALL immediately re-bench the entry so it cannot be selected again until the run resolves

#### Scenario: Freeze re-evaluated on resume
- **WHEN** a relay resumes or a non-`--new` relay starts and a pair's freeze has decayed
- **THEN** the system SHALL re-evaluate freeze state via `getState` (a pure read) rather than re-apply the stored frozen state verbatim

#### Scenario: All agents frozen ends the current pass
- **WHEN** all harness-model pairs in the mix are currently frozen and none have decayed to probation
- **THEN** the system SHALL end the relay pass as a failure, leaving freezes subject to later decay

#### Scenario: System waits when all agents paused
- **WHEN** all available harness-model pairs are paused (but not frozen)
- **THEN** the system SHALL wait until the next pair's hourly retry check
