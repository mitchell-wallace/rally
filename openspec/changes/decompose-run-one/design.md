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
run_one.go                  # Runner.runOne + newRunOneState/runOneState.outcome (index + state)
run_attempt_prepare.go      # captureRunStartWorkspaceState, setupRunBudget, prepareRunAttempt
run_attempt_monitor.go      # runMonitoredAttempt, executeTry, resolveAttemptFinalSnippet
run_attempt_reconcile.go    # reconcileAttemptProgress
run_attempt_classify.go     # classifyAttemptOutcome + extracted sub-steps
run_attempt_record.go       # recordAttemptOutcome + extracted sub-steps
run_attempt_cancel.go       # recordCancelledAttempt
run_retry_decide.go         # decideRetryOrComplete, routeFallbackCause.addTo
run_handoff.go              # runHandoffContinuation
run_finalize.go             # finalizeRunProgress
```

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
