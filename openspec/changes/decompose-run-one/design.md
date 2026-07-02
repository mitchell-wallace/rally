# Design — decompose-run-one

Behaviour-preserving refactor of the `internal/relay/runner` orchestration core.
No observable behaviour, CLI, telemetry, or store change; no version bump; no
release. Baselined against commit `f55712c` (2026-07-02), after
`modularize-harness-adapters` (#4). **Sequenced after
`separate-runtime-presentation-boundary` (#5)** — regenerate every line count
and function inventory at implementation time, because #5 moves rendering code
out of the exact files this change splits.

## Context

`decompose-relay-runner` (#1) gave the runner its package and named the run/try
phases as methods; `runOne` already reads as a table of contents delegating to
`prepareRunAttempt` → `runMonitoredAttempt` → (`resolveAttemptFinalSnippet`) →
`reconcileAttemptProgress` → `classifyAttemptOutcome` → `recordAttemptOutcome` →
`decideRetryOrComplete`, plus `recordCancelledAttempt`,
`runHandoffContinuation`, and `finalizeRunProgress`. But all of those bodies are
inlined in one 1,510-line `run_one.go` (the #3 flagship grandfather entry), with
`route_runtime.go` (752) and `relay_steps.go` (526) in the warning band beside
it. Current phase-body sizes (2026-07-02): `prepareRunAttempt` ~120,
`runMonitoredAttempt` ~101, `reconcileAttemptProgress` ~105,
`recordCancelledAttempt` ~131, `classifyAttemptOutcome` ~229,
`recordAttemptOutcome` ~261, `decideRetryOrComplete` ~86,
`finalizeRunProgress` ~51.

## Goals / Non-Goals

**Goals:**

- The package directory answers "where does X live" for the run/try loop: one
  responsibility-named file per phase, `run_one.go` reduced to the `runOne`
  index and `runOneState` constructors.
- The two deep phase bodies (`classifyAttemptOutcome`, `recordAttemptOutcome`)
  become scannable via named unexported sub-step helpers.
- `route_runtime.go` and `relay_steps.go` get the same treatment behind their
  existing entry points.
- The #3 grandfather baseline loses its flagship production entry.

**Non-Goals:**

- No behaviour, telemetry, store, laps, or CLI change of any kind.
- No exported-API change (`relay-module-structure` surface intact).
- No child package (see Decision 1).
- No test-file splitting (owned by #8, which mirrors this change's layout).
- No harness or non-runner file work (#4 landed; #7 owns the rest).

## Decisions

### Decision 1 — same-package file split, no `attempt` child package

Resolves the draft's first open question. The run/try phases share the
`Runner`/`runOneState` receiver surface and mutate runner state; carving a
child package (e.g. `internal/relay/runner/attempt`) would force exporting (or
interface-wrapping) that state for no consumer benefit. Per the carried-over
deep-module principle: file split first, promote only when a clean interface
has emerged. This mirrors what #2 did to `config_v2.go`.

### Decision 2 — target file layout

Same `package runner`, responsibility-named files behind the `runOne` index
(names indicative; verify function inventory at implementation time):

```text
run_one.go                  # Runner.runOne + runOneState.outcome (index only)
run_one_state.go            # (pre-existing from #5, left untouched) newRunOneState, captureRunStartWorkspaceState
run_attempt_prepare.go      # setupRunBudget, prepareRunAttempt
run_attempt_monitor.go      # runMonitoredAttempt, executeTry, resolveAttemptFinalSnippet
run_attempt_reconcile.go    # reconcileAttemptProgress
run_attempt_classify.go     # classifyAttemptOutcome + extracted sub-steps
run_attempt_record.go       # recordAttemptOutcome + extracted sub-steps
run_attempt_cancel.go       # recordCancelledAttempt
run_retry_decide.go         # decideRetryOrComplete, routeFallbackCause.addTo
run_handoff.go              # runHandoffContinuation
run_finalize.go             # finalizeRunProgress
```

> Re-grounded post-#5: `#5` already split `newRunOneState` and
> `captureRunStartWorkspaceState` out of `run_one.go` into the new sibling
> `run_one_state.go`. They are therefore NOT in `run_one.go` to move, and
> `run_one_state.go` is left exactly as #5 placed it. `run_one.go`'s index thus
> reduces to `Runner.runOne` + `runOneState.outcome`; `run_attempt_prepare.go`
> receives `setupRunBudget` + `prepareRunAttempt` (no `captureRunStartWorkspaceState`).
> The per-phase-file DECISION itself is unchanged — every phase body still moves
> verbatim to its named file.

Small utilities (`containsInt`) move with their only caller. Every symbol keeps
exactly one home; no `misc`/`helpers` catch-all file — the same rule the
`relay-module-structure` spec already imposes on the #1 split.

### Decision 3 — deep phase bodies get named sub-steps, everything else moves verbatim

`classifyAttemptOutcome` (~229) and `recordAttemptOutcome` (~261) are the only
bodies whose *internal* structure changes: their distinct concerns (e.g.
classification inputs assembly vs taxonomy application; store/progress record
vs telemetry emission) are extracted into named **unexported, package-local**
helpers within their phase file, preserving statement order, error strings,
telemetry fields, and control flow. `recordCancelledAttempt` (~131) may receive
the same treatment only if a clean seam presents itself; do not force it. All
other functions move verbatim — no signature or behaviour edits. Alternative
considered — rewriting the phases around a narrower state struct: rejected,
that is redesign risk with no findability payoff beyond the file split.

### Decision 4 — `route_runtime.go` split behind the `routeRuntime` type

```text
route_runtime.go            # routeRuntime type, accessors (quotaScope, Warnings, applyProviders), routeSelectionError
route_runtime_construct.go  # newRouteRuntimeFromConfig/FromStoredLabel, legacy-mix/override/resolved constructors, entry resolution (legacyMixRouteEntries, resolveRouteEntries, resolveAgentSpec, agentRouteSpec, cloneParsedEntries)
route_runtime_select.go     # next, selectionWaitError, prepareExecutorForSelection, joinRouteWarnings, overrideRoute
route_runtime_recovery.go   # syncRecoverySignals, hasProbationEventForCurrentFreeze, persistProbationEvent, forceUnpauseAll
route_runtime_bench.go      # benchQuotaScope, benchResetAt, resilienceKeyForEntry, resolvedEntryAgent, roleForScheduler
```

Functions move verbatim (no sub-step extraction needed — the largest bodies,
`next` ~100 and `selectionWaitError` ~93, are cohesive selection logic and stay
whole). The resilience vocabulary (**stall** / **frozen** / **benched**) is
untouched.

### Decision 5 — split `relay_steps.go` now

Resolves the draft's second open question: split it in this change rather than
leaving a 526-line advisory warning. Rationale: the package is already open,
its four concerns are clean, and leaving it warns forever under #3. Candidate
layout (re-ground after #5, which relocates `printRelaySummary` rendering and
the route-warning stderr writes):

```text
relay_steps.go              # startOrResumeRelay, startRelaySpan/startRunSpan, consume*ScopedMessage (relay-level index)
relay_route_wait.go         # selectRouteOrWait, emitFallbackEvents, resolveFallbackCause
relay_run_progress.go       # updateRunProgress, updateSkippedRunProgress, applyRunOutcomeToResilience, completeRelayIfTargetMet
relay_summary.go            # printRelaySummary (or its post-#5 event-emitting successor), tallyRuns
```

### Decision 6 — guardrail ratchet, no archguard spec change

After the split, regenerate the grandfather map (`go run ./tools/archguard
--report`): the `run_one.go` 1,510 production entry disappears and no new
`internal/relay/runner` production file may need an entry — target every new
file under the 500-line warning, hard-require under the 800 budget. The runner
`_test.go` grandfather entries are explicitly untouched (owned by #8). This is
a policy-baseline update consistent with the `architecture-guardrails` spec;
that spec is not modified (same precedent as #4).

### Decision 7 — spec delta lives in `relay-module-structure`

The structural contract extends the existing `relay-module-structure`
capability (an ADDED requirement for the phase-file decomposition) rather than
creating a new capability. That spec already owns "responsibility-named
decomposition" for this package; a sibling spec would fragment ownership.

## Risks / Trade-offs

- **[Risk] Mechanical move silently changes behaviour** (dropped defer, changed
  receiver, reordered statement) → move functions whole per commit, keep
  `git diff --color-moved` reviewable, and run the race suite
  (`go test -race -shuffle=on -count=1 ./internal/relay/...`) after each phase
  of the split.
- **[Risk] Sub-step extraction in classify/record alters control flow** (early
  returns, shared mutable locals) → extract only straight-line regions with
  explicit inputs/outputs; where a region reads/writes many locals, pass the
  existing `runOneState`/attempt structs rather than inventing new ones; error
  strings and telemetry fields byte-identical.
- **[Risk] Stale layout vs #5** — #5 rehomes rendering out of these files, so
  the inventories above will drift → re-run the function inventory
  (`grep -n "^func"`) at implementation start; the *decisions* (per-phase
  files, sub-step extraction, verbatim moves) hold regardless of exact
  membership.
- **[Risk] #8's test mirror drifts** → this change records its final file list
  in the tasks checklist so #8 can mirror it one-for-one.
- **Trade-off**: more files in an already file-heavy package (~20 production
  files → ~29). Accepted: the directory listing is the index we want; archguard
  holds the per-file budgets.

## Migration Plan

Pure refactor inside one package; land as a short stack of move-only commits
(run_one phases → route_runtime → relay_steps → guardrail regen), each leaving
the tree green. Rollback is `git revert` of any commit — no data, config, or
release surface involved.

## Open Questions

_None — the draft's two open questions are resolved by Decisions 1 and 5._

## Re-grounding — post-#5 (tasks 1.2 / 1.3)

Recorded at the implementation-time baseline gate (after #5
`separate-runtime-presentation-boundary` landed). The `f55712c` line counts in
the Context/proposal are intentionally left as the *historical* baseline; the
numbers here are the live, re-grounded values.

### Baseline gate (task 1.1) — all GREEN

- `go build ./...` — exit 0
- `go vet ./...` — exit 0
- `gofmt -l .` — empty (exit 0)
- `go test -count=1 ./internal/relay/...` — ok (relay, runner, runtimeevent)
- `go run ./tools/archguard --ci` — exit 0 (grandfathered warn-band entries
  reported, none denied; no `runner` production file is over the hard cap)

### Re-grounded line counts (task 1.2)

| file | baseline `f55712c` | post-#5 (live) | Δ |
| --- | --- | --- | --- |
| `run_one.go` | 1,510 | **1,476** | −34 |
| `route_runtime.go` | 752 | **752** | 0 |
| `relay_steps.go` | 526 | **547** | +21 |

Phase-body sizes are essentially unchanged from baseline (post-#5 spans):
`prepareRunAttempt` 120, `runMonitoredAttempt` 106, `reconcileAttemptProgress`
105, `recordCancelledAttempt` 132, `classifyAttemptOutcome` **229**,
`recordAttemptOutcome` **262**, `decideRetryOrComplete` 89, `finalizeRunProgress`
51. The two deep bodies (Decisions 3) are unchanged, so the sub-step extraction
plan holds.

### Drift vs the artifacts (task 1.2)

1. **`captureRunStartWorkspaceState` + `newRunOneState`** — `#5` already moved
   these OUT of `run_one.go` into the new sibling `run_one_state.go` (still
   called from `run_one.go:225` / `run_one.go:231`). Decision 2's table is
   reconciled above: `run_one.go` index drops to `runOne` + `outcome`;
   `run_attempt_prepare.go` gets `setupRunBudget` + `prepareRunAttempt` only;
   `run_one_state.go` is left untouched. The per-phase-file DECISION holds.
2. **`setupRunBudget`** — still in `run_one.go` (line 295). No drift; moves to
   `run_attempt_prepare.go` as planned.
3. **`printRelaySummary`** — still in `relay_steps.go` (line 481), but its body
   was transformed by `#5` from operator-facing stdout/stderr rendering into
   event emission (`r.eventSink().Emit(RelaySummaryReady{...})` +
   `RelayCompleted{...}`). Decision 5's parenthetical "(or its post-#5
   event-emitting successor)" anticipated this exactly. Membership unchanged.
4. **Route-warning stderr writers + `style`/`keyboard`** — confirmed GONE from
   all three files. No `style`/`keyboard` imports remain; the only `os.Stdout`
   is `mon.Start(os.Stdout)` at `run_one.go:486` (the documented `#5` residual —
   monitor status-line wiring stays runner-driven). All remaining
   `fmt.Fprint*` calls write to the relay `log` writer, not stderr/stdout. No
   membership-table consequence (these were inline writes, not functions).
5. **`route_runtime.go`** — zero drift. The live function inventory matches
   Decision 4's membership table exactly. No edit.
6. **`relay_steps.go`** — membership matches Decision 5; the `printRelaySummary`
   event-emit note is confirmed. No table edit beyond the existing parenthetical.
7. **`containsInt` (subtlety)** — defined in `run_one.go:1469`, but its ONLY
   callers are `updateRunProgress` in `relay_steps.go` (lines 441, 453). The
   "move small utilities with their only caller" rule therefore routes it into
   the **`relay_steps` split** (`relay_run_progress.go`, with
   `updateRunProgress`), NOT a `run_one` phase file.

### Pre-change function inventory (task 1.3) — name → file

`run_one.go` (16 funcs):
`routeFallbackCause.addTo` · `runOneState.outcome` · `Runner.runOne` ·
`Runner.setupRunBudget` · `Runner.prepareRunAttempt` · `Runner.runMonitoredAttempt` ·
`Runner.resolveAttemptFinalSnippet` · `Runner.reconcileAttemptProgress` ·
`Runner.recordCancelledAttempt` · `Runner.classifyAttemptOutcome` ·
`Runner.recordAttemptOutcome` · `Runner.decideRetryOrComplete` ·
`Runner.runHandoffContinuation` · `Runner.finalizeRunProgress` ·
`Runner.executeTry` · `containsInt` (only callers live in `relay_steps.go`).

`route_runtime.go` (29 funcs):
`routeRuntime.quotaScope` · `routeRuntime.applyProviders` · `routeRuntime.Warnings` ·
`routeSelectionError.Error` · `newRouteRuntimeFromConfig` ·
`newRouteRuntimeFromStoredLabel` · `newLegacyMixRouteRuntime` ·
`newOverrideRouteRuntime` · `newOverrideRouteRuntimeWithReasoning` ·
`newResolvedRouteRuntime` · `newResolvedRouteRuntimeWithReasoning` ·
`routeRuntime.next` · `joinRouteWarnings` · `routeRuntime.overrideRoute` ·
`routeRuntime.syncRecoverySignals` · `routeRuntime.hasProbationEventForCurrentFreeze` ·
`persistProbationEvent` · `routeRuntime.selectionWaitError` ·
`routeRuntime.forceUnpauseAll` · `routeRuntime.benchQuotaScope` ·
`routeRuntime.resilienceKeyForEntry` · `routeRuntime.resolvedEntryAgent` ·
`routeRuntime.roleForScheduler` · `routeRuntime.benchResetAt` ·
`legacyMixRouteEntries` · `resolveRouteEntries` · `resolveAgentSpec` ·
`agentRouteSpec` · `cloneParsedEntries` · `Runner.prepareExecutorForSelection`.

`relay_steps.go` (14 funcs):
`Runner.startOrResumeRelay` · `Runner.startRelaySpan` ·
`Runner.consumeRelayScopedMessage` · `Runner.selectRouteOrWait` ·
`Runner.startRunSpan` · `Runner.emitFallbackEvents` ·
`Runner.consumeRunScopedMessage` · `Runner.resolveFallbackCause` ·
`Runner.updateSkippedRunProgress` · `Runner.applyRunOutcomeToResilience` ·
`Runner.updateRunProgress` · `Runner.completeRelayIfTargetMet` ·
`Runner.printRelaySummary` · `tallyRuns`.

Reference: post-#5 `internal/relay/runner` production files (for tasks 5.5 / #8):
`action_loop.go`, `failure_display.go`, `final_snippet.go`, `git.go`,
`handoff_only.go`, `liveness.go`, `log.go`, `progress.go`, `relay_steps.go`,
`route_runtime.go`, `runner.go`, `run_one.go`, `run_one_state.go`, `task.go`,
`telemetry.go`, `terminal.go` (16 production files).
