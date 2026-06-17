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
The system SHALL classify a try as "incomplete" rather than "failed" when file changes produced **by that try** were left uncommitted (dirty working tree) and the agent neither finalized the lap (`laps done`) nor handed off (`laps handoff`). To attribute changes to the try, the system SHALL snapshot the set of already-dirty paths at try start and SHALL consider only the working-tree delta against that snapshot. Uncommitted leftovers inherited from a prior failed try and left untouched SHALL NOT, on their own, make a later try incomplete.

Stronger evidence SHALL take precedence over incomplete classification: when structured executor evidence or a provider/configuration/quota signal (`usage_limit`, `invalid_model`, `auth_or_proxy`, `provider_overloaded`, `short_rate_limit`) is present, the system SHALL classify by that signal even if the working tree is dirty, so a dirty file cannot mask a stronger failure.

Paths that are not task work SHALL be excluded from the meaningful-change determination through a single shared exclusion helper covering Rally's own metadata (`.rally/`, `.laps/`) and known harness-local transient state (starting with `.claude/settings.local.json`). A try whose only dirty paths are excluded SHALL be treated as having produced no meaningful task changes.

An incomplete try SHALL have its auto-commit suppressed, leaving changes uncommitted. The retry run SHALL inherit the uncommitted changes and SHALL receive prompt guidance: "The last run was incomplete. Check any current git changes, finish anything not done, verify correctness, commit when good, then run `laps done`." An incomplete try SHALL be retried but SHALL NOT count toward the pause/freeze resilience cascade.

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

#### Scenario: Provider error beats dirty harness-local state
- **WHEN** a try logs a provider/quota/config signal (e.g. `RESOURCE_EXHAUSTED`, `model_not_found`, `Failed to authenticate`) and the only dirty path is excluded harness-local state (e.g. `.claude/settings.local.json`)
- **THEN** the system SHALL classify by the provider/quota/config signal and SHALL NOT classify the try as incomplete

#### Scenario: Harness-local-only dirt is not meaningful change
- **WHEN** a try's only dirty paths are covered by the shared exclusion helper
- **THEN** the system SHALL treat the try as having produced no meaningful task changes for incomplete classification

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
The system SHALL consider a try failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes). The system SHALL assign each failure a stable `FailureCategory` (see "Failure taxonomy and evidence") and SHALL map that category onto one of three resilience classes:
- **infra-class**: short rate limit, provider overload, harness/launch error (e.g. `argument list too long`, `fork/exec`), transient infrastructure error (`transient_infra`: API timeout, network/connection/TLS failure, non-overload 5xx), or liveness stall detection.
- **agent-class**: ordinary agent error or short no-op.
- **incomplete**: file changes were produced but the agent did not finalize the lap (`laps done` or `laps handoff`).

Long usage/quota exhaustion (`usage_limit`), invalid-model/config (`invalid_model`), and authentication/proxy (`auth_or_proxy`) failures SHALL NOT be classified infra-class; they are handled by benching/routing and SHALL NOT increment the pause/freeze counter. A try whose outcome is `run_timeout` or `handoff_timeout` (see "Try outcome lifecycle") SHALL likewise NOT be classified infra-class and SHALL NOT increment the pause/freeze counter.

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

#### Scenario: Timeout lifecycle outcomes are not infra-class
- **WHEN** a try resolves with a `run_timeout` or `handoff_timeout` outcome
- **THEN** the system SHALL NOT classify it infra-class, SHALL NOT increment the pause/freeze counter, and SHALL NOT treat it as a usage-limit or harness failure

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

### Requirement: Failure taxonomy and evidence
The system SHALL classify each failure into one stable `FailureCategory`: `usage_limit`, `short_rate_limit`, `provider_overloaded`, `transient_infra`, `invalid_model`, `auth_or_proxy`, `harness_launch`, `incomplete_finalization`, or `agent_error`. (`transient_infra` covers API timeout, network/connection/TLS failure, and non-overload 5xx; the liveness stall kill is not a log-text category and continues to set an infra class directly via the stall path.) Each category SHALL have a short, human-readable display label distinct from the machine-readable category, and SHALL map to exactly one resilience `FailureClass` per the Category→FailureClass mapping (`usage_limit`, `invalid_model`, and `auth_or_proxy` map to neither infra nor the freeze counter). Classification SHALL prefer, in order: (1) typed `FailureEvidence` supplied by the executor; (2) provider/configuration/quota evidence from structured data or bounded error snippets; (3) meaningful task-file change (`incomplete_finalization`); (4) harness-scoped text patterns; (5) `agent_error` as the default. The system SHALL tolerate absent evidence and fall back to log-tail classification. The freeze-cascade counter SHALL be driven solely by the mapped infra `FailureClass`, making the Category→FailureClass mapping the single source of truth for freeze accounting; no separate per-`Category` check SHALL govern the freeze increment at its call site.

#### Scenario: Structured evidence wins over text patterns
- **WHEN** the executor supplies `FailureEvidence` with a category
- **THEN** the system SHALL use that category rather than re-deriving one from log text

#### Scenario: Unknown failure defaults to agent_error
- **WHEN** no evidence, provider/config/quota signal, meaningful change, or harness-scoped pattern matches
- **THEN** the system SHALL classify the failure as `agent_error`

#### Scenario: Each category maps to one resilience class
- **WHEN** a category is assigned
- **THEN** the system SHALL derive its resilience `FailureClass` from a single mapping in which `usage_limit`/`invalid_model`/`auth_or_proxy` are not infra-class

### Requirement: Harness-scoped classification
The system SHALL scope failure classification to the harness that produced the failure, so that a pattern naming or implying one harness cannot match a failure from a different harness. Display labels for generic failures SHALL NOT name a harness unless the failing harness matches that harness and the label is intentionally harness-specific.

#### Scenario: Codex prose does not match a Claude pattern
- **WHEN** a Codex try's log tail incidentally contains the text "rate-limit" or "Claude"
- **THEN** the system SHALL NOT classify or label the failure as a Claude rate limit

#### Scenario: Harness name omitted from generic label
- **WHEN** a generic failure (e.g. a usage limit) is displayed for a non-Claude harness
- **THEN** the display label SHALL NOT name a different harness

### Requirement: Terminal failure categories short-circuit the attempt loop
The system SHALL terminate a run's per-try attempt loop on the first detection of a `usage_limit` or `auth_or_proxy` failure, or of a `handoff_timeout` outcome, so the run makes exactly one attempt against an exhausted quota, failed auth, or an unrecoverable handoff rather than consuming its remaining retry budget. The resolved category/outcome and any reset evidence SHALL be surfaced from the run to the routing layer so a bench, route-away, or recovery-routing decision can be made.

#### Scenario: Usage limit makes one attempt
- **WHEN** a try fails with `usage_limit`
- **THEN** the run SHALL NOT make further attempts against the same runner and SHALL surface the category and reset evidence to the routing layer

#### Scenario: Auth failure routes away without looping
- **WHEN** a try fails with `auth_or_proxy`
- **THEN** the run SHALL make exactly one attempt and route away

#### Scenario: Handoff timeout short-circuits to recovery routing
- **WHEN** a try resolves as `handoff_timeout`
- **THEN** the run SHALL make no further same-runner attempts and SHALL surface the outcome to the routing layer so the next run for the lap is routed to RECOVERY

### Requirement: Usage-limit benching and reset recovery
On a `usage_limit` failure the system SHALL bench every route entry sharing the failed runner's quota scope until the limit resets, instead of waiting a short fixed cooldown and retrying. Benching SHALL be modelled as a persisted resilience state carrying the reset deadline and quota scope, reusing the existing resilience persistence, recovery-sync, and selection-wait pipeline rather than a separate in-memory rotation axis. When reset timing is parseable the bench SHALL last until that reset; otherwise a long conservative default SHALL be used and the runner routed away. While a scope's reset deadline is in the future, its entries SHALL NOT be returned to rotation by the recovery sync; because the benched state is not the active state, this requires no special-case guard on the active-recovery path and SHALL NOT affect the probation one-shot unbench. When every entry in a lane is benched with a future reset, the system SHALL wait until the earliest reset rather than ending the relay as "all agents frozen". Because the deadline is persisted in the agent-status event log, the bench SHALL survive across relays on the same machine with no dedicated restoration path, and on the first selection after a persisted deadline passes the scope SHALL be re-probed once.

#### Scenario: Usage limit benches the quota scope until reset
- **WHEN** a try fails with `usage_limit` carrying a parsed reset window
- **THEN** the system SHALL bench all entries sharing the quota scope until that reset and route away, rather than looping a short cooldown

#### Scenario: Bench is not undone by recovery sync
- **WHEN** the resilience recovery sync runs while a benched scope's reset deadline is still in the future
- **THEN** the system SHALL keep the scope's entries out of rotation without a special-case guard, since the benched state is distinct from the active state

#### Scenario: All-benched lane waits for reset
- **WHEN** every entry in a lane is benched with a future reset deadline
- **THEN** the system SHALL wait until the earliest reset rather than failing the relay as "all agents frozen"
- **NOTE**: this differs from "All agents frozen ends the current pass" (Error resilience cascade) — a frozen lane with no pending reset still ends the pass; only a future bench deadline produces a wait

#### Scenario: Reset persists across relays and re-probes once
- **WHEN** a relay starts and a persisted reset deadline for a quota scope has not yet passed
- **THEN** the scope SHALL remain benched; and once the deadline passes the scope SHALL be re-probed a single time before normal selection resumes

#### Scenario: Re-probe still exhausted re-benches a fresh window
- **WHEN** the single post-deadline re-probe again returns `usage_limit`
- **THEN** the scope SHALL be re-benched for a fresh window (parsed reset or default) rather than treated as permanently exhausted

### Requirement: Harness-aware quota scope
The system SHALL resolve a quota scope key from a runner's harness and model via a single resolver, so benching applies to all route entries sharing an account/quota bucket. The resolver SHALL key antigravity per model family (case-insensitive substring over the free-form display label: `claude` / `flash` / `pro`), opencode per provider (the segment before the first `/` in the model id), and direct harnesses (`claude`, `codex`, `gemini`) per harness with the model ignored.

#### Scenario: Antigravity families are distinct scopes
- **WHEN** quota scopes are resolved for antigravity Claude and antigravity Gemini-flash models
- **THEN** they SHALL resolve to distinct scopes, so exhausting one does not bench the other

#### Scenario: Opencode keys on provider
- **WHEN** a quota scope is resolved for an opencode model `provider/model`
- **THEN** the scope SHALL be keyed on the provider segment before the first `/`

#### Scenario: Direct harness keys per harness
- **WHEN** a quota scope is resolved for a direct harness (claude/codex/gemini)
- **THEN** the scope SHALL be keyed on the harness and SHALL NOT be mis-split by any `/` in the model string

### Requirement: Categorised failure display and records
The system SHALL display the accurate failure category in the run footer and the collapsed retry line, including reset or wait detail when known (e.g. `usage limit, resets in 123h50m`, `rate limit, waiting 2m`, `invalid model`). Persisted try records (`TryRecord` in `tries.jsonl`) SHALL store the stable category separately from the human-readable display reason (`FailReason`), so machine consumers do not parse the display string.

#### Scenario: Usage limit shows reset detail
- **WHEN** a `usage_limit` failure with a parsed reset is displayed
- **THEN** the footer/retry line SHALL include the reset detail (e.g. `usage limit, resets in 123h50m`)

#### Scenario: Records carry stable category
- **WHEN** a failed try is persisted
- **THEN** the record SHALL include both the stable `FailureCategory` and the human-readable display reason

### Requirement: Resume finalization guidance
When retrying or resuming after an `incomplete_finalization` failure (including operator pause/resume of a laps-backed run), the resumed prompt SHALL include explicit finalization instructions directing the agent to finish the lap and call `laps done` (or `laps handoff` if blocked).

#### Scenario: Incomplete retry carries finalization guidance
- **WHEN** a run is retried after an `incomplete_finalization` failure
- **THEN** the resumed prompt SHALL include explicit guidance to finish and call `laps done`/`laps handoff`

### Requirement: Try outcome lifecycle
The system SHALL classify every try with a stable `TryOutcome` lifecycle value, orthogonal to the `FailureCategory` failure-cause taxonomy: `completed` (lap finalized via `laps done`), `handoff_requested` (a successful handoff — both `laps handoff` and `laps wrapup` completed — with the lap not yet done), `incomplete` (the try produced own file changes but did not finalize), `run_timeout` (the implementation attempt was cancelled because the run budget was exhausted and a handoff-only continuation will resolve the run), `handoff_timeout` (the bounded handoff recovery did not finalize, or no handoff-only continuation could be invoked), `failed` (a hard failure whose cause is carried by `FailureCategory`), or `interrupted` (operator stop). `FailureCategory` SHALL retain only its failure-cause values and SHALL NOT be extended with lifecycle labels. Success, freeze, retry, and Issue decisions SHALL be driven by `TryOutcome` (and, for a `failed` outcome, the category's resilience class), so no consumer treats a failure-cause category as a success.

`handoff_requested` SHALL be a successful outcome: it SHALL NOT increment the pause/freeze counter, SHALL NOT be treated as a harness/usage/rate/agent/infra failure, and SHALL NOT be captured as an Issue. `run_timeout` SHALL be a non-freezing, non-Issue implementation-attempt outcome and SHALL NOT trigger recovery by itself. `handoff_timeout` SHALL be a non-freezing failure outcome that does not feed the freeze counter. The persisted try record SHALL store the `TryOutcome` and whether a try is `HandoffOnly` while retaining the existing `Completed` boolean for compatibility.

#### Scenario: Completed lap records completed outcome
- **WHEN** a try finalizes its lap via `laps done`
- **THEN** the try outcome SHALL be `completed`

#### Scenario: Handoff success records handoff_requested
- **WHEN** a try completes both `laps handoff` and `laps wrapup`
- **THEN** the try outcome SHALL be `handoff_requested`, which SHALL NOT count toward pause/freeze and SHALL NOT be captured as an Issue

#### Scenario: Outcome does not collide with failure category
- **WHEN** a try resolves with a lifecycle outcome (`handoff_requested`, `run_timeout`, `handoff_timeout`, `incomplete`, `completed`, `interrupted`)
- **THEN** the system SHALL NOT add that value to `FailureCategory`, and a `failed` outcome SHALL be the only one that carries a `FailureCategory` cause

### Requirement: Run/try timeout and bounded handoff recovery
The system SHALL enforce a hard wall-clock budget on a run measured **across all of its retry attempts** (`run_timeout_secs` under `[reliability]`, default 4500 seconds / 75 minutes), independent of the silence-based stall detector, so a struggling runner cannot grind for hours before the run resolves. The system SHALL additionally enforce a secondary per-attempt cap (`try_timeout_secs`, default 3600 seconds / 60 minutes). Whichever bound (run budget, per-try cap, or stall detector) fires first SHALL cancel the running attempt via the existing graceful-shutdown path. A per-try cap firing with run budget remaining MAY be followed by a fresh retry within the remaining budget; exhaustion of the **run budget** SHALL stop further retries and proceed to the bounded handoff. The bounded handoff phase SHALL NOT be counted against the run budget.

When the run budget is exhausted, the system SHALL attempt exactly one bounded handoff-only recovery **iff** the harness reports `ResumeSupported()` and a session ID was captured: it SHALL resume that same session with a handoff-only prompt under a separate hard bound (`handoff_timeout_secs` under `[reliability]`, default 300 seconds / 5 minutes). The handoff-only phase SHALL NOT continue implementation; its only goal is for the agent to summarize the blocker and call `laps handoff` followed by `laps wrapup`. The handoff window SHALL never exceed the per-try cap.

A bounded handoff recovery SHALL record the try outcome as `handoff_requested` **only when both** `laps handoff` and `laps wrapup` completed. If the harness cannot resume, no session was captured, or the handoff-only phase fails, times out, or completes `laps handoff` without a completed `laps wrapup`, the system SHALL record the try outcome as `handoff_timeout`.

When the handoff-only continuation is invoked, it SHALL be persisted as a separate try record under the same `RunID` as the budget-cancelled implementation attempt, with the next `AttemptNumber` (even if that exceeds the normal retry budget), `HandoffOnly=true`, the same resolved route, and `handoff_requested` or `handoff_timeout` as the resolving outcome. The budget-cancelled implementation attempt SHALL also be persisted first with outcome `run_timeout`; that record is non-freezing, not an Issue, and not a recovery trigger by itself. When no resume-capable session exists, the system SHALL NOT fabricate a handoff-only try record; the implementation attempt SHALL instead be the persisted resolving try with outcome `handoff_timeout`.

#### Scenario: Run budget across retries stops grinding
- **WHEN** a run's cumulative wall-clock across its retry attempts reaches `run_timeout_secs`
- **THEN** the system SHALL cancel the active attempt via graceful shutdown, stop further retries, and proceed to the bounded handoff (treating it as neither a stall nor an ordinary agent error)

#### Scenario: Per-try cap stops a single runaway attempt
- **WHEN** a single attempt runs past `try_timeout_secs` while run budget remains
- **THEN** the system SHALL cancel that attempt and MAY start a fresh retry within the remaining run budget

#### Scenario: Resumable harness gets a bounded handoff-only continuation
- **WHEN** the run budget is exhausted, the harness reports `ResumeSupported()`, and a session ID was captured
- **THEN** the system SHALL persist the cancelled implementation attempt as `run_timeout`, then resume that session once with a handoff-only prompt bounded by `handoff_timeout_secs`, persist the continuation as a separate `HandoffOnly` try with the same `RunID` and next `AttemptNumber`, and SHALL NOT permit continued implementation in that phase

#### Scenario: Successful bounded handoff records handoff_requested
- **WHEN** the bounded handoff-only phase completes both `laps handoff` and `laps wrapup`
- **THEN** the system SHALL record the try outcome as `handoff_requested`

#### Scenario: No resume support records handoff_timeout
- **WHEN** the run budget is exhausted and the harness does not support resume or no session ID was captured
- **THEN** the system SHALL record the implementation try outcome as `handoff_timeout`, SHALL NOT append a synthetic handoff-only try, and SHALL route the next run for the lap to RECOVERY

#### Scenario: Failed or partial handoff records handoff_timeout
- **WHEN** the bounded handoff-only phase fails, times out, or completes `laps handoff` without a completed `laps wrapup`
- **THEN** the system SHALL record the try outcome as `handoff_timeout`

#### Scenario: Timeout outcomes do not feed the freeze counter
- **WHEN** a try resolves as `handoff_requested`, `run_timeout`, or `handoff_timeout`
- **THEN** the system SHALL NOT increment the infra freeze counter and SHALL NOT treat the outcome as a usage-limit, rate-limit, or harness failure

### Requirement: Recovery routing triggers
The system SHALL route the next run for a lap to the `recovery` route when the lap is not done **and** either recovery trigger holds:
1. the lap's resolving run was a **dirty handoff** — a handoff completed through `laps wrapup` yet meaningful own-uncommitted changes remain. This is persisted as `TryRecord.DirtyHandoff`, derived from the current-run durable handoff entry together with own-uncommitted changes at try resolution, distinct from the `incomplete` `TryOutcome`, which a handoff makes unreachable because any handoff marks the try finalized;
2. the lap's resolving run ended with a `handoff_timeout` outcome.

Recovery is specifically for reconciling a dirty, handed-off tree. An ordinary `failed` try (e.g. usage limit, provider overload, rate limit, agent error) SHALL NOT trigger recovery; such failures SHALL be handled by the existing bench/route/rotate resilience paths. An `incomplete` outcome without a handoff SHALL keep its existing resume-with-finalization-guidance retry path and SHALL NOT, on its own, trigger recovery. A clean handoff that finalized with no meaningful leftover dirty state SHALL keep its existing follow-up flow and SHALL NOT trigger recovery.

Recovery routing SHALL be applied as an in-relay route override that resolves the `recovery` route by assignee, and SHALL NOT rewrite the lap's `assignee` in the work-queue file. A recovery-forced run SHALL expose an effective assignee/prompt role of `recovery` for role prompt resolution, telemetry role tags, and recovery-classification gating, while preserving the original lap assignee in persisted queue/audit fields. While a recovery trigger holds for a dirty tree, the system SHALL NOT advance past that dirty tree before a recovery run executes: if handoff followups were inserted at the queue head, any claimed head lap whose ID appears in the triggering try's handoff-created lap IDs SHALL also route to RECOVERY as continuation of the same dirty handoff. If no `recovery` route is configured, the system SHALL fall back to the lap's normal route and emit a warning rather than deadlocking the relay.

#### Scenario: Dirty handoff routes to recovery
- **WHEN** a try completes handoff through `laps wrapup` but leaves meaningful own-uncommitted changes
- **THEN** the system SHALL route the next run for that lap to the `recovery` route without advancing the queue past the lap

#### Scenario: Dirty handoff is not auto-committed
- **WHEN** a try completes handoff through `laps wrapup` but leaves meaningful own-uncommitted changes (a dirty handoff)
- **THEN** the system SHALL NOT auto-commit those leftover changes and SHALL leave the working tree dirty for the recovery run to reconcile

#### Scenario: Recovery-forced run uses the recovery prompt role
- **WHEN** a junior/senior/ui lap is recovery-pending and the `recovery` route is selected
- **THEN** the runner SHALL compose the RECOVERY role prompt and tag the run/try telemetry as role `recovery`, while preserving the original lap assignee in queue/audit fields

#### Scenario: Handoff timeout routes to recovery
- **WHEN** a try resolves as `handoff_timeout`
- **THEN** the system SHALL route the next run for the lap to the `recovery` route

#### Scenario: Ordinary failure does not route to recovery
- **WHEN** a try resolves as `failed` (e.g. usage limit, provider overload, rate limit, or agent error) without a handoff
- **THEN** the system SHALL NOT route to recovery and SHALL handle the failure via the existing bench/route/rotate resilience paths

#### Scenario: Incomplete alone is unchanged
- **WHEN** a try produces file changes without finalizing and without any handoff
- **THEN** the system SHALL keep the existing incomplete retry path and SHALL NOT route to recovery

#### Scenario: Clean handoff alone is unchanged
- **WHEN** a try hands off cleanly with no meaningful own-uncommitted changes remaining
- **THEN** the system SHALL keep the existing follow-up flow and SHALL NOT route to recovery

#### Scenario: Missing recovery route does not deadlock
- **WHEN** a recovery trigger holds for a lap but no `recovery` route is configured
- **THEN** the system SHALL fall back to the lap's normal route and emit a warning

### Requirement: Recovery-pending derived from persisted records
The system SHALL determine whether a lap is recovery-pending from the persisted try records (`tries.jsonl`) rather than from in-memory relay state, so the routing decision survives a relay restart. A lap SHALL be recovery-pending when it is not done, its most-recent run ended `handoff_timeout` or a dirty handoff, **and** fewer than the recovery cap (2) consecutive recovery-route runs have already executed for the lap. Try records SHALL carry the lap ID, the `TryOutcome` needed to evaluate timeout triggers, the `DirtyHandoff` boolean needed to distinguish clean and dirty handoffs after restart, any `HandoffCreatedLapIDs` that should inherit the dirty-handoff recovery route when claimed at the queue head, and the **resolved route name** needed to count consecutive recovery runs. The lap's persisted `lap_assignee` SHALL NOT be used for this count, because it records the unsubstituted queue assignee and recovery routing is applied by in-relay assignee substitution without mutating the lap.

The system SHALL count the recovery cap by distinct consecutive `RunID`s for the lap, using each run's resolving try (the last try for that `RunID`), not by raw try rows; a single recovery run with multiple retry attempts SHALL count once. After a recovery run has executed for the lap, the most-recent-run condition SHALL no longer hold and selection SHALL return to the lap's normal route, unless a new trigger arises within the cap.

The system SHALL bound consecutive recovery runs per lap: when a recovery-route run itself resolves `handoff_timeout` or a dirty handoff, it SHALL re-arm recovery only until 2 consecutive recovery runs have executed for the lap. On reaching the cap the system SHALL stop routing the lap to recovery, SHALL raise a `needs_user` operator Issue (per the telemetry taxonomy), SHALL fall back to the lap's normal route, and SHALL NOT loop the lap back to recovery indefinitely. This cap-hit `needs_user` is a relay-synthesized operator signal and SHALL NOT be conflated with a RECOVERY agent's recorded `needs_user` classification; the cap-hit decision occurs at selection time with no recovery agent running and SHALL NOT write a recovery classification.

#### Scenario: Recovery state survives a restart
- **WHEN** a relay restarts and the head lap's persisted records satisfy a recovery trigger
- **THEN** the system SHALL route the next run for that lap to the `recovery` route without any in-memory carryover

#### Scenario: Recovery clears after a recovery run
- **WHEN** a recovery run has executed for a recovery-pending lap and resolved its outcome cleanly (not `handoff_timeout` or a dirty handoff)
- **THEN** the most-recent-run recovery condition SHALL no longer hold and the lap SHALL route normally unless a new trigger arises

#### Scenario: Repeated recovery failures stop at the cap
- **WHEN** 2 consecutive recovery-route runs for a lap each resolve `handoff_timeout` or a dirty handoff
- **THEN** the system SHALL stop routing the lap to recovery, SHALL raise a relay-synthesized `needs_user` operator Issue, SHALL NOT write a recovery classification, SHALL NOT mark the lap done or handed off, SHALL fall back to the normal route, and SHALL NOT loop the lap back to recovery

### Requirement: Recovery classification recorded
When a run executes under the `recovery` route, the system SHALL persist the RECOVERY agent's state classification — one of `continue`, `discard`, `course_correct`, `repair_plan`, or `needs_user` — on the try/run record as structured state, read from the agent's recorded wrapup output (a `Classification` field on the current-run summary entry, supplied through `laps wrapup --classification <value>`) and validated against that closed set. A non-recovery run SHALL leave the classification empty. The classification SHALL be recorded best-effort: an omitted or unrecognised value SHALL leave the field empty and SHALL NOT fail the run.

#### Scenario: Recovery run records a valid classification
- **WHEN** a recovery run records a classification within the closed set via `laps wrapup --classification`
- **THEN** the system SHALL persist that classification on the try/run record

#### Scenario: Non-recovery run has no classification
- **WHEN** a run executes under any non-recovery route
- **THEN** the recovery classification field SHALL be empty

#### Scenario: Invalid classification does not fail the run
- **WHEN** a recovery run records no classification or an unrecognised value
- **THEN** the system SHALL leave the field empty and SHALL NOT fail the run on that basis

