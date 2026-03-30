## ADDED Requirements

### Requirement: Relay lifecycle
The system SHALL manage relays as a campaign of N sequential runs with a configured agent mix. A relay tracks: relay ID, target iterations, completed iterations, agent mix, start time, and end time.

#### Scenario: New relay created
- **WHEN** a user starts a relay with a prompt, iteration count, and agent mix
- **THEN** the system SHALL create a relay record and begin executing runs sequentially

#### Scenario: Relay resumes after interruption
- **WHEN** rally starts and an incomplete relay exists in state
- **THEN** the system SHALL offer to resume from the next uncompleted iteration

### Requirement: Agent mix cycling
The system SHALL cycle through agents in a deterministic rotation based on the configured agent mix weights. For example, `cc:2 cx:1` produces the cycle `[cc, cc, cx]`, repeated across runs.

#### Scenario: Deterministic agent selection
- **WHEN** a run is about to start within a relay
- **THEN** the system SHALL select the agent using `cycle[(runIndex) % len(cycle)]`

#### Scenario: Agent mix parsed from spec
- **WHEN** agent specs like `"cc:2 cx:1"` are provided
- **THEN** the system SHALL parse them into weighted cycles preserving declaration order

### Requirement: Run execution
The system SHALL execute each run by: writing `.rally/current_task.md`, building a prompt, invoking the selected agent's Executor, recording the RunResult, and auto-committing workspace changes.

#### Scenario: Context file written before run
- **WHEN** a run is about to execute
- **THEN** the system SHALL write the current task context to `.rally/current_task.md` (gitignored, ephemeral)

#### Scenario: Run result recorded
- **WHEN** an agent executor returns a RunResult
- **THEN** the system SHALL persist the result to the store with: run ID, relay ID, agent type, completed status, summary, files changed, commit hash, timestamps, and attempt number

### Requirement: Retry logic
The system SHALL retry failed runs up to 3 times per task. A run is considered failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes).

#### Scenario: Retry with previous summary
- **WHEN** a run fails and retries remain
- **THEN** the system SHALL pass the previous run's summary as `PreviousSummary` in the next attempt's RunOptions

#### Scenario: Retry exhaustion
- **WHEN** a run fails 3 times consecutively
- **THEN** the system SHALL halt the relay with a retry-exhausted error

### Requirement: Error resilience cascade
The system SHALL implement a per-agent-type error resilience cascade: after 3 consecutive failures, pause the agent type for 1 hour. Retry hourly; after 5 hours of failures, freeze the agent type for the remainder of the relay. If all agent types are paused, wait for the next hourly check. If all agent types are frozen, end the relay as a failure.

#### Scenario: Agent paused after retry exhaustion
- **WHEN** an agent type fails 3 consecutive retries
- **THEN** the system SHALL mark that agent type as paused and skip it in the agent mix for 1 hour

#### Scenario: Agent frozen after extended failure
- **WHEN** a paused agent type continues to fail after 5 hours of hourly retries
- **THEN** the system SHALL freeze that agent type for the remainder of the relay

#### Scenario: All agents frozen ends relay
- **WHEN** all agent types in the mix are frozen
- **THEN** the system SHALL end the relay as a batch failure

#### Scenario: System waits when all agents paused
- **WHEN** all available agent types are paused (but not frozen)
- **THEN** the system SHALL wait until the next agent's hourly retry check

### Requirement: Graceful stop
The system SHALL support graceful stopping: when requested, the current run completes, and the relay halts without starting a new run. The relay state is preserved for future resumption.

#### Scenario: Stop requested during run
- **WHEN** a stop is requested while a run is in progress
- **THEN** the system SHALL complete the current run and then halt the relay

#### Scenario: Relay state preserved on stop
- **WHEN** a relay is stopped gracefully
- **THEN** the relay record SHALL reflect completed iterations and remain resumable

### Requirement: Inbox message consumption
The system SHALL support an inbox of messages that can be injected into agent runs. The oldest pending message is consumed by each run and included in the agent's prompt.

#### Scenario: Message included in prompt
- **WHEN** a pending inbox message exists at run start
- **THEN** the system SHALL include the message body in the agent's prompt and mark it as consumed after the run

#### Scenario: Message addressed tracking
- **WHEN** the agent's RunResult includes `MessageAddressed: true`
- **THEN** the system SHALL mark the consumed message as addressed in the store
