## Context

The runner's per-try attempt loop lives in `runOne` (`internal/relay/runner.go`,
loop at `runner.go:1218`). The selected runner (`picked`, a harness+model) is
fixed for the **whole** loop — every retry attempt of a run resumes/retries the
same runner; rotation to a different harness+model only happens *between* runs,
back in the routing dispatch loop. Each attempt launches `executeTry` on a
goroutine and selects in `runActionLoop` (`runner.go:988`) over: the try result
channel, the late-pid channel, the silence-based **stall** tick (`stallTick`,
driven by `reliability/stall.go`), and operator keyboard actions. Cancelling
`attemptCtx` (`runner.go:1344`) is the single lever that stops a running attempt.
There is **no wall-clock bound** on a try or a run: the stall detector only fires
on *log silence*, and retries are bounded by `maxAttempts` (`runner.go:1207`,
default 5 from `retry_budget`), not by time. A struggling runner can therefore
spend its entire retry budget — and hours of wall-clock — on one harness+model
before the run ends and the next run rotates (observed in `RALLY-2`: run 5 spent
~3 hours over 5 retries on a single opencode model before failing over).

Outcomes are not a first-class type. The executor returns `agent.TryResult{Completed
bool, …}`; `runOne` derives `failed`/`success` bools, a `failReason`, a
`reliability.FailureClass`, and a `reliability.FailureCategory`, persisting
`store.TryRecord{Completed bool, FailReason, Category}` (`records.go:4`) and
surfacing `runOutcome{Success, FailureClass, Category}` (`runner.go:1104`). The
stable `FailureCategory` taxonomy and its `categoryToClass` mapping live in
`reliability/category.go`. `improve-error-categorisation` added the
`terminalCategory` short-circuit (`runner.go:~1537`) and the routing loop acts on
the surfaced category (bench / route away — `runner.go:721-760`).

Routing is assignee-driven: `routeRuntime.next(task, resilience)`
(`route_runtime.go:176`) → `selector.ActiveRoute(routing.Lap{Assignee:
task.Assignee}, override)` (`routing/select.go:71`). Roles are embedded `.md`
snippets resolved by `agent_prompt.Role(role)` (`agent_prompt.go:74`) with an
on-disk override; the per-role snippet is injected only for a run whose assignee
matches that role. Honest session resume already exists (agent-lifecycle
"Pause-now and honest session resume").

## Goals / Non-Goals

**Goals:**
- A hard wall-clock budget on a run **across its retries**, so a struggling
  runner cannot grind for hours before the run resolves.
- On budget exhaustion, one bounded attempt to extract a clean handoff from the
  same session, then route to a fresh recovery session.
- A first-class `TryOutcome` lifecycle type so success-side and failure-side
  lifecycle states (`handoff_requested` vs `handoff_timeout`) are modelled
  cleanly, orthogonal to the `FailureCategory` failure-cause taxonomy.
- A RECOVERY role/route distinct from SENIOR and VERIFY, defaulting to a stronger
  model, that classifies and acts on **dirty leftover/handed-off** state.
- Rally-driven recovery routing for the two states that leave a half-finished,
  suspect tree: a **dirty handoff** (`DirtyHandoff`, derived from the current-run
  handoff entry plus `hasOwnUncommittedChanges`) and `handoff_timeout`. Derived from
  persisted try records so it survives relay restarts.

**Non-Goals:**
- Treating ordinary failures as a recovery trigger. A `failed` try (usage limit,
  provider instability, agent error) is **not** a recovery signal — it routes/
  benches/rotates through the existing resilience paths. RECOVERY is only for a
  dirty handed-off tree that needs reconciliation.
- **Fixing mid-run failover** (the run/retry model pinning all retries to one
  runner). This change adds a *time* bound that caps the damage, but rotating
  harness+model *within* a run on repeated runner-specific failures is a separate
  routing/resilience concern (see "Failover gap" below) and is proposed as its
  own change.
- Transcript spying to detect agent non-progress; replacing the stall detector;
  a timeout-tuning TUI (`build-new-tui`).

## Decisions

### 1. Per-run wall-clock budget across retries (primary), per-try cap (secondary)

The primary hard bound is a **per-run budget measured across all retry attempts**:
`run_timeout_secs` (config, default **4500** = 75 minutes), excluding the bounded
handoff phase (Decision 2). A secondary **per-try** cap, `try_timeout_secs`
(default **3600** = 60 minutes), guards against a single runaway attempt; the run
budget sits modestly above it so a quick non-blocking retry after a provider
dropout or network blip still has buffer. Both are
enforced via timers in/around `runActionLoop` (`runner.go:988`) as new select arms
mirroring `stallTick`: on fire they cancel `attemptCtx` (the existing path), mark
the attempt timed-out, drain `tryCh`, and break.

- A per-try cap firing with run budget remaining ends the attempt; the loop may
  start a fresh retry (same runner, per the existing model) within the remaining
  run budget.
- When the **run budget** is exhausted (the dominant "we've spent enough on this
  run" signal — and the one that catches both the multi-retry grind *and* a single
  pathological 90-minute try), the loop stops retrying and proceeds to the bounded
  handoff (Decision 2).

The per-run budget composes with the silence stall detector; whichever fires first
wins. Both knobs are retained (product decision): the per-try cap bounds any single
agent session at an hour, and the slightly larger run budget gives quick
non-blocking retries room after a transient blip.

### 2. Bounded handoff-only resume on budget exhaustion

When the per-run budget is exhausted, before the run resolves, `runOne` attempts
one bounded handoff-only continuation **iff** `exec.ResumeSupported()` and a
`sessionID` was captured:

1. Build a handoff-only prompt (agent-prompt; Decision 8) forbidding further
   implementation and instructing the agent to summarize the blocker and call
   `laps handoff` then `laps wrapup`.
2. Run one `executeTry` with `ResumeSessionID = sessionID`, a fresh `attemptCtx`
   bounded by `handoff_timeout_secs` (default **300** = 5 minutes, *not* counted in
   the run budget), and the handoff-only prompt.
3. The silence stall detector is not applied to this short phase.

Reuses the existing resume machinery — no new `Executor` method. A harness with
`ResumeSupported() == false`, or no captured session, skips straight to
`OutcomeHandoffTimeout`.

### 3. `TryOutcome` — a first-class lifecycle type, orthogonal to `FailureCategory`

Today the outcome is a derived `(Completed, failed, FailureCategory)` tuple, and
`FailureCategory` doubles as both "why it failed" and a lifecycle label. Stuffing a
*success* (`handoff_requested`) into a failure enum is a smell. Add a lifecycle
dimension:

```go
type TryOutcome string
const (
    OutcomeCompleted        TryOutcome = "completed"         // lap finalized (laps done)
    OutcomeHandoffRequested TryOutcome = "handoff_requested" // clean handoff+wrapup; success-side, lap not done
    OutcomeIncomplete       TryOutcome = "incomplete"        // own changes, not finalized
    OutcomeHandoffTimeout   TryOutcome = "handoff_timeout"   // bounded handoff recovery failed
    OutcomeFailed           TryOutcome = "failed"            // hard failure — cause is in FailureCategory
    OutcomeInterrupted      TryOutcome = "interrupted"       // operator stop
)
```

- `FailureCategory` keeps **only** its nine failure-cause values; `handoff_*` are
  *not* added to it. `categoryToClass`/freeze accounting are unchanged.
- Success / freeze / Issue / retry decisions read `TryOutcome` (and, for
  `OutcomeFailed`, the `FailureCategory→FailureClass` class).
- `OutcomeHandoffRequested` is success-side: not a failure, no freeze, never an
  Issue. Set only when **both** `laps handoff` and `laps wrapup` completed
  (Decision 5).
- `OutcomeHandoffTimeout` is failure-side but **non-freezing**: it does not feed
  the freeze counter and joins the terminal short-circuit set so the run makes no
  further same-runner attempts and control returns to routing.

`store.TryRecord` retains `Completed bool` (back-compat) and gains `Outcome
TryOutcome`; `runOutcome` carries `Outcome`. Telemetry emits the new `outcome` tag
alongside the existing `failure_category` tag (Decision 10); `FailureState`
(`telemetry/failure_state.go`) gains `Outcome` and `RecoveryClassification` fields,
surfaced through `FailureStateTags`.

### 4. Recovery routing — two triggers (dirty handed-off state only)

RECOVERY engages for the next run on a lap when the lap is not done **and** either:

1. **Dirty handoff** — a handoff completed through `laps wrapup` yet meaningful
   own-uncommitted changes remain (a suspect, half-finished tree). This is *not*
   the `incomplete` `TryOutcome`: in `runOne` `finalized := … || handoffState != 0
   || …` (`runner.go:1425`), so `incomplete := … && hasOwnUncommittedChanges &&
   !finalized` (`runner.go:1427`) is forced **false** whenever a handoff fires —
   the two are mutually exclusive. The trigger is therefore a distinct derived
   predicate, `handoffEntryForCurrentRun != nil && hasOwnUncommittedChanges`
   evaluated at try resolution, not the `incomplete` outcome. A plain `incomplete`
   outcome (changes, no handoff) keeps its existing resume-with-finalization retry;
   a clean `handoff` (no leftover dirt) keeps its existing follow-up flow.
2. **`OutcomeHandoffTimeout`** — incomplete by definition (the bounded handoff
   recovery did not finalize).

Ordinary `failed` tries are **not** a recovery trigger: a usage limit, provider
overload, rate limit, or agent error is handled by the existing bench/route/rotate
resilience paths, not by escalating to RECOVERY. RECOVERY exists specifically to
reconcile a dirty, handed-off tree — not to react to any failure.

`runOne` already computes `hasOwnUncommittedChanges` (`runner.go:1417-1424`, own
delta vs the start-of-try `dirtySnapshot`). Handoff completion MUST be read from
the durable current-run `progress.HandoffEntry` appended after
`summaryEntryCountBeforeRun`; the transient `HandoffState` is only reliable for
the partial/no-wrapup case because the wrapup path clears run-state. The
dirty-handoff trigger is persisted as `TryRecord.DirtyHandoff`, and
`handoff_timeout` is read from the resolving run's `TryOutcome`.

**Auto-commit is suppressed on a dirty handoff** (product decision). Today the
auto-commit gate at `runner.go:1434` (`dirtyBeforeAutoCommit && hasUserFileChanges
&& !incomplete && finalized`) fires on a handoff (because `finalized` is true and
`incomplete` is false), which would commit the leftover changes before the next run
starts. To give RECOVERY the *real* half-finished tree to reconcile, the gate MUST
additionally exclude the dirty-handoff case: when the current run has a durable
handoff entry and `hasOwnUncommittedChanges`, skip auto-commit and leave the working
tree dirty for the recovery run. A clean handoff with no leftover dirt is unaffected
because there is nothing to commit. This mirrors the existing `incomplete` branch,
which already suppresses auto-commit for the same reason.

### 5. `handoff_requested` requires both `laps handoff` and `laps wrapup`

`OutcomeHandoffRequested` is set only when the current run has a durable
`progress.HandoffEntry` from `AppendRunEntry`, which proves both `laps handoff` and
`laps wrapup` completed. A `laps handoff` without a completed `laps wrapup` is a
failed handoff: in the budget-exhaustion path it becomes `OutcomeHandoffTimeout`;
in the voluntary path it falls back to the existing incomplete/agent-error
handling. Detection uses entries appended after `summaryEntryCountBeforeRun`; it
does not rely on `HandoffState` for successful handoffs because the wrapup path
clears run-state after writing the summary entry.

### 6. Configuration under `[reliability]`

Three new keys join `stall_threshold_secs`/`retry_budget`/`liveness_probe`:

- `run_timeout_secs` — per-run wall-clock budget across retries; default **4500**
  (75m).
- `try_timeout_secs` — secondary per-try cap; default **3600** (60m).
- `handoff_timeout_secs` — bounded handoff-only resume; default **300** (5m), not
  counted in the run budget. Clamped to never reach/exceed the effective
  `try_timeout_secs`/`run_timeout_secs`.

`0`/unset yields the defaults. Parsing/defaulting lives with the existing
`[reliability]` config (`internal/config/config_v2.go`); the runner reads them off
`RunnerConfig` (`runner.go:40`). The config form (`internal/cli/config.go`) gains
the three fields.

### 7. Recovery-pending is derived from persisted try records — durable, no queue rewrite

RECOVERY is forced via an **in-relay assignee substitution that resolves the
`recovery` route**, **not** by rewriting the lap's `assignee` in `.laps/laps.json`
(a persisted side effect the agent did not author, leaking Rally routing into the
work queue — counter to the AGENTS.md tool boundary).

Mechanism caveat: `Selector.ActiveRoute(lap, override)`'s `override` arg is a
full-route **bypass** — when non-nil it returns that route verbatim and never
consults `s.routes["recovery"]` (`routing/select.go:71-81`). So recovery must be
forced by substituting the *assignee*, i.e. resolve
`ActiveRoute(routing.Lap{Assignee: "recovery"}, r.overrideRoute())` rather than
passing `recovery` as the `override`. This composes correctly with the existing
relay-wide `--route` override that `routeRuntime.next` already forwards
(`route_runtime.go:177`): if an operator pinned a relay-wide override it still
wins, otherwise the substituted `recovery` assignee resolves the configured
`recovery` route.

Route substitution must also surface an **effective assignee/prompt role** to the
runner. The original lap assignee remains persisted as `TryRecord.LapAssignee`
for queue/audit history, but a recovery-forced run uses `EffectiveAssignee =
"recovery"` for role prompt resolution (`roles/recovery.md`), run/try telemetry
role tags, recovery-classification gating, and any prompt-composition tests. Without
this field, Rally could select a recovery runner for a junior lap while still
injecting the JUNIOR prompt, silently bypassing the RECOVERY contract.

The recovery-pending *state* is **derived from the try records Rally already
persists** (`tries.jsonl`), not an in-memory flag:

> A head lap is recovery-pending when it is not done, its most-recent run ended
> `OutcomeHandoffTimeout` or a **dirty handoff** (`TryRecord.DirtyHandoff`),
> **and** fewer than 2 consecutive recovery-route runs have already executed for it
> (the anti-loop cap below).

`TryRecord` already carries `LapID` (`records.go:22`), `Completed`, and (after
Decision 3) `Outcome`; this change also adds `DirtyHandoff bool` so a clean
`handoff_requested` and a dirty handoff remain distinguishable after restart. For a
dirty handoff that created follow-up laps at the queue head, the try record also
copies `HandoffCreatedLapIDs []string` from the durable handoff entry. Those
created followups are treated as recovery-continuation targets for the same dirty
tree, so the next claimed head followup is routed through RECOVERY instead of
bypassing the original dirty handoff. The query is therefore computable from the
store at selection time and survives relay restarts for free, with no laps.json
mutation. This **follows the same replay-derived pattern** as resilience state —
`Resilience.GetState` replays `agent_status.jsonl` rather than holding in-memory
flags — but does **not** reuse that store: resilience state is keyed by
`harness:model` (a `ResilienceKey`), whereas recovery-pending is per-lap, so
`tries.jsonl` (which has `LapID`) is the correct source of truth. While recovery is
pending the dispatch loop does not advance the queue past the dirty tree: if the
claimed head is the original dirty lap or a `HandoffCreatedLapIDs` followup from
that dirty handoff, it is recovery-forced. This handles the existing `laps add
head` behavior, where handoff followups can appear ahead of the original lap. Once
a recovery run has executed for the original lap or one of its handoff-created
followups, the most-recent-run condition no longer holds and selection returns to
normal — **subject to the anti-loop cap below**. A missing `recovery` route falls
back to the lap's normal route with a warning (a config gap must not deadlock the
relay).

**Anti-loop cap (product decision).** A recovery run can itself resolve
`handoff_timeout` or leave another dirty handoff, which would re-arm the same
trigger and route the lap to RECOVERY forever. The recovery-pending query therefore
also counts the **consecutive recovery-route runs already executed for the lap**.
This count cannot use `TryRecord.LapAssignee` — that field stores the lap's
*unsubstituted* queue assignee (`task.Assignee`, `runner.go:1625`, e.g. `junior`),
and Decision 7 deliberately substitutes the assignee only at route resolution
without mutating the lap. A new `ResolvedRoute string` field is therefore persisted
on `TryRecord` (set from `selection.Route.Name`, available at ~`runner.go:614`).
Because `tries.jsonl` is per attempt, the cap counts distinct consecutive `RunID`s
for the lap by first reducing each run to its resolving try (the last try for that
`RunID`), then checking whether that resolving try has `ResolvedRoute == "recovery"`.
Up to **2** consecutive recovery runs are allowed; once that cap is reached the lap is **no
longer** recovery-pending — recovery routing stops, Rally raises a `needs_user`
operator Issue (Decision 10), and the lap falls back to its normal route so the
relay does not loop. (This Rally-synthesized `needs_user` is an operator-attention
signal, distinct from a RECOVERY *agent's* `needs_user` classification recorded on a
real recovery try — see Decision 9; the cap-hit decision happens at selection time
with no recovery agent running, so it does not write `RecoveryClassification` and
the cap-hit run carries no recovery try.) A recovery run that resolves cleanly
(`completed`/`handoff_requested`/plain `incomplete`/`failed`) resets the count by no
longer satisfying a trigger.

### 8. RECOVERY role prompt and the handoff-only prompt

Two new agent-facing prompt artifacts:

- **`internal/agent_prompt/roles/recovery.md`** — reasoning-heavy like VERIFY but
  with authority to modify code and reconcile dirty state like SENIOR. Embeds the
  five-way classification contract (`continue`/`discard`/`course_correct`/
  `repair_plan`/`needs_user`), instructs classify-then-*act* (never stop at
  diagnosis unless `needs_user`), allows follow-up laps as containment without
  dodging recovery, and requires recording the classification (Decision 9) and
  finishing via `laps done`/`laps handoff` + `laps wrapup`. Stays OpenSpec-agnostic.
- **The handoff-only prompt** (a `general/` snippet) — forbids continuing
  implementation; directs the agent to summarize blocker, hypotheses, evidence,
  changed files, and the next decision, then `laps handoff` + `laps wrapup`.

The five-iteration voluntary-handoff rule is added to the shared
implementation-role guidance consumed by junior/senior/ui; VERIFY and RECOVERY are
reasoning roles and do not receive it.

### 9. Structured recovery classification, captured via the wrapup channel and surfaced only on RECOVERY runs

Per product call, the RECOVERY agent's classification is captured as structured
state this release:

- Add `RecoveryClassification string` to `store.TryRecord` (surfaced on the run
  record), validated against `{continue, discard, course_correct, repair_plan,
  needs_user}`; empty for non-recovery runs.
- **Capture channel:** the RECOVERY agent records it through the existing laps
  wrapup flow, regardless of whether it finishes with `laps done` or `laps handoff`.
  Add a `Classification` field to `progress.RunEntry` plus a
  `rally progress --classification <value>` flag that `laps wrapup` forwards. The
  runner reads the current-run summary entry and validates it. Reuses the channel
  that already captures wrapup context rather than inventing a side-channel, while
  avoiding a handoff-only field that would lose classifications on `laps done`.
- **The instruction to record a classification lives in `roles/recovery.md`
  only.** Because the per-role snippet is injected solely for a run whose effective
  assignee/prompt role is `recovery`, the classification field is referenced in the composed
  prompt **only on RECOVERY runs** — never on JUNIOR/SENIOR/UI/VERIFY runs. The
  shared/general snippets and other role docs MUST NOT mention it. This is the
  dynamic, role-scoped behavior to verify in tests (Decision 8 / task 7.5).
- Recorded best-effort: an omitted/unrecognised value leaves the field empty and
  never fails the run. Telemetry attaches `recovery_classification` (Decision 10).

### 10. Telemetry and Sentry grouping for the new lifecycle

Extends the existing agent-state-on-failure tags and Issue taxonomy, not
duplicates:

- Each try event carries a new `outcome` tag (`TryOutcome`) and, for
  `OutcomeFailed`, the existing `failure_category` tag (the real key emitted by
  `FailureStateTags`, `telemetry/failure_state.go`/`tags.go:73` — **not** a new
  `category` tag) — so handoff lifecycle events group on their own outcome
  instead of collapsing into rate-limit/harness Issues (the exact mislabelling seen
  in `RALLY-2`, where a lap's whole failure history collapsed into one
  "rate limit, waiting 1m" Issue).
- `handoff_timeout` and `handoff_requested` are **not** Issues — spans/logs.
- A RECOVERY try attaches `recovery_classification`. A `needs_user` classification
  MAY be captured as an Issue; the other four are spans/logs.

## Failover gap (investigation — proposed as a separate change)

Root-caused from `RALLY-2`: between-run failover works (the lap cycled antigravity
→ claude → opencode/qwen → opencode/kimi across runs). The miss is **within a run**:
`runOne` pins all retries to the one selected `picked` runner, and `short_rate_limit`
maps to `StrategyWaitResume` (wait, retry *same* runner). So run 5 waited-and-retried
`qwen3.7-max` five times across ~3 hours despite a healthy alternative in the lane,
only rotating once the budget was spent. Cases where mid-run failover is missed:

- Any `StrategyWaitResume` category (short_rate_limit, provider_overloaded) retries
  the same runner up to `retry_budget` times — never rotates mid-run even when the
  lane has a healthy peer.
- `agent_error` (default `StrategyFreshRestart`) likewise restarts the same runner.
- Only specific patterns map to `StrategyRotate` (which sets `skipFlag`, ends the
  run, and lets the dispatch loop pick a different entry). There is no
  "rotate after N consecutive runner-specific failures" rule.

The per-run budget (Decision 1) **caps the wasted time** but does not itself rotate.
A proper fix — ending a run early / rotating harness+model when repeated
runner-specific failures occur and a healthy alternative exists, while still letting
`incomplete_finalization` resume the same session — is a routing/resilience change
distinct from this one. **Recommendation: handle it in a separate OpenSpec change**
(it touches the strategy→action mapping and the run/retry boundary broadly). This
change deliberately does not alter mid-run rotation.

## Risks / Trade-offs

- **`run_timeout_secs` (75m) vs `try_timeout_secs` (60m) vs `retry_budget` (5).**
  The run budget is the dominant bound; the per-try cap bounds any single session
  at an hour. The 15m gap between them is the buffer for a quick non-blocking retry
  after a transient blip — small by design, not a second full attempt.
- **`TryOutcome` adds a dimension** alongside `FailureCategory`; `Completed bool` is
  retained for back-compat.
- **Per-run budget excludes the handoff phase**, so worst-case wall clock per run is
  `run_timeout_secs + handoff_timeout_secs` (≈ 80m by default).
- **Deriving recovery-pending from records** assumes try records are written before
  the next selection (they are — `AppendTry` happens in finalization before the loop
  continues).
- **The failover gap remains** after this change (capped in time, not fixed); a
  separate change is recommended.

## Open Questions

- Whether `handoff_timeout` should escalate to an Issue after repeated recoveries on
  the same lap (partly addressed by the recovery cap in Decision 7, which escalates
  to `needs_user` once the cap is reached; further per-lap Issue escalation deferred).
- Scope/ownership of the mid-run failover fix (separate change vs folded in later).

(Resolved: keep both `run_timeout_secs` and `try_timeout_secs` with defaults 75m /
60m; suppress auto-commit on a dirty handoff so RECOVERY gets the real dirty tree;
cap consecutive recovery runs per lap at 2 before escalating to `needs_user`.)
