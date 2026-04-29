## ADDED Requirements

### Requirement: Naming — try, run, relay
The system SHALL use three-tier naming for execution units:
- **Try**: One invocation of an agent CLI, regardless of outcome. The atomic unit. Each try produces a `TryResult`.
- **Run**: One logical iteration that counts against the relay's target iteration count. A run consumes a distinct run-level inbox message and receives the same task context. If no agent failures occur, one run equals one try. If the try fails, the run is retried — each retry is a new try within the same run.
- **Relay**: A campaign of N runs with a configured agent mix.

#### Scenario: Try fails within a run
- **WHEN** a try fails within a run
- **THEN** the system SHALL retry the try within the same run

### Requirement: Relay lifecycle
The system SHALL manage relays as a campaign of N sequential runs with a configured agent mix. A relay tracks: relay ID, target iterations, completed iterations, agent mix, start time, end time, first/last try ID, and consumed relay-level message IDs.

#### Scenario: New relay created
- **WHEN** a user starts a relay with a prompt, iteration count, and agent mix
- **THEN** the system SHALL create a relay record and begin executing runs sequentially

#### Scenario: Relay resumes after interruption
- **WHEN** `rally relay` is invoked and an incomplete relay exists in state
- **THEN** rally SHALL print a summary of the incomplete relay (completed/total runs, agent mix) and prompt the user to resume or discard. A `--resume` flag SHALL skip the prompt and resume automatically.

### Requirement: Agent mix cycling
The system SHALL cycle through agents in a deterministic rotation based on the configured agent mix weights. For example, `cc:2 cx:1` produces the cycle `[cc, cc, cx]`, repeated across runs.

#### Scenario: Deterministic agent selection
- **WHEN** a run is about to start within a relay
- **THEN** the system SHALL select the agent using `cycle[(runIndex) % len(cycle)]`

#### Scenario: Agent mix parsed from spec
- **WHEN** agent specs like `"cc:2 cx:1"` are provided
- **THEN** the system SHALL parse them into weighted cycles preserving declaration order

### Requirement: Try execution
The system SHALL execute each try by: writing `.rally/current_task.md` (the prompt fed to the agent), recording HEAD before the try, invoking the selected agent's Executor, tracking commit hash, recording the TryResult, and auto-committing if needed.

#### Scenario: Context file written before try
- **WHEN** a try is about to execute
- **THEN** the system SHALL write the agent prompt to `.rally/current_task.md` (gitignored, ephemeral)

#### Scenario: Commit hash tracking
- **WHEN** a try completes
- **THEN** the runner SHALL compare current HEAD against pre-try HEAD. If the agent committed (HEAD changed), use that commit hash. If uncommitted changes exist, auto-commit and use that hash. If no changes, record no commit hash.

#### Scenario: Auto-commit uses --no-verify by default
- **WHEN** the runner auto-commits uncommitted changes
- **THEN** the commit SHALL use `--no-verify` unless `run_hooks_on_autocommit = true` is set in `.rally/config.toml`
- **AND** if git `user.name` or `user.email` are not configured, the runner SHALL set `user.name=Rally` and `user.email=rally@localhost` for the commit

#### Scenario: Try result recorded
- **WHEN** a try's commit hash is determined
- **THEN** the system SHALL persist the result to the store with: try ID, run ID, relay ID, agent type, completed status, summary, files changed, commit hash, timestamps, and attempt number

#### Scenario: Auto-commit only when needed
- **WHEN** a try leaves uncommitted workspace changes
- **THEN** the system SHALL auto-commit on the current branch. Rally does NOT create, switch, or merge branches. If the agent already committed, no auto-commit is needed.

### Requirement: Failure detection
The system SHALL consider a try failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes).

#### Scenario: Short no-op try detected as failure
- **WHEN** a try produces no file changes and completes in under 3 minutes
- **THEN** the system SHALL treat it as a failed try (possible rate limit, auth error, or empty response)

#### Scenario: Agent error exit detected as failure
- **WHEN** the agent subprocess exits with a non-zero exit code
- **THEN** the system SHALL treat it as a failed try

### Requirement: Retry logic
The system SHALL retry failed tries up to 3 times within a single run. Retries do NOT count against the relay's iteration count. The previous try's summary is passed to the next attempt.

#### Scenario: Retry with previous summary
- **WHEN** a try fails and retries remain
- **THEN** the system SHALL pass the previous try's summary as `PreviousSummary` in the next attempt's RunOptions

#### Scenario: Retry exhaustion triggers error cascade
- **WHEN** a run's try fails 3 times consecutively
- **THEN** the system SHALL trigger the error resilience cascade for that agent type (NOT halt the relay)

### Requirement: Error resilience cascade
The system SHALL implement a per-agent-type error resilience cascade. After 3 consecutive try failures within a run, the agent type is paused for 1 hour. The system retries hourly. After 5 hours of continued failures, the agent type is frozen for the remainder of the relay. If all agent types are paused, the system waits for the next hourly check. If all agent types are frozen, the relay ends as a failure.

#### Scenario: Agent paused after retry exhaustion
- **WHEN** an agent type's tries fail 3 consecutive times within a run
- **THEN** the system SHALL mark that agent type as paused, skip it in the agent mix, and schedule an hourly retry

#### Scenario: Agent unfreezes after hourly retry succeeds
- **WHEN** a paused agent type's hourly retry try succeeds
- **THEN** the system SHALL restore the agent type to active status in the mix

#### Scenario: Agent frozen after extended failure
- **WHEN** a paused agent type continues to fail after 5 hours of hourly retries
- **THEN** the system SHALL freeze that agent type for the remainder of the relay

#### Scenario: All agents frozen ends relay
- **WHEN** all agent types in the mix are frozen
- **THEN** the system SHALL end the relay as a relay failure

#### Scenario: System waits when all agents paused
- **WHEN** all available agent types are paused (but not frozen)
- **THEN** the system SHALL wait until the next agent's hourly retry check

#### Scenario: Pause/freeze state persisted across restarts
- **WHEN** rally is restarted while agents are paused or frozen
- **THEN** the system SHALL restore pause/freeze state and timestamps from `agent_status.jsonl`, so that timers are not reset by a restart

### Requirement: Graceful stop
The system SHALL support graceful stopping: when requested, the current try completes, and the relay halts without starting a new run. The relay state is preserved for future resumption.

#### Scenario: Stop requested during try
- **WHEN** a stop is requested while a try is in progress
- **THEN** the system SHALL complete the current try and then halt the relay

#### Scenario: Relay state preserved on stop
- **WHEN** a relay is stopped gracefully
- **THEN** the relay record SHALL reflect completed iterations and remain resumable

### Requirement: Inbox message consumption
The system SHALL support an inbox of messages that can be injected into runs. The oldest pending message is consumed per run (not per try) and included in all try prompts within that run.

#### Scenario: Message included in prompt
- **WHEN** a pending inbox message exists at run start
- **THEN** the system SHALL include the message body in the agent's prompt for all tries within that run

#### Scenario: Message addressed tracking
- **WHEN** the agent's TryResult includes `MessageAddressed: true`
- **THEN** the system SHALL mark the consumed message as addressed in the store

#### Scenario: Message not re-consumed on retry
- **WHEN** a try fails and is retried within the same run
- **THEN** the same inbox message SHALL be included (not a new one consumed)

### Requirement: Relay logging
The system SHALL produce a human-readable relay log for each relay, capturing filtered output from all tries.

#### Scenario: Dual-write relay logs
- **WHEN** relay output is written
- **THEN** the system SHALL write to both `~/.local/share/rally/relays/relay-N.log` (durable) and `.rally/relays/relay-N.log` (repo cache)

#### Scenario: Repo cache pruning
- **WHEN** the relay log cache in `.rally/relays/` exceeds 10 files
- **THEN** the system SHALL prune the oldest logs to maintain the 10-file limit

#### Scenario: Relay log cache is gitignored
- **WHEN** the `.rally/` directory is configured
- **THEN** `.rally/relays/` SHALL be excluded from git tracking
