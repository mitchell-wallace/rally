## ADDED Requirements

### Requirement: Lap-ID pinning
The system SHALL pin the assigned lap ID(s) when a run starts and SHALL verify, when the run finalizes, that the lap(s) recorded as completed match the pinned set. A mismatch SHALL fail the run with a distinct reason and SHALL NOT advance the work queue.

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
- **WHEN** a run records a lap completion
- **THEN** the system SHALL record the lap ID(s) attempted (with timestamp), not only the lap(s) accepted as done, so multi-lap consumption is traceable

### Requirement: Completion file-change cross-check
When a lap declares expected file paths, the system SHALL verify those files were modified since the run started before accepting a `laps done`. When a lap declares no expected files, the check SHALL be skipped and behavior unchanged.

#### Scenario: Expected files modified
- **WHEN** a lap declares expected files and those files changed since run start
- **THEN** the system SHALL accept the completion

#### Scenario: Expected files untouched
- **WHEN** a lap declares expected files and none of them changed since run start
- **THEN** the system SHALL reject or warn on the completion rather than silently accept it

#### Scenario: No expected files declared
- **WHEN** a lap declares no expected file paths
- **THEN** the system SHALL skip the cross-check and finalize as it would without this feature

### Requirement: Role-aware freeze-recovery
The system SHALL NOT treat "files were committed" as sufficient to convert a frozen try into a success for a VERIFY run. A frozen VERIFY try SHALL require a verification verdict artifact to be treated as success. Implementation roles SHALL retain files-committed freeze-recovery.

#### Scenario: Frozen VERIFY try without a verdict
- **WHEN** a VERIFY try is killed for freeze and files were committed but no verification verdict artifact is present
- **THEN** the system SHALL NOT treat the try as success and SHALL keep it a retry-eligible failure

#### Scenario: Frozen implementation try with commits
- **WHEN** a non-VERIFY implementation try is killed for freeze and files were committed
- **THEN** the system SHALL retain the existing freeze-recovery and may treat the committed work as success

### Requirement: Bounded prompt context
The system SHALL bound the recent-try context included in the assembled prompt by a configurable run count (default approximately 5) and by per-summary and overall character budgets, truncating sensibly when a budget is exceeded.

#### Scenario: Verbose summaries truncated
- **WHEN** recent-try summaries exceed the per-summary or overall character budget
- **THEN** the system SHALL truncate them (e.g. head/tail) so the assembled prompt stays within the budget

#### Scenario: Run count configurable
- **WHEN** a run count is configured for recent-try context
- **THEN** the system SHALL include at most that many recent tries (defaulting to approximately 5 when unset)

## MODIFIED Requirements

### Requirement: Failure detection
The system SHALL consider a try failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes). The system SHALL classify each failure as either infra-class (rate limit, harness/launch error such as `argument list too long`, or API timeout / network stall) or agent-class (ordinary agent error or short no-op). Only infra-class failures SHALL drive the per-agent-type resilience cascade; agent-class failures SHALL fail the try and be retry-eligible but SHALL NOT increment the pause/freeze counter.

#### Scenario: Short no-op try detected as failure
- **WHEN** a try produces no file changes and completes in under 3 minutes
- **THEN** the system SHALL treat it as a failed, retry-eligible try, classified agent-class, and SHALL NOT count it toward pause/freeze

#### Scenario: Agent error exit detected as failure
- **WHEN** the agent subprocess exits with a non-zero exit code matching an agent-class pattern
- **THEN** the system SHALL treat it as a failed, retry-eligible try and SHALL NOT count it toward pause/freeze

#### Scenario: Infra failure drives the cascade
- **WHEN** a try fails with an infra-class pattern (rate limit, harness/launch error, or API timeout)
- **THEN** the system SHALL treat it as failed and SHALL count it toward the per-agent-type pause/freeze cascade

### Requirement: Retry logic
The system SHALL retry failed tries up to the configured budget within a single run. Retries do NOT count against the relay's iteration count. The previous try's summary is passed to the next attempt. Hourly retries of a paused agent SHALL allow more than one attempt so a single transient failure does not escalate the agent toward freeze.

#### Scenario: Retry with previous summary
- **WHEN** a try fails and retries remain
- **THEN** the system SHALL pass the previous try's summary as `PreviousSummary` in the next attempt's RunOptions

#### Scenario: Retry exhaustion triggers error cascade
- **WHEN** a run's tries fail their full budget consecutively with infra-class failures
- **THEN** the system SHALL trigger the error resilience cascade for that agent type (NOT halt the relay)

#### Scenario: Hourly retry allows more than one attempt
- **WHEN** a paused agent type's hourly retry runs
- **THEN** the system SHALL allow more than one attempt before recording an hourly failure toward freeze

### Requirement: Error resilience cascade
The system SHALL implement a per-agent-type error resilience cascade driven by infra-class failures. After the configured consecutive infra-failure threshold within a run, the agent type is paused for 1 hour. The system retries hourly. After continued infra-failures the agent type is frozen, but the freeze SHALL NOT be terminal: a frozen agent type SHALL decay back to active (or probation) after a bounded `FreezeDuration`, and the decay SHALL be re-evaluated on resume/start rather than re-applied verbatim. If all agent types are paused, the system waits for the next hourly check. If all agent types are frozen, the relay ends as a failure for the current pass but the freeze remains subject to decay for subsequent starts.

#### Scenario: Agent paused after infra-failure exhaustion
- **WHEN** an agent type's tries fail the consecutive infra-failure threshold within a run
- **THEN** the system SHALL mark that agent type as paused, skip it in the agent mix, and schedule an hourly retry

#### Scenario: Agent unfreezes after hourly retry succeeds
- **WHEN** a paused agent type's hourly retry try succeeds
- **THEN** the system SHALL restore the agent type to active status in the mix

#### Scenario: Frozen agent decays back to active
- **WHEN** an agent type has been frozen for longer than `FreezeDuration`
- **THEN** `getState` SHALL report it active (or probation), making it eligible to run again without a fresh relay

#### Scenario: Freeze re-evaluated on resume
- **WHEN** a relay resumes or a non-`--new` relay starts and an agent type's freeze has decayed
- **THEN** the system SHALL re-evaluate freeze state rather than re-apply the stored frozen state verbatim

#### Scenario: All agents frozen ends the current pass
- **WHEN** all agent types in the mix are currently frozen and none have decayed
- **THEN** the system SHALL end the relay pass as a failure, leaving freezes subject to later decay

#### Scenario: System waits when all agents paused
- **WHEN** all available agent types are paused (but not frozen)
- **THEN** the system SHALL wait until the next agent's hourly retry check
