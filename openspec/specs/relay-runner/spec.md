# relay-runner Specification

## Purpose
TBD - created by archiving change consolidate-rally-gry. Update Purpose after archive.
## Requirements
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
The system SHALL classify a try as "incomplete" rather than "failed" when file changes
produced **by that try** were left uncommitted (dirty working tree) and the agent
neither finalized the lap (`laps done`) nor handed off (`laps handoff`). To attribute
changes to the try, the system SHALL snapshot the set of already-dirty paths at try
start and SHALL consider only the working-tree delta against that snapshot. Uncommitted
leftovers inherited from a prior failed try and left untouched SHALL NOT, on their own,
make a later try incomplete. An incomplete try SHALL have its auto-commit suppressed,
leaving changes uncommitted. The retry run SHALL inherit the uncommitted changes and
SHALL receive prompt guidance: "The last run was incomplete. Check any current git
changes, finish anything not done, verify correctness, commit when good, then run `laps
done`." An incomplete try SHALL be retried but SHALL NOT count toward the pause/freeze
resilience cascade.

#### Scenario: Agent produces file changes without finalizing
- **WHEN** a try produces file changes in the working tree but the agent does not call `laps done` or `laps handoff`
- **THEN** the system SHALL classify the try as incomplete, suppress auto-commit, retry the run with prompt guidance, and SHALL NOT call `PauseAgent` or `RecordHourlyFailure`

#### Scenario: No file changes and no finalization
- **WHEN** a try produces no file changes and the agent does not finalize
- **THEN** the system SHALL classify as a normal agent-class failure (retry-eligible, does not escalate)

#### Scenario: Inherited leftover changes do not trigger incomplete
- **WHEN** a try begins with a dirty working tree inherited from a prior failed try, and the try produces no new changes of its own and does not finalize
- **THEN** the system SHALL NOT classify the try as incomplete on the basis of those untouched leftovers

#### Scenario: Touching an inherited leftover attributes it to this try
- **WHEN** a try modifies, reverts, or commits a path that was already dirty at try start
- **THEN** that path SHALL count as a change produced by this try when classifying incompleteness

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
- Gets at most one run per probation cycle. The one-shot is enforced by `syncRecoverySignals`: when the frozen→probation transition is first observed, a probation event is persisted and the scheduler entry is unbenched. The entry remains selectable while state is probation; the once-per-cycle guarantee is enforced by the probation event guard (no duplicate probation events are written) and by `runOne` writing an active or frozen event when the run resolves.
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
- **WHEN** a probationary pair's frozen→probation transition is first observed by `syncRecoverySignals`
- **THEN** the system SHALL persist a probation event and unbench the entry. The entry remains selectable while state is probation. The once-per-cycle guarantee is enforced by the probation event guard (preventing duplicate probation events) and by `runOne` writing an active or frozen event when the run resolves.

#### Scenario: Freeze re-evaluated on resume
- **WHEN** a relay resumes or a non-`--new` relay starts and a pair's freeze has decayed
- **THEN** the system SHALL re-evaluate freeze state via `getState` (a pure read) rather than re-apply the stored frozen state verbatim

#### Scenario: All agents frozen ends the current pass
- **WHEN** all harness-model pairs in the mix are currently frozen and none have decayed to probation
- **THEN** the system SHALL end the relay pass as a failure, leaving freezes subject to later decay

#### Scenario: System waits when all agents paused
- **WHEN** all available harness-model pairs are paused (but not frozen)
- **THEN** the system SHALL wait until the next pair's hourly retry check

#### Scenario: Pause/freeze/probation state persisted across restarts
- **WHEN** rally is restarted while agents are paused, frozen, or on probation
- **THEN** the system SHALL restore state and timestamps from `agent_status.jsonl`, re-evaluating frozen entries against `FreezeDuration` rather than inheriting stale state

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

#### Scenario: Data-dir relay logs
- **WHEN** relay output is written
- **THEN** the system SHALL write to `~/.local/share/rally/relays/<repo>/relay-N.log`

#### Scenario: Relay log is not mirrored into the repo
- **WHEN** a relay run writes log output
- **THEN** the system SHALL NOT create `.rally/relays/`

### Requirement: Configurable stall threshold default
The system SHALL read the stall/liveness threshold from `stall_threshold_secs` in `.rally/config.toml` and SHALL default it to 900 seconds (15 minutes) when unset. The "slowing" display indicator SHALL derive from this threshold (at 0.6× the threshold) so a single configured value moves both the kill threshold and the warning indicator together.

#### Scenario: Default threshold when unset
- **WHEN** a relay starts and `stall_threshold_secs` is not configured
- **THEN** the system SHALL use a 900-second stall threshold
- **AND** the "slowing" indicator SHALL appear only after ~540 seconds (0.6×) of log silence

#### Scenario: Operator overrides threshold
- **WHEN** `stall_threshold_secs` is set to a positive value
- **THEN** the system SHALL use that value for both the stall threshold and the derived slowing indicator

#### Scenario: Normal reasoning is not flagged
- **WHEN** an agent produces no log activity for a period shorter than the slowing-indicator window (e.g. a multi-minute reasoning burst under the default)
- **THEN** the system SHALL NOT display a "slowing" indicator for that period

### Requirement: Live retry indicator
While a run is retrying within its retry budget, the system SHALL surface the retry progress as an inline field (`retry N/M`) on the existing live status line. The system SHALL NOT print a separate console block for each retry attempt.

#### Scenario: Retry in progress
- **WHEN** a run begins attempt N of M (N > 1)
- **THEN** the live status line SHALL include a `retry N/M` field
- **AND** no new status block SHALL be printed solely to announce the retry

### Requirement: Run-level result tally
The final relay summary SHALL count each run once: a run counts as a pass if it ultimately completed, and as a failure only if all of its retry attempts were exhausted without completion. Individual retried (non-final) attempts SHALL NOT each be counted as failures.

#### Scenario: Run succeeds after retries
- **WHEN** a run fails several attempts and then completes on a later attempt
- **THEN** the final summary SHALL count the run as one pass and zero failures

#### Scenario: Run exhausts retries
- **WHEN** a run fails every attempt up to its retry budget
- **THEN** the final summary SHALL count the run as one failure

### Requirement: Runner normalizes final snippets
The relay runner SHALL normalize the final snippet used for persisted `TryResult.Summary`, retry context, and `summary.jsonl` so Rally's persisted surfaces agree about what the agent reported. When a `laps wrapup` summary is recorded after `laps done` or `laps handoff`, that wrapup summary SHALL be the golden source. If no wrapup summary was recorded, the runner SHALL use the executor's parsed final assistant or structured summary text. If neither source exists, the runner SHALL use the executor's bounded tail text or explicit no-finalization/error indicator.

#### Scenario: Wrapup summary recorded
- **WHEN** an agent finalizes a run by calling `laps done` or `laps handoff` and then `laps wrapup --summary ...`
- **THEN** the persisted `TryResult.Summary` SHALL use the `laps wrapup` summary text
- **AND** retry context and `summary.jsonl` SHALL use the same normalized final snippet

#### Scenario: Executor summary fallback
- **WHEN** no `laps wrapup` summary was recorded but the executor returned parsed final assistant or structured summary text
- **THEN** the runner SHALL use that executor summary as the normalized final snippet

#### Scenario: Bounded fallback summary
- **WHEN** no wrapup summary and no parsed executor final text are available
- **THEN** the runner SHALL use the executor's bounded tail text or explicit no-finalization/error indicator as the normalized final snippet

### Requirement: State commit respects operator gitignore
When the system commits its `.rally` operational state paths, any `.rally` path that the operator has placed under `.gitignore` SHALL be skipped without error and without forcing the add. The default tracked `.rally` paths SHALL explicitly include `.rally/config.toml` and `.rally/summary.jsonl`. The system SHALL NOT use `git add -f` and SHALL NOT abort the run because a `.rally` operational path was gitignored. This requirement is scoped to `.rally` operational paths and SHALL NOT permit skipping `.laps/laps.json`.

#### Scenario: Tracked path is gitignored by operator
- **WHEN** the system attempts to add a `.rally` operational state path the operator has gitignored
- **THEN** the system SHALL skip that path, continue committing the remaining tracked `.rally` paths, and SHALL NOT return an error for the ignored path

#### Scenario: Laps queue state remains mandatory
- **WHEN** `.laps/laps.json` is present in the workspace
- **THEN** this gitignore-tolerance behavior SHALL NOT treat it as optional or silently omit it from required commits

