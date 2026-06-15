## MODIFIED Requirements

### Requirement: Failure detection
The system SHALL consider a try failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes). The system SHALL assign each failure a stable `FailureCategory` (see "Failure taxonomy and evidence") and SHALL map that category onto one of three resilience classes:
- **infra-class**: short rate limit, provider overload, harness/launch error (e.g. `argument list too long`, `fork/exec`), transient infrastructure error (`transient_infra`: API timeout, network/connection/TLS failure, non-overload 5xx), or liveness stall detection.
- **agent-class**: ordinary agent error or short no-op.
- **incomplete**: file changes were produced but the agent did not finalize the lap (`laps done` or `laps handoff`).

Long usage/quota exhaustion (`usage_limit`), invalid-model/config (`invalid_model`), and authentication/proxy (`auth_or_proxy`) failures SHALL NOT be classified infra-class; they are handled by benching/routing and SHALL NOT increment the pause/freeze counter. A try whose outcome is `handoff_timeout` (see "Try outcome lifecycle") SHALL likewise NOT be classified infra-class and SHALL NOT increment the pause/freeze counter.

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

#### Scenario: Handoff-timeout outcome is not infra-class
- **WHEN** a try resolves with a `handoff_timeout` outcome
- **THEN** the system SHALL NOT classify it infra-class, SHALL NOT increment the pause/freeze counter, and SHALL NOT treat it as a usage-limit or harness failure

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

## ADDED Requirements

### Requirement: Try outcome lifecycle
The system SHALL classify every try with a stable `TryOutcome` lifecycle value, orthogonal to the `FailureCategory` failure-cause taxonomy: `completed` (lap finalized via `laps done`), `handoff_requested` (a successful handoff — both `laps handoff` and `laps wrapup` completed — with the lap not yet done), `incomplete` (the try produced own file changes but did not finalize), `handoff_timeout` (the bounded handoff recovery did not finalize), `failed` (a hard failure whose cause is carried by `FailureCategory`), or `interrupted` (operator stop). `FailureCategory` SHALL retain only its failure-cause values and SHALL NOT be extended with lifecycle labels. Success, freeze, retry, and Issue decisions SHALL be driven by `TryOutcome` (and, for a `failed` outcome, the category's resilience class), so no consumer treats a failure-cause category as a success.

`handoff_requested` SHALL be a successful outcome: it SHALL NOT increment the pause/freeze counter, SHALL NOT be treated as a harness/usage/rate/agent/infra failure, and SHALL NOT be captured as an Issue. `handoff_timeout` SHALL be a non-freezing failure outcome that does not feed the freeze counter. The persisted try record SHALL store the `TryOutcome` while retaining the existing `Completed` boolean for compatibility.

#### Scenario: Completed lap records completed outcome
- **WHEN** a try finalizes its lap via `laps done`
- **THEN** the try outcome SHALL be `completed`

#### Scenario: Handoff success records handoff_requested
- **WHEN** a try completes both `laps handoff` and `laps wrapup`
- **THEN** the try outcome SHALL be `handoff_requested`, which SHALL NOT count toward pause/freeze and SHALL NOT be captured as an Issue

#### Scenario: Outcome does not collide with failure category
- **WHEN** a try resolves with a lifecycle outcome (`handoff_requested`, `handoff_timeout`, `incomplete`, `completed`, `interrupted`)
- **THEN** the system SHALL NOT add that value to `FailureCategory`, and a `failed` outcome SHALL be the only one that carries a `FailureCategory` cause

### Requirement: Run/try timeout and bounded handoff recovery
The system SHALL enforce a hard wall-clock budget on a run measured **across all of its retry attempts** (`run_timeout_secs` under `[reliability]`, default 4500 seconds / 75 minutes), independent of the silence-based stall detector, so a struggling runner cannot grind for hours before the run resolves. The system SHALL additionally enforce a secondary per-attempt cap (`try_timeout_secs`, default 3600 seconds / 60 minutes). Whichever bound (run budget, per-try cap, or stall detector) fires first SHALL cancel the running attempt via the existing graceful-shutdown path. A per-try cap firing with run budget remaining MAY be followed by a fresh retry within the remaining budget; exhaustion of the **run budget** SHALL stop further retries and proceed to the bounded handoff. The bounded handoff phase SHALL NOT be counted against the run budget.

When the run budget is exhausted, the system SHALL attempt exactly one bounded handoff-only recovery **iff** the harness reports `ResumeSupported()` and a session ID was captured: it SHALL resume that same session with a handoff-only prompt under a separate hard bound (`handoff_timeout_secs` under `[reliability]`, default 300 seconds / 5 minutes). The handoff-only phase SHALL NOT continue implementation; its only goal is for the agent to summarize the blocker and call `laps handoff` followed by `laps wrapup`. The handoff window SHALL never exceed the per-try cap.

A bounded handoff recovery SHALL record the try outcome as `handoff_requested` **only when both** `laps handoff` and `laps wrapup` completed. If the harness cannot resume, no session was captured, or the handoff-only phase fails, times out, or completes `laps handoff` without a completed `laps wrapup`, the system SHALL record the try outcome as `handoff_timeout`.

#### Scenario: Run budget across retries stops grinding
- **WHEN** a run's cumulative wall-clock across its retry attempts reaches `run_timeout_secs`
- **THEN** the system SHALL cancel the active attempt via graceful shutdown, stop further retries, and proceed to the bounded handoff (treating it as neither a stall nor an ordinary agent error)

#### Scenario: Per-try cap stops a single runaway attempt
- **WHEN** a single attempt runs past `try_timeout_secs` while run budget remains
- **THEN** the system SHALL cancel that attempt and MAY start a fresh retry within the remaining run budget

#### Scenario: Resumable harness gets a bounded handoff-only continuation
- **WHEN** the run budget is exhausted, the harness reports `ResumeSupported()`, and a session ID was captured
- **THEN** the system SHALL resume that session once with a handoff-only prompt bounded by `handoff_timeout_secs` and SHALL NOT permit continued implementation in that phase

#### Scenario: Successful bounded handoff records handoff_requested
- **WHEN** the bounded handoff-only phase completes both `laps handoff` and `laps wrapup`
- **THEN** the system SHALL record the try outcome as `handoff_requested`

#### Scenario: No resume support records handoff_timeout
- **WHEN** the run budget is exhausted and the harness does not support resume or no session ID was captured
- **THEN** the system SHALL record the try outcome as `handoff_timeout` and SHALL route the next run for the lap to RECOVERY

#### Scenario: Failed or partial handoff records handoff_timeout
- **WHEN** the bounded handoff-only phase fails, times out, or completes `laps handoff` without a completed `laps wrapup`
- **THEN** the system SHALL record the try outcome as `handoff_timeout`

#### Scenario: Timeout outcomes do not feed the freeze counter
- **WHEN** a try resolves as `handoff_requested` or `handoff_timeout`
- **THEN** the system SHALL NOT increment the infra freeze counter and SHALL NOT treat the outcome as a usage-limit, rate-limit, or harness failure

### Requirement: Recovery routing triggers
The system SHALL route the next run for a lap to the `recovery` route when the lap is not done **and** either recovery trigger holds:
1. the lap's resolving run was a **dirty handoff** — a handoff occurred (`laps handoff`) yet meaningful own-uncommitted changes remain. This is a derived predicate (`handoffState != 0` together with own-uncommitted changes at try resolution), distinct from the `incomplete` `TryOutcome`, which a handoff makes unreachable because any handoff marks the try finalized;
2. the lap's resolving run ended with a `handoff_timeout` outcome.

Recovery is specifically for reconciling a dirty, handed-off tree. An ordinary `failed` try (e.g. usage limit, provider overload, rate limit, agent error) SHALL NOT trigger recovery; such failures SHALL be handled by the existing bench/route/rotate resilience paths. An `incomplete` outcome without a handoff SHALL keep its existing resume-with-finalization-guidance retry path and SHALL NOT, on its own, trigger recovery. A clean handoff that finalized with no meaningful leftover dirty state SHALL keep its existing follow-up flow and SHALL NOT trigger recovery.

Recovery routing SHALL be applied as an in-relay route override that resolves the `recovery` route by assignee, and SHALL NOT rewrite the lap's `assignee` in the work-queue file. While a recovery trigger holds for the head lap, the system SHALL NOT advance the queue past that lap before a recovery run executes. If no `recovery` route is configured, the system SHALL fall back to the lap's normal route and emit a warning rather than deadlocking the relay.

#### Scenario: Dirty handoff routes to recovery
- **WHEN** a try hands off (`laps handoff`) but leaves meaningful own-uncommitted changes
- **THEN** the system SHALL route the next run for that lap to the `recovery` route without advancing the queue past the lap

#### Scenario: Dirty handoff is not auto-committed
- **WHEN** a try hands off (`laps handoff`) but leaves meaningful own-uncommitted changes (a dirty handoff)
- **THEN** the system SHALL NOT auto-commit those leftover changes and SHALL leave the working tree dirty for the recovery run to reconcile

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
The system SHALL determine whether a lap is recovery-pending from the persisted try records (`tries.jsonl`) rather than from in-memory relay state, so the routing decision survives a relay restart. A lap SHALL be recovery-pending when it is not done, its most-recent run ended `handoff_timeout` or a dirty handoff, **and** fewer than the recovery cap (2) consecutive recovery-route runs have already executed for the lap. Try records SHALL carry the lap ID and the `TryOutcome` needed to evaluate the triggers, and the **resolved route name** needed to count consecutive recovery runs. The lap's persisted `lap_assignee` SHALL NOT be used for this count, because it records the unsubstituted queue assignee and recovery routing is applied by in-relay assignee substitution without mutating the lap; the system SHALL instead persist the resolved route on each try record and count tries whose resolved route is `recovery`. After a recovery run has executed for the lap, the most-recent-run condition SHALL no longer hold and selection SHALL return to the lap's normal route, unless a new trigger arises within the cap.

The system SHALL bound consecutive recovery runs per lap: when a recovery-route run itself resolves `handoff_timeout` or a dirty handoff, it SHALL re-arm recovery only until 2 consecutive recovery runs have executed for the lap. On reaching the cap the system SHALL stop routing the lap to recovery, SHALL raise a `needs_user` operator Issue (per the telemetry taxonomy), SHALL fall back to the lap's normal route, and SHALL NOT loop the lap back to recovery indefinitely. This cap-hit `needs_user` is a relay-synthesized operator signal and SHALL NOT be conflated with a RECOVERY agent's recorded `needs_user` classification; the cap-hit decision occurs at selection time with no recovery agent running and SHALL NOT write a recovery classification.

#### Scenario: Recovery state survives a restart
- **WHEN** a relay restarts and the head lap's persisted records satisfy a recovery trigger
- **THEN** the system SHALL route the next run for that lap to the `recovery` route without any in-memory carryover

#### Scenario: Recovery clears after a recovery run
- **WHEN** a recovery run has executed for a recovery-pending lap and resolved its outcome cleanly (not `handoff_timeout` or a dirty handoff)
- **THEN** the most-recent-run recovery condition SHALL no longer hold and the lap SHALL route normally unless a new trigger arises

#### Scenario: Repeated recovery failures stop at the cap
- **WHEN** 2 consecutive recovery-route runs for a lap each resolve `handoff_timeout` or a dirty handoff
- **THEN** the system SHALL stop routing the lap to recovery, SHALL resolve it as `needs_user` (surfaced as an operator Issue), and SHALL NOT loop the lap back to recovery

### Requirement: Recovery classification recorded
When a run executes under the `recovery` route, the system SHALL persist the RECOVERY agent's state classification — one of `continue`, `discard`, `course_correct`, `repair_plan`, or `needs_user` — on the try/run record as structured state, read from the agent's recorded wrapup/handoff output (a `Classification` field on the handoff entry) and validated against that closed set. A non-recovery run SHALL leave the classification empty. The classification SHALL be recorded best-effort: an omitted or unrecognised value SHALL leave the field empty and SHALL NOT fail the run.

#### Scenario: Recovery run records a valid classification
- **WHEN** a recovery run records a classification within the closed set via its wrapup/handoff output
- **THEN** the system SHALL persist that classification on the try/run record

#### Scenario: Non-recovery run has no classification
- **WHEN** a run executes under any non-recovery route
- **THEN** the recovery classification field SHALL be empty

#### Scenario: Invalid classification does not fail the run
- **WHEN** a recovery run records no classification or an unrecognised value
- **THEN** the system SHALL leave the field empty and SHALL NOT fail the run on that basis
