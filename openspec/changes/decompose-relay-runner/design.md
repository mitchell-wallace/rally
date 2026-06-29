## Context

`internal/relay/runner.go` accreted every responsibility of the relay engine into
one 3,782-line file. The package also holds well-factored primitives —
`relay.go` (relay-record lifecycle), `resilience.go` (the freeze/bench/pause state
machine), `mix.go` (agent-mix parsing), `constants.go` (resilience timing),
`log.go` (per-relay log + repo identity) — plus a large, exhaustive test suite
(`runner_test.go` ~6,915 lines and a dozen focused `runner_*_test.go` files).

The dominant mass is two functions:

| Function | ~Lines | Role |
| --- | --- | --- |
| `runOne` | 1,221 | The run/try lifecycle: budget, monitored execution, outcome classification, final-snippet, laps reconciliation, retry decision. |
| `Run` | 465 | The relay loop: start/resume, message consumption, route selection + fallback, resilience update, progress, summary. |
| `runBoundedHandoffOnly` | 273 | Recovery-role bounded continuation. |

### The boundary is already obvious

The original draft said deeper package extraction "should wait until same-package
sharding has made the real boundaries obvious." The dependency graph disproves
that — the boundaries are obvious *now*:

- `relay.go`, `resilience.go`, `mix.go`, `constants.go` reference **zero**
  orchestrator-side symbols (`Runner`, `runTask`, `runOne`, …).
- `log.go`'s helpers (`repoKey`, `repoDisplayName`, `openRelayLog`) are consumed
  **only** by `runner.go`.
- `route_runtime.go` couples to the orchestrator through a single thread: its
  `next(task runTask, …)` takes `runTask`, a runner concept. Its only *exported*
  symbol is `FormatMixLabel` (a mix-formatting helper).
- `FormatMixLabel` and route runtime also share two private persisted-label tokens
  (`__routes__`, `__override__:`). The split must preserve those exact stored
  strings and their display semantics without adding exported token constants.
- Externally, only `cmd/rally/main.go` imports the package, and just two of its
  references move (`relay.NewRunner`, `relay.Config`).

So the cut is clean and one-directional: **`internal/relay/runner` → `internal/relay`**.
Keeping everything in `package relay` would force every carved file to be moved a
second time when the boundary is finally drawn. We draw it now.

### Deep modules, the right way round

A Philosophy of Software Design warns against **shallow** modules — units whose
interface cost rivals the complexity they hide. The runner is the opposite of
shallow: an enormous implementation behind a tiny interface (`NewRunner` +
`Runner.Run`). Giving it its own package makes that depth explicit and pays a
near-zero interface tax (a one-way import, two relocated call sites). The internal
carving then makes the depth *navigable*. The genuinely deep modules the rest of
the sequence wants are built **on** this boundary:

- `action_loop.go` + `liveness.go` (operator control / stall detection) → the
  operator-control boundary in `separate-runtime-presentation-boundary` (#5).
- `run_one.go` / `relay_steps.go` (run/try and relay-iteration lifecycle) → the
  presentation-neutral runtime #5 and the future TUI render.
- `telemetry.go` → a stable provider-neutral surface (New Relic).
- `task.go` / `progress.go` (laps + role + prompt bridge) → the confined
  laps-coupling seam referenced by the role rename (#6) and `prepare-laps`.

## Goals / Non-Goals

**Goals:**

- A dedicated `internal/relay/runner` package with a one-way dependency on
  `internal/relay`'s primitives.
- `runner.go` explains the top-level relay flow and little else (~250–400 lines).
- Every file answers one architectural question; every symbol has one home.
- `runOne` (1,221 lines) and `Run` (465 lines) decomposed into named steps.
- Tests split beside the code they exercise, one small shared fixtures file.
- Zero behaviour change: identical CLI output, telemetry fields, store shape, laps
  semantics, git messages; the exported API is **relocated, not redesigned**.

**Non-Goals:**

- Any package extraction beyond `runner` (pushing presentation/harness/config out
  is owned by #2/#4/#5).
- New interfaces or abstraction layers inside the runner (the carve is by file,
  not by interface).
- CI file-size / import-boundary budgets (owned by `add-architecture-guardrails`
  #3; this change only creates the edge it enforces).
- Behaviour, performance, TUI, harness, or role changes.

## Package boundary

| Package | Files | Exported surface |
| --- | --- | --- |
| `internal/relay` (primitives, stays) | `relay.go`, `resilience.go`, `mix.go` (← gains `FormatMixLabel`), `constants.go` | `CreateRelay`, `ResumeRelay`, `CompleteRelay`, `Resilience`, `NewResilience`, `ResilienceKey`, `KeyFromAgent`, `AgentState`/`State*`, `AgentMix`, `ParseAgentMix`, `Resolver`, `FormatMixLabel`, the resilience-timing consts |
| `internal/relay/runner` (orchestrator, new `package runner`) | `runner.go`, `route_runtime.go`, `log.go` + the carved files below | `Config`, `Runner`, `NewRunner`, `Runner.Run`, `Runner.SetTelemetry`, `Runner.RequestStop`, `CancellationSource` (+ consts) |

`runner` imports `relay` for `Resilience`/`ResilienceKey`/`AgentMix`/`Resolver`/
the consts. `relay` imports nothing from `runner`. The boundary is acyclic and
verified (Phase A compiles before any carving).

## File manifest (inside `internal/relay/runner`)

`package runner`, bare responsibility names throughout (no `runner_` qualifier —
a filename never collides with an imported package).

### Lifecycle spine (relay › run › try)

| File | Owns (representative) | Answers |
| --- | --- | --- |
| `runner.go` | `Config`, `Runner`, `NewRunner`, `RequestStop`, `SetTelemetry`, `tel`, `outWriter`, `newBoundTimer`, slimmed `Run` skeleton | What is a `Runner`, and what is the shape of the relay loop? |
| `relay_steps.go` | `Run`'s extracted iteration steps (start/resume, relay- & run-scoped message consumption, route select/wait, fallback emit, apply-outcome-to-resilience, update-progress, print-summary), `tallyRuns` | What happens in one relay iteration? |
| `run_one.go` | slimmed `runOne` + its extracted run-level steps, `runOutcome`, `routeFallbackCause`(+`addTo`), `executeTry`, `containsInt` | What is the lifecycle of one run (its tries) and how is its outcome resolved? |
| `action_loop.go` | `tryResult`, `actionMonitor`, `actionLoopDeps`, `actionLoopResult`, `CancellationSource`(+consts/`String`), `forceKillGroup`, `drainTimedOut`, `runActionLoop`, `drainOperatorCancellation` | How are operator keypresses, timeouts, stalls, and cancellation reconciled during a try? |
| `liveness.go` | `stallCheckInterval`, `newStallController`, `buildLivenessProbe` | How is per-try stall/liveness detection wired? |
| `handoff_only.go` | `noHandoffResumeReason`, `buildHandoffOnlyPrompt`, `runBoundedHandoffOnly` | How does a bounded handoff-only recovery run behave? |

### Cross-cutting helpers (by responsibility)

| File | Owns (representative) | Answers |
| --- | --- | --- |
| `terminal.go` | `renderRunFooter`, `waitOutcome`(+consts), `waitWithCountdown`, `waitLoop`, `formatRemaining` | How is the run footer/countdown rendered and how do operator waits resolve? |
| `failure_display.go` | `formatCategorizedDisplay`, `usageResetDuration`, `formatHoursMinutes`, `formatMinutesSeconds`, `benchResetDeadline` | How are failure categories and reset deadlines formatted for the operator? |
| `telemetry.go` | `applyTags`, `rallyContext`, `applyRallyContext`, `rallyFailure`, `failureStateEvent`, `limitSignalEvent`, `runnerLimitCategory`, `applyEvidenceToFailureState`, `applySafeExecErrorEvidence`, `addFailureEvidenceTelemetry`, `lapPinMismatchDiagnosticEvent`, `agentStateName`, `firstNonEmpty`, `resolvedRunnerModel`, `lastOutputAge` | How are relay telemetry spans, events, and evidence assembled? |
| `task.go` | `runTask`(+`promptAssignee`), `headPullLap`, `queueSize`, `errQueueEmpty`, `resolveRunTask`, `resolveInstructions`, `loadFreeRunPrompt`, `resolveRoleInstructions`, free-run / incomplete-retry prompt consts, `buildRecentContext`, `recentContextStatus` | How is the next lap/role/prompt resolved and the recent-context prompt built? |
| `git.go` | `commitLeftoverSummary`, `headHash`, `commitRange`, `autoCommit`, `filesChangedList`, `nonEmptyLines` | How is a try's work committed and its file-change list computed? |
| `final_snippet.go` | final-snippet consts, `normalizeFinalSnippet`, `progressSummaryEntryCount`, `recordedWrapupSummaryForRun`, `readTryLog`, `boundedFinalSnippetTail`, `finalSnippetErrorIndicator`, `readLastNLines` | How is the run's final snippet derived and bounded? |
| `progress.go` | `newProgressRunState`, `storeLapAttempts`, `mergeStrings`, `hasDirtyChangesSince`, `handoffCreatedLapIDs`, `recoveryClassificationForRun`, `progressLapsCompletedForRun`, `progressRunEntryLapIDs`, `pinnedLapCompleteElsewhere`, `lapDoneInLapsState`, `stringSliceContains`, `recordedHandoffEntryForRun`, `handoffEntryFromRunEntry`, `recordedRunEntryForRun`, `tryOutcomeForAttempt`, `validatePinnedLap`, `detectLapsMarkerInText`, `maybeWriteStubAndClearState` | How is laps/progress state validated and reconciled for a run? |

### Moved-in supporting files & relocations

- `route_runtime.go` moves into `runner` (it is orchestrator-coupled via
  `runTask`); `prepareExecutorForSelection` joins it. `FormatMixLabel` is
  relocated *out* of it, down into `relay`'s `mix.go`. The route-selection stored
  labels remain exact private literals in both packages (`__routes__`,
  `__override__:`), pinned by tests, so the split adds no exported relay token API.
- `log.go` moves into `runner` (consumers are runner-only); `logf` joins it.

The exact run/relay step-method names and where a tiny util like `containsInt`
lands follow the *existing contiguous blocks* — not pre-committed here.

## Decisions

**1. Extract `internal/relay/runner` now; do not defer behind a same-package
shard.** The boundary is one-directional and verified (see Context). Carving in
place first would move every file twice. The runner is the codebase's largest and
most-worked body of code; a package is the honest home for it. *Alternative
considered (and rejected):* the draft's "stay `package relay`, extract later" —
it is a preemptive constraint the dependency graph does not justify, and it
double-handles every file.

**2. Cut line = orchestrator vs primitives.** `runner.go` + `route_runtime.go` +
`log.go` form the orchestrator package; `relay.go` + `resilience.go` + `mix.go` +
`constants.go` stay as primitives. `route_runtime.go` moves because `runTask`
binds it to the orchestrator; `log.go` moves because only the runner consumes it;
`FormatMixLabel` stays in `relay` (relocated to `mix.go`) because it is a mix
primitive with external callers. This minimises caller churn to two references.

**3. Decompose `runOne` and `Run` in this change, sequenced last.** A 1,221-line
function is the biggest single risk in the codebase and the cheapest time to split
it is now, while behaviour is fully pinned by tests and before #4/#5 wrap new
behaviour around it. The decomposition is **block-for-block**: each named private
method is a verbatim lift of an existing contiguous block. It runs in Phase C,
after the package move (A) and pure carving (B) are green, so a regression is
isolated. *Alternative considered:* leave both whole — rejected: `runner.go` stays
~2,000 lines and the change under-delivers.

**4. Every symbol gets exactly one home; no catch-all.** The manifest assigns all
~14 previously-unplaced symbols (`tallyRuns`, `buildRecentContext`, `executeTry`,
`newStallController`, `runOutcome`, `routeFallbackCause`, `containsInt`, `logf`,
`prepareExecutorForSelection`, …), relocating two into moved-in files. No
`misc.go`/`helpers.go`. Cohesive clusters stay intact rather than being atomized —
"one file = one question," not "many tiny files."

**5. `liveness.go` is split from `action_loop.go`.** Both are try-level
monitoring, but answer different questions ("how is cancellation reconciled" vs
"how is stall detection constructed"). Keeping them apart keeps the operator-
control file — the one #5 lifts — focused.

**6. Bare filenames; no `runner/` *within* `runner`.** Files are named by the
question they answer, bare. The `runner_` qualifier the same-package draft used
(to dodge `telemetry`/`progress` import collisions) is gone: in `package runner` a
filename never collides with an imported package. No further subdirectory nesting
inside `runner` — the file count is bounded by the manifest.

**7. Carry a `relay-module-structure` capability spec, kept lean.** It records the
`runner → relay` boundary, the exported-API relocation contract, the
behaviour/telemetry/persistence-preservation contract, and the
one-responsibility-per-file invariant (enforcement handed to #3). It is a
risk-management contract — "here is exactly what may not change in behaviour, and
exactly how the API moved" — not a restatement of the manifest.

**8. Tests shard along the new files; fixtures stay shared and small.**
`runner_test.go` splits into files mirroring the production files. Shared fixtures
(`CopyFixtureProject`, `InitGitRepo`, `NewFixtureExecutor`, …) stay small and
package-local after the package split: `internal/relay` and
`internal/relay/runner` each get only the helpers their tests use, with truly
package-neutral helpers moved to `internal/testutil` only if duplication would be
larger than the helper itself. No second giant `helpers_test.go`.

**9. Route-selection stored-label tokens stay private and exact.** Route-based
relays currently persist `Relay.AgentMix` as `__routes__` or `__override__:<specs>`
so resume and CLI display can distinguish configured routes from legacy mixes. The
package split leaves `FormatMixLabel` in `relay` and route runtime in `runner`, so
the two packages deliberately keep tiny unexported constants with the same literal
values. Tests pin both sides: runner route creation/resume emits and accepts the
same labels, and `relay.FormatMixLabel` renders them as `configured routes`, the
override specs, or `(override)` for an empty override. No exported constants or
helper functions are added for these private persistence markers.

## Risks / Trade-offs

- **Phase C step-extraction is not a pure move.** Threading locals through new
  private methods can subtly change behaviour. → Mitigation: extract verbatim
  contiguous blocks; explicit receivers/params; `go test -race -shuffle=on` after
  each; run it **last** so regressions are isolated from the package move; compare
  coverage (must not drop).
- **The package move touches the exported API location.** A typo could rename or
  drop a symbol. → Mitigation: it is a *relocation*, signatures unchanged; diff the
  exported-identifier set of both packages before/after and `go build ./...` over
  all callers (only `cmd/rally` + two test files import the package).
- **Phase A can break tests before the code move is proven.** Existing `package
  relay` tests call symbols that move into `runner` (`NewRunner`, `runOne`,
  route runtime helpers, log helpers). → Mitigation: split/move tests by symbol
  ownership during Phase A, before the first `go test ./...` checkpoint.
- **Route-label tokens are easy to drift after the package split.** The private
  stored labels are now needed on both sides of the boundary. → Mitigation: keep
  exact literals, no new exported API, and add regression tests for route label
  generation/resume plus `relay.FormatMixLabel` display.
- **Large test re-shard could drop/duplicate a test.** → Mitigation: move whole
  `func TestXxx`/`BenchmarkXxx` blocks; keep a pre/post test inventory so every
  pre-change function appears exactly once after the split.
- **Merge cost against in-flight work** (runner.go is hot). → Mitigation: land this
  as its own focused branch ahead of #2–#5; blast radius is `internal/relay` +
  `cmd/rally` only.
- **Over-fragmentation into shallow files.** → Mitigation: the "one file = one
  question" rule and intact cohesive clusters (Decision 4); count bounded by the
  manifest.

## Migration Plan

1. **Baseline.** `go test -count=1 ./internal/relay ./cmd/rally` green; capture the
   exported-identifier set, the test/benchmark function inventory, and the
   `./internal/relay/...` coverage total as checksums. If red, record and do not
   fold unrelated fixes in.
2. **Phase A — package move.** Create `internal/relay/runner`; move `runner.go`,
   `route_runtime.go`, `log.go` (`package runner`) plus tests that reference the
   moved symbols; split mixed tests so primitive assertions stay in `relay`;
   relocate `FormatMixLabel` → `mix.go` with exact private route-label literals;
   fix `cmd/rally` and any test imports that reference `Runner`/`Config`. `go test
   ./...` green with `runner.go` still monolithic.
3. **Phase B — carve.** Move helper clusters into responsibility files (Section
   manifest), then the moved-in relocations (`logf` → `log.go`,
   `prepareExecutorForSelection` → `route_runtime.go`). `go test` after each;
   `-race` after `action_loop.go`/`liveness.go`.
4. **Phase C — decompose (last).** Move `runOne` into `run_one.go` verbatim; then
   decompose `Run` into `relay_steps.go` and `runOne` into `run_one.go` named
   step-methods, block-for-block, `-race` after each.
5. **Test reshard** to mirror the new files; verify the `Test*` count checksum.
6. **Verify:** `go test -count=1 ./...`; `go test -race -shuffle=on -count=1
   ./internal/relay/...`; coverage ≥ baseline; exported-surface diff matches the
   intended relocation and nothing else; `openspec validate --strict`.

**Rollback:** every phase is independent commits; revert the offending one. No
data, config, or release-mechanism change.

## Open Questions

- **Exact run/relay step-method boundaries** — resolved by implementation: follow
  the existing contiguous blocks; do not invent abstractions to make a block
  extractable.
- **Enforced `runner.go` line target?** No — an aspirational ~250–400 lines is
  stated; enforced budgets are owned by #3. This change only needs to leave
  `runner.go` small so #3's grandfathered cap starts low.
- **Do any focused `runner_*_test.go` files become redundant after the reshard?**
  Resolve during step 5: absorb overlaps, never drop or duplicate (count checksum
  guards this).
