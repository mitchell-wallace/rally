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
- A freeze is always recoverable: bounded decay to probation, re-evaluated on
  resume, and an explicit `--new` reset.
- Only repeated genuine infra failures push a harness toward freeze.
- Agent-class failures and "incomplete" runs retry without escalating the
  resilience cascade.
- Rate-limit tracking is per-harness-model, not per-harness.
- The assembled prompt cannot grow unbounded.

**Non-Goals**
- Retuning the liveness detector thresholds (it is not the cause).
- stdin/file prompt transport (deferred; argv is best-supported across harnesses
  today — revisit once telemetry shows the real size breakdown).
- A `rally reconcile` command (rejected; correctness should be intrinsic).
- Moving/renaming `agent_status.jsonl` — location rework is `tidy-rally-runtime-data-storage`'s
  job; this change adds fields but keeps the file where it is.
- Enforcing expected file paths on lap completion (culled — too fragile for a
  harness-side check; agent-facing "Files & scope" instructions remain in
  `prepare-laps` for the agent to read).

## Decisions

**1. Lap-ID pinning is the primary state-integrity guard.**
Pin the assigned lap ID when a run starts; on `laps wrapup`/finalization,
compare recorded-completed laps against the pinned ID. Mismatch → fail the run
with `wrong_lap_consumed`/`multi_lap_consumed` and do not advance the queue.
This directly prevents the phantom-completion that consumed `pray-43a5`.
Every attempted lap (not just completed) is recorded on the try record with a
timestamp so multi-lap consumption is traceable. Note: this is detection+containment,
not full prevention — the external laps queue may already be mutated by the time
rally detects the mismatch. The pinning ensures rally does not silently proceed.

**2. "Incomplete" failure class for file-changes-without-finalization.**
When a try produces file changes (commits) but the agent neither calls `laps done`
nor `laps handoff`, the try is classified as "incomplete" rather than "failed."
This is a softer failure class: the run retries but the incomplete result does
NOT count toward the pause/freeze cascade. This replaces the previous "completion
file-change cross-check" concept, which was culled as too fragile for harness-side
enforcement.

**3. Freeze decays to probation; probation is a distinct tentative-active state.**
Add `StateProbation` to the resilience state machine. When `getState` sees a
frozen event older than `FreezeDuration` (default 5h), it returns `StateProbation`
rather than `StateActive`. A probationary agent:
- Is eligible for exactly one run per probation cycle (not continuously scheduled).
- On success: promoted to `StateActive` (unfreeze).
- On failure: re-frozen (with a fresh timestamp, restarting the decay window).
- On resume/start: freeze state is re-evaluated (via `getState`) rather than
  re-applied verbatim; `syncRecoverySignals` reflects probation in the scheduler
  the same way it reflects paused — the agent gets one shot, and the outcome
  determines the next state.

**4. `--new` explicitly truncates agent status.**
`rally start --new` resets agent status history for all harnesses so every
harness-model pair starts deterministically active. This is a store-level
truncation, not an append of `active` events. Today `--new` only ends the old
relay and recovered by timing luck; make it intentional.

**5. Failure classification at per-harness-model granularity with a >1 infra threshold.**
Extend `internal/reliability/patterns.go` `ClassifyError` with an
infra/agent/incomplete distinction. The failure classes are:
- **infra-class**: rate-limit, harness/launch error (`argument list too long`,
  `fork/exec`), API timeout/network stall, liveness-stall detection.
- **agent-class**: ordinary agent errors, short no-op tries (<3min, no changes).
- **incomplete**: file changes were committed but the agent did not finalize the
  lap (no `laps done`/`laps handoff`).

In `runner.go`, `PauseAgent`/`RecordHourlyFailure` are called only when >1
attempt within a run is classified as infra-class. A single infra failure retries
without escalation. Agent-class and incomplete failures fail the try and retry
but do NOT increment the freeze counter.

Rate-limit flags are tracked per harness-model pair (not per harness). This means
an opencode runner using multiple providers (e.g. kimi + gemini models) does not
freeze the entire opencode harness when only one provider hits its rate limit.
The key for rate-limit tracking is `harness:model`.

**6. Hourly retries get up to 3 attempts.**
`runner.go` sets `maxAttempts=3` on the hourly retry path (was 1). Pairs with
decision 3 so freeze is both harder to reach (more retries per hourly cycle) and
self-healing (probation decay).

**7. Role-aware stall-recovery requires a verdict artifact for VERIFY.**
The "files committed → success" stall-recovery is unsafe for VERIFY. A stalled
VERIFY try requires a verification verdict artifact in `.rally/state/verify-reports.jsonl`
(append-only JSONL, consistent with existing patterns) before being treated as
success. The artifact records at minimum: `lap_id`, `verdict` (pass/fail),
`timestamp`, `relay_id`. If the artifact is absent and the try was stalled, it
remains a retry-eligible failure regardless of commits. Implementation roles
keep the current files-committed recovery.

**8. Bounded prompt context.**
`runner.go` already caps to `RecentTries(5)` but concatenates each summary in
full. Add configurable run count (default 5, in `[reliability]` config) plus
per-summary and overall character budgets with head/tail truncation, and min/max
bounds for terse vs verbose outliers. Per-source size telemetry is emitted by
the Sentry sink in `tidy-rally-runtime-data-storage`; this change only enforces
the budget.

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

- **Freeze decay lets a genuinely-broken harness retry forever** → probation
  bounds this: a probationary agent gets exactly one run; if it fails, it's
  re-frozen with a fresh timestamp, resetting the decay window. Persistent
  failure stays frozen.
- **Misclassifying an agent error as infra (or vice versa)** → the pattern table
  is the single, testable update point; default unknown failures to the agent
  side to avoid premature lockout. The >1 infra threshold further reduces the
  blast radius of a single misclassification.
- **Probation + scheduler interaction** → `syncRecoverySignals` treats probation
  like paused: the scheduler entry gets one attempt. If the entry has alternatives
  (route fallback), the scheduler cycles normally. If it's the only entry, the
  relay waits for the next probation check (same cadence as paused hourly retries).
- **Lap pinning false-positives on legitimate multi-lap runs** → only the
  pinned-vs-completed comparison fails; if multi-lap completion is ever intended,
  it must be expressed as multiple explicitly assigned laps, not silent consumption.
- **Per-harness-model keying adds state complexity** → the key is `harness:model`;
  `agent_status.jsonl` gets a new optional `model` field. Falls back gracefully:
  if `model` is absent, the key is just `harness` (backward-compatible).
