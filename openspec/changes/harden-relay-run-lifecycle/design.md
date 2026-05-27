## Context

This change converts the solid findings of the `Prayer-app` black-box review
into rally fixes. The raw evidence lives in `qa-report/`, `qa-report-2/`, and
`qa-suggestion/`; those reports are explicitly black-box (no rally/laps source
was read), so each finding below was re-grounded against the current code before
being scoped in. Findings that were QA inference rather than fact (a "monthly
org cap" reading of a `five_hour` limit, an E2BIG "resource leak" theory, a
"Codex violated its VERIFY role" claim) are treated as motivation only.

Three distinct "frozen"-flavored systems exist and must not be conflated — and
this change renames them so they read distinctly (see Decision 9):

- **Liveness detector** (`internal/reliability/freeze.go`): a per-try watchdog
  that kills a single stuck process based on log-silence plus connection/IO
  signals. Already well-targeted (it keys on the rate-limit/network-stall shape)
  and self-clearing. Behavior unchanged; **renamed freeze→stall** for clarity.
- **Per-agent-type circuit breaker** (`internal/relay/resilience.go`): the
  persisted state machine over `agent_status.jsonl`. `frozen` is terminal —
  cleared only by a successful hourly retry that a frozen agent can never get
  (`resilience.go getState`) — and is re-applied verbatim on resume
  (`route_runtime.go` `syncRecoverySignals`). **This is the lockout. Changed
  here.** Keeps the name "frozen" (user-facing; the `event_type` is persisted).
- **Scheduler entry state** (`internal/routing/scheduler.go`): per route-entry
  availability that drives lane rotation (`EntryState.Frozen`/`Exhausted`).
  Behavior unchanged; **`Frozen` renamed to `Benched`** to separate it from the
  agent-type freeze that sets it via `syncRecoverySignals`.

The current `relay-runner` and `store` specs actively mandate the buggy
behavior ("frozen for the remainder of the relay"; "still frozen from a previous
relay"), so the fixes are genuine MODIFIED requirements, not just code edits.

## Goals / Non-Goals

**Goals**
- A run's recorded state faithfully reflects the work it did (no phantom lap
  completions; VERIFY success means a real verdict).
- A freeze is always recoverable: bounded decay, re-evaluated on resume, and
  an explicit `--new` reset.
- Only genuine infra failures push a harness toward freeze.
- The assembled prompt cannot grow unbounded.

**Non-Goals**
- Retuning the liveness detector thresholds (it is not the cause).
- stdin/file prompt transport (deferred; argv is best-supported across harnesses
  today — revisit once telemetry shows the real size breakdown).
- A `rally reconcile` command (rejected; correctness should be intrinsic).
- Moving/renaming `agent_status.jsonl` or reworking record shapes — that is
  `tidy-rally-runtime-data-storage`'s job; coordinate, don't duplicate.

## Decisions

**1. Lap-ID pinning is the primary state-integrity guard.**
Pin the assigned lap ID(s) when a run starts; on `laps wrapup`/finalization,
compare recorded-completed laps against the pinned set. Mismatch → fail the run
with `wrong_lap_consumed`/`multi_lap_consumed` and do not advance the queue.
This directly prevents the phantom-completion that consumed `pray-43a5`. Logging
every attempted lap (not just completed) falls out of this naturally.

**2. Completion file-change cross-check is opt-in.**
Only enforced when a lap declares expected file paths. Absent that field,
behavior is unchanged. Avoids false rejections for laps whose work is not a
simple file edit (docs, investigation, config).

**3. Freeze decay over a bounded window; re-evaluate on resume.**
Add a `FreezeDuration` so `getState` returns active/probation once
`now > frozenSince + FreezeDuration`, mirroring the existing paused-decay at
`route_runtime.go`. `syncRecoverySignals` re-evaluates rather than re-applies
frozen verbatim. Alternative considered: keep frozen terminal but add a manual
`unfreeze` command — rejected as the same "CLI to fix internal state" smell that
killed `rally reconcile`.

**4. `--new` resets agent status by design.**
`rally start --new` appends `active` events (or truncates) for all harnesses so a
fresh relay is deterministically clean. Today `--new` only ends the old relay and
recovered by timing luck; make it intentional.

**5. Failure classification gates the breaker, not just retry strategy.**
`internal/reliability/patterns.go` `ClassifyError` already exists but only steers
in-attempt retry strategy. Extend it with an infra/agent distinction; in
`runner.go`, only call `PauseAgent`/`RecordHourlyFailure` for infra-classified
failures (rate-limit, harness/launch error such as `argument list too long`,
API timeout/network). Agent-logic errors and short no-op tries still fail and
retry but do not increment the freeze counter. Keep the pattern table the single
update point.

**6. Hourly retries get more than one attempt.**
`runner.go` sets `maxAttempts=1` on the hourly retry; allow 2–3 so a transient
failure during the once-per-hour probe does not burn a freeze life. Pairs with
decision 3 so freeze is both harder to reach and self-healing.

**7. Role-aware freeze-recovery.**
The "files committed → success" recovery is unsafe for VERIFY (it produced a
false success while the real gap persisted). A frozen VERIFY try requires a
verification verdict artifact before being treated as success; implementation
roles keep current behavior.

**8. Bounded prompt context.**
`runner.go` already caps to `RecentTries(5)` but concatenates each summary in
full. Add a configurable run count (default ~5) plus per-summary and overall
character budgets with head/tail truncation, and min/max bounds for terse vs
verbose outliers. Per-source size telemetry is emitted by the Sentry sink in
`tidy-rally-runtime-data-storage`; this change only enforces the budget.

**9. Naming disambiguation (clarity refactor).**
Because "freeze" currently names three unrelated things, rename while reworking
them: the liveness detector freeze→**stall** (`StallDetector`,
`Assessment.Stalled`, "stalled try"; recovery becomes "stall-recovery"), and the
scheduler `EntryState.Frozen`→**`Benched`**. The persisted per-agent-type
`frozen` keeps its name (and `agent_status.jsonl` `event_type` value) to avoid a
data-format change. Pure rename — no behavior change beyond what Decisions 3–7
already specify. Separately, `FallbackConfig`/`loadFallbackInstructions` (the
default prompt for a laps-less, promptless run) is misnamed but is owned by
`cli-polish` (#4) as `FreeRunPrompt`, not this change.

## Risks / Trade-offs

- **Freeze decay lets a genuinely-broken harness retry forever** → bound it with
  the same pause/freeze escalation, just non-terminal; document the window.
- **Misclassifying an agent error as infra (or vice versa)** → the pattern table
  is the single, testable update point; default unknown failures to the
  non-infra (does-not-freeze, still-retries) side to avoid premature lockout.
- **Lap pinning false-positives on legitimate multi-lap runs** → only the
  pinned-vs-completed comparison fails; if multi-lap completion is ever intended,
  it must be expressed as multiple pinned laps, not silent consumption.
- **Coordination drift with `tidy`** → record-shape additions (commit list,
  laps-attempted) are deferred to `tidy`; this change must not fork the shapes.
