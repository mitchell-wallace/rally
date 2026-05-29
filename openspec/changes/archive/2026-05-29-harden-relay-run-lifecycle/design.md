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
  completions; a stalled VERIFY is not silently blessed as success).
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
frozen event older than `FreezeDuration` (default 5h, hardcoded constant), it
returns `StateProbation` rather than `StateActive`. A probationary agent:
- Is eligible for exactly one run per probation cycle. The one-shot is enforced
  by `syncRecoverySignals`: after unbenching the scheduler entry to allow the
  probation run, the entry is immediately re-benched so it cannot be selected
  again. When the run resolves, `syncRecoverySignals` reflects the new state
  (active on success, frozen on failure).
- On success (any non-failed run, including incomplete): promoted to `StateActive`.
- On failure (agent-class or infra-class): re-frozen with a fresh timestamp,
  restarting the decay window. Incomplete results do NOT re-freeze — they're a
  behavioral/progress issue, not a model-availability issue.
- Gets `maxAttempts=3` (same as hourly retries). A single transient blip
  shouldn't keep an otherwise-usable agent frozen; retries are cheap compared
  to lost agent time.
- On resume/start: freeze state is re-evaluated (via `getState`, which remains
  a pure read function) rather than re-applied verbatim. The probation event
  (`event_type: "probation"`) is persisted exactly once in `syncRecoverySignals`
  when it first observes a key transitioning from `StateFrozen` to
  `StateProbation`.

**4. `--new` explicitly truncates agent status.**
`rally start --new` truncates agent status history via `store.ResetAgentStatus()`
so every harness-model pair starts deterministically active. Today `--new` only
ends the old relay and recovered by timing luck; make it intentional.

**5. Failure classification at per-harness-model granularity with a >1 infra threshold.**
Extend `internal/reliability/patterns.go` `ClassifyError` with an
infra/agent/incomplete distinction. The failure classes are:
- **infra-class**: rate-limit, harness/launch error (`argument list too long`,
  `fork/exec`), API timeout/network stall, liveness-stall detection.
- **agent-class**: ordinary agent errors, short no-op tries (<3min, no changes).
- **incomplete**: file changes were produced but the agent did not finalize the
  lap (no `laps done`/`laps handoff`).

In `runner.go`, `PauseAgent`/`RecordHourlyFailure` are called only when >1
attempt within a run is classified as infra-class. A single infra failure retries
without escalation. Agent-class and incomplete failures fail the try and retry
but do NOT increment the freeze counter.

Incomplete tries leave their file changes *uncommitted* — the auto-commit is
suppressed. The retry agent inherits the working-tree changes as partial progress
and receives prompt guidance: "The last run was incomplete. Check any current git
changes, finish anything not done, verify correctness, commit when good, then run
`laps done`."

Rate-limit flags are tracked per harness-model pair (not per harness). The key
for all resilience operations is `harness:model`. This means an opencode runner
using multiple providers (e.g. kimi + gemini models) does not freeze the entire
opencode harness when only one provider hits its rate limit. Every method in
`Resilience` (`getState`, `PauseAgent`, `RecordHourlyFailure`, `FreezeAgent`,
`UnpauseAgent`, `SelectActiveAgent`) and every caller in `runner.go` and
`route_runtime.go` must be updated to thread the model through. Define a
`ResilienceKey` type (`{Harness, Model}`) to keep signatures clean. The
`AgentStatusEvent` gains an optional `model` field; `GetAgentStatus` always
filters on both `agent_type` and `model` when model is present.

**6. Hourly retries get up to 3 attempts.**
`runner.go` sets `maxAttempts=3` on the hourly retry path (was 1). Pairs with
decision 3 so freeze is both harder to reach (more retries per hourly cycle) and
self-healing (probation decay with 3-attempt runway).

**7. Role-aware stall-recovery: VERIFY is excluded from files-committed recovery.**
The "files committed → success" stall-recovery is unsafe for VERIFY. VERIFY's job
is to verify, not produce committed work, and it may legitimately commit only a
trivial fix (its role allows small clearly-correct edits), so "files were
committed" is not evidence the verification actually happened. A stalled VERIFY
try is therefore NOT auto-accepted on the basis of commits — it stays a
retry-eligible failure and is retried/resumed (session resume from #4/#5 lets it
pick up rather than restart). Implementation roles keep the current
files-committed recovery. We considered a dedicated verdict artifact
(`verify-reports.jsonl`) to gate VERIFY success, but rejected it: a verdict is
essentially a more targeted progress summary (redundant with the summary the
agent already writes), forcing it mid-run would pressure verification quality,
and "stalled-but-passed" is not a state VERIFY needs — simply failing and
resuming is cleaner and correct.

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

**10. Bench-trigger uses enum, not string-matching.**
Replace `failureFreezesEntry`'s substring-scanning (`strings.Contains(reason, "freeze")`)
with an explicit enum or boolean parameter on `OnAgentFailed`. The current
implicit behavior (only `"freeze"` benches, `"retry-budget-exhausted"` does not)
is preserved but made explicit. The rename to `failureBenchesEntry` (task 9.2)
is the natural place for this refactor.

**11. Resilience constants centralized in a single location.**
`FreezeDuration` (5h), `PauseDuration` (1h), `HourlyRetriesBeforeFreeze` (5),
and `HourlyRetryMaxAttempts` (3) stay hardcoded (not in `config.toml`) but are
centralized in a `constants.go` or similar single-location file rather than
spread across `resilience.go` and `runner.go`.

**12. `RecordHourlyFailure` counting loop guards against new event types.**
The backward-counting loop that tallies `retry_failed` events currently breaks
only on `active`. It must also break on `frozen` and `probation` to avoid
counting across state-transition boundaries from different freeze cycles.

## Risks / Trade-offs

- **Freeze decay lets a genuinely-broken harness retry forever** → probation
  bounds this: a probationary agent gets exactly one run enforced by
  `syncRecoverySignals` re-benching the scheduler entry after unbenching; if it
  fails, it's re-frozen with a fresh timestamp, resetting the decay window.
  Incomplete results move back to active (they're progress issues, not
  availability issues).
- **Misclassifying an agent error as infra (or vice versa)** → the pattern table
  is the single, testable update point; default unknown failures to the agent
  side to avoid premature lockout. The >1 infra threshold further reduces the
  blast radius of a single misclassification.
- **Probation + scheduler interaction** → `syncRecoverySignals` unbenches the
  entry for probation then immediately re-benches it so the scheduler blocks
  re-entry. When the run resolves, the new state (active or frozen) is reflected.
  If the entry has alternatives (route fallback), the scheduler cycles normally.
  If it's the only entry, the relay waits for the next probation check (same
  cadence as paused hourly retries).
- **Lap pinning false-positives on legitimate multi-lap runs** → only the
  pinned-vs-completed comparison fails; if multi-lap completion is ever intended,
  it must be expressed as multiple explicitly assigned laps, not silent
  consumption. Wrongly-consumed laps cannot be automatically recovered by rally
  (the external laps queue was already mutated); rally logs the pinned and
  consumed lap IDs for manual operator or VERIFY-run recovery.
- **Per-harness-model keying adds state complexity** → the key is `harness:model`
  (a `ResilienceKey` type); `agent_status.jsonl` gets a new optional `model`
  field. Callers always provide model when available; `GetAgentStatus` filters on
  both. Every resilience method signature changes — the scope is approximately 10
  method signatures and 20+ call sites, explicitly enumerated in the tasks.
- **`agent_status.jsonl` 50-event window can drop freeze timestamps** → bump the
  window to 500 events and, when truncating, synthesize a summary event preserving
  the latest effective state and timestamp for any active frozen/probation entries,
  so `getState` always reconstructs correctly regardless of window.
- **Incomplete runs leave uncommitted changes for the retry agent** → this is
  intentional; the retry inherits partial progress with prompt guidance to finish
  and commit. If this proves too noisy in practice, a future `auto-squash` flag
  could revert incomplete changes before retry.
