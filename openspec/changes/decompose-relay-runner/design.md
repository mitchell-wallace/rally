## Context

`internal/relay/runner.go` accreted every responsibility of the relay engine into
one 3,782-line file. The package already has well-factored siblings —
`resilience.go` (the freeze/bench/pause state machine), `route_runtime.go` (route
selection), `mix.go`, `log.go`, `constants.go` — and a large, exhaustive test
suite (`runner_test.go` ~6,915 lines plus a dozen focused `runner_*_test.go`
files). The seams between concerns already exist and are individually tested;
they are just not reflected in the file layout.

The dominant mass is two functions:

| Function | ~Lines | Role |
| --- | --- | --- |
| `runOne` | 1,221 | The run/try lifecycle: budget, monitored execution, outcome classification, final-snippet, laps reconciliation, retry decision. |
| `Run` | 465 | The relay loop: start/resume, message consumption, route selection + fallback, resilience update, progress, summary. |
| `runBoundedHandoffOnly` | 273 | Recovery-role bounded continuation. |

Everything else (~1,800 lines) is helper clusters that already have clear owners.

This change is the first of a six-change architecture sequence
(`openspec/next-up.md`). The carried-over **OpenSpec/laps** principle applies: the
laps coupling must stay confined (here, to `task.go` and `runner_progress.go`),
and nothing OpenSpec-specific belongs in this generic surface.

### Deep modules, the right way round

A Philosophy of Software Design warns against **shallow** modules — units whose
interface cost rivals the complexity they hide. The cheapest way to *create*
shallow modules is to extract subpackages prematurely: each new package is a new
import edge and a new public API to maintain, justified only once its boundary is
proven by real use. So this change deliberately does **not** create modules. It
shards one file into responsibility-named files **inside the same package**,
which has zero interface cost (no new imports, no new exported API) and makes the
true boundaries observable. The genuinely deep modules come *later* in the
sequence, when those boundaries have been exercised:

- `action_loop.go` + `liveness.go` (operator control / stall detection) →
  feed the operator-control boundary in `separate-runtime-presentation-boundary`
  (#5).
- `run_one.go` / `relay_steps.go` (the run/try and relay-iteration lifecycle) →
  the presentation-neutral runtime that #5 and the future TUI render.
- `runner_telemetry.go` → already a stable provider-neutral surface (New Relic).
- `task.go` / `runner_progress.go` (laps + role + prompt bridge) → the confined
  laps-coupling seam referenced by the role rename (#6) and `prepare-laps`.

Sequencing the interface tax *after* sharding is the deep-module discipline, not
a deferral of it.

## Goals / Non-Goals

**Goals:**

- `runner.go` explains the top-level relay flow and little else (~250–400 lines).
- Every file answers exactly one architectural question; every current symbol has
  exactly one home (no ambiguous remainder).
- The 1,221-line `runOne` and 465-line `Run` are decomposed into named steps so
  the lifecycle is readable and no single function is a future hazard.
- Tests are split to sit beside the code they exercise, with one small shared
  fixtures file.
- Zero behaviour change: identical CLI output, telemetry fields, persisted-store
  shape, laps semantics, git messages, and exported API.

**Non-Goals:**

- New subpackages, interfaces, or any abstraction layer (deferred to #4/#5 and a
  later subpackage-extraction change).
- CI file-size / import-boundary budgets (owned by `add-architecture-guardrails`
  #3; this change only makes those budgets *easy to set low*).
- Behaviour, performance, TUI, harness, or role changes.

## File manifest

All files are `package relay` in `internal/relay/`. Naming rule: **bare
responsibility name**, matching `resilience.go`/`route_runtime.go`; add a
`runner_` qualifier **only** when a bare name would collide with an imported
package (`telemetry`, `progress`). A `runner/` subdirectory is intentionally
*not* used — see Decision 5.

### Lifecycle spine (relay › run › try)

| File | Owns (representative) | Answers |
| --- | --- | --- |
| `runner.go` | `Config`, `Runner`, `NewRunner`, `RequestStop`, `SetTelemetry`, `tel`, `outWriter`, `newBoundTimer`, slimmed `Run` skeleton | What is a `Runner`, and what is the shape of the relay loop? |
| `relay_steps.go` | `Run`'s extracted iteration steps (start/resume, relay- & run-scoped message consumption, route select/wait, fallback emit, apply-outcome-to-resilience, update-progress, print-summary), `tallyRuns` | What happens in one relay iteration? |
| `run_one.go` | slimmed `runOne` + its extracted run-level steps, `runOutcome`, `routeFallbackCause`(+`addTo`), `executeTry`, `containsInt` | What is the lifecycle of one run (its tries) and how is its outcome resolved? |
| `action_loop.go` | `tryResult`, `actionMonitor`, `actionLoopDeps`, `actionLoopResult`, `CancellationSource`(+consts/`String`), `forceKillGroup`, `drainTimedOut`, `runActionLoop`, `drainOperatorCancellation` | How are operator keypresses, timeouts, stalls, and cancellation reconciled during a try? |
| `liveness.go` | `stallCheckInterval`, `newStallController`, `buildLivenessProbe` | How is per-try stall/liveness detection wired? |
| `handoff_only.go` | `noHandoffResumeReason`, `buildHandoffOnlyPrompt`, `runBoundedHandoffOnly`, `lastOutputAge` | How does a bounded handoff-only recovery run behave? |

### Cross-cutting helpers (by responsibility)

| File | Owns (representative) | Answers |
| --- | --- | --- |
| `terminal.go` | `renderRunFooter`, `waitOutcome`(+consts), `waitWithCountdown`, `waitLoop`, `formatRemaining` | How is the run footer/countdown rendered and how do operator waits resolve? |
| `failure_display.go` | `formatCategorizedDisplay`, `usageResetDuration`, `formatHoursMinutes`, `formatMinutesSeconds`, `benchResetDeadline` | How are failure categories and reset deadlines formatted for the operator? |
| `runner_telemetry.go` | `applyTags`, `rallyContext`, `applyRallyContext`, `rallyFailure`, `failureStateEvent`, `limitSignalEvent`, `runnerLimitCategory`, `applyEvidenceToFailureState`, `applySafeExecErrorEvidence`, `addFailureEvidenceTelemetry`, `lapPinMismatchDiagnosticEvent`, `agentStateName`, `firstNonEmpty`, `resolvedRunnerModel` | How are relay telemetry spans, events, and evidence assembled? |
| `task.go` | `runTask`(+`promptAssignee`), `headPullLap`, `queueSize`, `errQueueEmpty`, `resolveRunTask`, `resolveInstructions`, `loadFreeRunPrompt`, `resolveRoleInstructions`, free-run / incomplete-retry prompt consts, `buildRecentContext`, `recentContextStatus` | How is the next lap/role/prompt resolved and the recent-context prompt built for a run? |
| `git.go` | `commitLeftoverSummary`, `headHash`, `commitRange`, `autoCommit`, `filesChangedList`, `nonEmptyLines` | How is a try's work committed and its file-change list computed? |
| `final_snippet.go` | final-snippet consts, `normalizeFinalSnippet`, `progressSummaryEntryCount`, `recordedWrapupSummaryForRun`, `readTryLog`, `boundedFinalSnippetTail`, `finalSnippetErrorIndicator`, `readLastNLines` | How is the run's final snippet derived and bounded? |
| `runner_progress.go` | `newProgressRunState`, `storeLapAttempts`, `mergeStrings`, `hasDirtyChangesSince`, `handoffCreatedLapIDs`, `recoveryClassificationForRun`, `progressLapsCompletedForRun`, `progressRunEntryLapIDs`, `pinnedLapCompleteElsewhere`, `lapDoneInLapsState`, `stringSliceContains`, `recordedHandoffEntryForRun`, `handoffEntryFromRunEntry`, `recordedRunEntryForRun`, `tryOutcomeForAttempt`, `validatePinnedLap`, `detectLapsMarkerInText`, `maybeWriteStubAndClearState` | How is laps/progress state validated and reconciled for a run? |

### Relocations to existing files

| Symbol | New home | Why |
| --- | --- | --- |
| `logf` | `log.go` | The package's logging concern already lives there. |
| `prepareExecutorForSelection` | `route_runtime.go` | It is route-selection glue; route selection already lives there. |

The exact run/relay step-method names (e.g. `startOrResumeRelay`,
`selectRouteOrWait`, `applyRunOutcomeToResilience`, `classifyTryOutcome`,
`reconcileLapsProgress`) and where a tiny util like `containsInt` lands are
implementation details to be discovered by following the *existing contiguous
blocks* — not pre-committed here.

## Decisions

**1. Same package, no subpackages, no new interfaces.** All moved code stays in
`package relay`. This is the whole reason the change is safe and mechanically
reviewable: no import churn, no new public API, no risk of an extraction creating
a shallow module. Subpackage extraction is explicitly a *later* architecture
change. *Alternative considered:* extract `internal/relay/runner` or
`internal/relay/lifecycle` now — rejected: it pays the interface tax before the
boundaries are proven and would collide with the work #4/#5 own.

**2. Decompose `runOne` and `Run` in this change, not "optionally later".** The
draft listed step-extraction as optional follow-up (its section J). We pull it in
because a 1,221-line function is the single biggest risk in the package and the
cheapest time to split it is now, while behaviour is fully pinned by tests and
before later changes wrap new behaviour around it. The decomposition is
**block-for-block**: each named private method is a verbatim lift of an existing
contiguous block, with the same locals threaded through, so the diff stays
reviewable and `go test` (incl. `-race`) is the safety net. To bound risk, this
step is sequenced **last**, after all pure helper moves are green (see
Migration). *Alternative considered:* leave both functions whole (helpers-only
split) — rejected: `runner.go` would stay ~2,000 lines and the change would
under-deliver on its own headline goal.

**3. Every symbol gets exactly one home; no catch-all.** The draft left ~14
symbols unplaced (`tallyRuns`, `buildRecentContext`, `recentContextStatus`,
`prepareExecutorForSelection`, `executeTry`, `newStallController`,
`buildLivenessProbe`, `stallCheckInterval`, `runOutcome`, `routeFallbackCause`,
`containsInt`, `logf`, plus trivial `Runner` glue). The manifest above assigns
each one, including relocating two into existing files. We do **not** create a
`misc.go`/`helpers.go` catch-all — that would recreate the original problem at
smaller scale. The principle is "one file = one architectural question," not "many
tiny files": cohesive clusters (e.g. the whole laps/progress validation set) stay
together rather than being atomized.

**4. `liveness.go` is split from `action_loop.go`.** Both are try-level
monitoring, but they answer different questions: `action_loop.go` is "how is
operator/cancellation/timeout state reconciled," `liveness.go` is "how is stall
detection constructed." Keeping them separate keeps each file single-purpose and
keeps the operator-control file (the one #5 will lift) focused.

**5. Filename taxonomy, and why not a `runner/` subdirectory.** Files are named by
the architectural question they answer, bare, matching existing siblings; the
`runner_` qualifier is used only on collision with an imported package
(`runner_telemetry.go`, `runner_progress.go`). A `runner/` *subdirectory* was
considered for visual grouping but is rejected for this change: in Go one
directory is one package, so a subdirectory **is** a new package — which violates
Decision 1 and the sequence (subpackage extraction is later). The flat
responsibility-named layout is chosen so that the eventual subpackage split is a
near-mechanical `git mv` of an already-cohesive file, not a re-untangling.
*Alternative considered:* uniform `runner_` prefix on every carved file —
rejected: it diverges from the package's established bare-named siblings and adds
no information once the files are responsibility-named.

**6. Carry a `relay-module-structure` capability spec, kept lean.** The spec
records three things and no more: the preserved exported surface, the
behaviour/telemetry/persistence-preservation contract, and the
one-responsibility-per-file invariant (with enforcement explicitly handed to #3).
It is a risk-management contract — "here is exactly what may not change" — and it
gives `openspec validate` a delta and #3 a referent. It is **not** a place to
enumerate every file (that lives in this design); the spec states the *invariant*,
not the manifest. *Alternative considered:* no spec delta — rejected: it leaves no
durable contract and diverges from the sibling `harden-ci-correctness-gates`,
which carried a spec even though it was tooling-only.

**7. Tests shard along the same seams, fixtures stay shared and small.**
`runner_test.go` is split into files mirroring the production files
(`terminal_test.go`, `task_test.go`, `git_test.go`, `runner_progress_test.go`,
`handoff_only_test.go`, …). Existing focused `runner_*_test.go` files are left as
they are or absorbed where they overlap. Shared fixtures (`CopyFixtureProject`,
`InitGitRepo`, `NewFixtureExecutor`, etc.) stay in one small helper file; we do
**not** grow a second giant `helpers_test.go`.

## Risks / Trade-offs

- **Step-extraction is not a pure move (Decision 2).** Threading locals through
  new private methods can subtly change behaviour (e.g. a closed-over variable, an
  early `return`/`continue`, a deferred call's scope). → Mitigation: extract
  verbatim contiguous blocks, keep method receivers/params explicit, run
  `go test -race -shuffle=on -count=1 ./internal/relay` after each extraction, and
  do this step **last** so a regression is isolated from the pure moves. Compare
  coverage before/after — it must not drop.
- **Large test re-shard can drop or duplicate a test.** → Mitigation: move whole
  `func TestXxx` blocks; assert the total `Test`/`Benchmark` function count is
  unchanged before and after; `go test ./internal/relay -run . -list .` (or a
  count) as a checksum.
- **Accidental exported-surface change** (e.g. capitalizing a moved helper, or
  dropping `CancellationSource`'s exported consts). → Mitigation: diff the
  exported-identifier set before/after (`go doc` or a `grep` of exported decls)
  and `go build ./...` over all callers.
- **Merge cost against in-flight work.** runner.go is a hot file. → Mitigation:
  land this change as its own focused branch ahead of #2–#5; it touches only
  `internal/relay` so its blast radius is contained.
- **Over-fragmentation** producing shallow files. → Mitigation: the "one file =
  one question" rule and keeping cohesive clusters intact (Decision 3); the file
  count is bounded by the manifest, not open-ended.

## Migration Plan

Strictly ordered so risk rises only after the safe moves are green:

1. **Baseline.** `go test -count=1 ./internal/relay` green; capture the exported-
   identifier set and the `Test*` function count as checksums. If the baseline is
   red, record it and do not fold unrelated fixes into this change.
2. **Pure helper files** (`terminal.go`, `failure_display.go`,
   `runner_telemetry.go`, `task.go`, `git.go`, `final_snippet.go`,
   `runner_progress.go`) — verbatim symbol moves; `go test` after each.
3. **Try-level files** (`action_loop.go`, `liveness.go`) + relocations (`logf` →
   `log.go`, `prepareExecutorForSelection` → `route_runtime.go`); run with
   `-race` since this touches monitor/cancellation code.
4. **`handoff_only.go`** — move the bounded continuation cluster.
5. **Lifecycle split (highest risk, last):** move `runOne` to `run_one.go`
   verbatim; then decompose `Run` into `relay_steps.go` named methods and `runOne`
   into `run_one.go` named methods, block-for-block, `-race` after each.
6. **Test re-shard** to mirror the new files; verify the `Test*` count checksum.
7. **Verification:** `go test -count=1 ./...`, `go test -race -shuffle=on -count=1
   ./internal/relay`, coverage compared to baseline (must not drop), exported-
   surface diff empty, `openspec validate decompose-relay-runner --strict`.

**Rollback:** every step is an independent file-move commit; revert the offending
commit. No data, config, or release-mechanism change is involved.

## Open Questions

- **Exact run/relay step-method boundaries.** Resolved by implementation: follow
  the existing contiguous blocks in `Run`/`runOne`; do not invent abstractions to
  make a block extractable. Illustrative names in the manifest are not binding.
- **Should `runner.go` have an enforced line target here?** No — an aspirational
  ~250–400 lines is stated, but enforced file-size budgets are owned by
  `add-architecture-guardrails` (#3). This change only needs to leave `runner.go`
  small enough that #3's grandfathered cap starts low.
- **Do any focused `runner_*_test.go` files become redundant after the reshard?**
  Resolve during step 6: absorb overlaps, but never duplicate or drop a test
  (count checksum guards this).
