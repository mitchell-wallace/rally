## 1. Baseline & checksums

- [x] 1.1 `go test -count=1 ./internal/relay ./cmd/rally` green. If red, capture failures and STOP — do not fold unrelated fixes into this refactor.
- [x] 1.2 Capture the exported-identifier set of `internal/relay` as a checksum (`go doc ./internal/relay` or a grep of exported decls), to compare against the post-split `internal/relay` + `internal/relay/runner` sets. Note `CancellationSource` and its consts are exported and will move to `runner`; no new exported route-label constants/helpers should appear.
- [x] 1.3 Capture a test/benchmark inventory, not just a count: pre-change `(file, Test*/Benchmark* name)` for all `internal/relay/*_test.go` functions. Keep a migration checklist so every pre-change function appears exactly once after the split, even when its package/file changes.
- [x] 1.4 Capture comparable coverage baseline: `go test -coverprofile=/tmp/rally-relay-cover-before.out ./internal/relay/...`, then record the `go tool cover -func=/tmp/rally-relay-cover-before.out` total. Coverage for the post-split `./internal/relay/...` packages must not drop.

## 2. Phase A — establish the `internal/relay/runner` package (mechanical move)

- [x] 2.1 Create `internal/relay/runner/` and move `runner.go`, `route_runtime.go`, and `log.go` into it wholesale as `package runner`. Also move/split tests that reference moved runner/route/log symbols before the Phase A test checkpoint; classify by symbol ownership, not by the old source filename. Do not carve or decompose yet (`runner.go` stays monolithic).
- [x] 2.2 Relocate `FormatMixLabel` (the only exported symbol in `route_runtime.go`) *down* into `internal/relay/mix.go` so it stays `relay.FormatMixLabel` (design Decision 2). Preserve the exact private stored-label literals `__routes__` and `__override__:` on both sides of the package boundary; do not add exported relay constants/functions for them. Move/add tests proving runner route labels still render correctly through `relay.FormatMixLabel`.
- [x] 2.3 Add `runner`'s import of `internal/relay` for the primitives it uses (`Resilience`, `ResilienceKey`, `AgentMix`, `Resolver`, the resilience consts). Confirm the dependency is one-way — `internal/relay` must not import `internal/relay/runner` (design Package boundary).
- [x] 2.4 Update callers: `relay.NewRunner` → `runner.NewRunner` and `relay.Config` → `runner.Config` in `cmd/rally/main.go`, `cmd/rally/telemetry_test.go`, and any other file that actually references the relocated symbols; add the `internal/relay/runner` import only where used. Leave `cmd/rally/main_test.go` alone unless it gains runner references; today it only uses `relay.NewResilience`. Leave `relay.CompleteRelay`, `relay.FormatMixLabel`, `relay.NewResilience`, `relay.AgentMix`, etc. unchanged.
- [x] 2.5 `go test -count=1 ./...` green with `runner.go` still monolithic; `go build ./...` compiles. Confirm the exported-identifier checksum (1.2) now splits cleanly across the two packages as intended and nothing else changed.

## 3. Phase B — carve the runner into responsibility files

All new files are `package runner` in `internal/relay/runner/`, bare names. Verbatim symbol moves; `go test -count=1 ./internal/relay/...` after each.

- [x] 3.1 `terminal.go`: `renderRunFooter`, `waitOutcome` (+const block), `waitWithCountdown`, `waitLoop`, `formatRemaining`.
- [x] 3.2 `failure_display.go`: `formatCategorizedDisplay`, `usageResetDuration`, `formatHoursMinutes`, `formatMinutesSeconds`, `benchResetDeadline`.
- [x] 3.3 `telemetry.go`: `applyTags`, `rallyContext`, `applyRallyContext`, `rallyFailure`, `failureStateEvent`, `limitSignalEvent`, `runnerLimitCategory`, `applyEvidenceToFailureState`, `applySafeExecErrorEvidence`, `addFailureEvidenceTelemetry`, `lapPinMismatchDiagnosticEvent`, `agentStateName`, `firstNonEmpty`, `resolvedRunnerModel`, `lastOutputAge` (shared by normal try timeout telemetry and handoff-only timeout telemetry).
- [x] 3.4 `task.go`: `runTask` (+`promptAssignee`), `headPullLap`, `queueSize`, `errQueueEmpty`, `resolveRunTask`, `resolveInstructions`, `loadFreeRunPrompt`, `resolveRoleInstructions`, free-run / incomplete-retry prompt consts, `buildRecentContext`, `recentContextStatus`. (Keeps the laps/role/prompt coupling confined here.)
- [x] 3.5 `git.go`: `commitLeftoverSummary`, `headHash`, `commitRange`, `autoCommit`, `filesChangedList`, `nonEmptyLines`.
- [x] 3.6 `final_snippet.go`: final-snippet consts, `normalizeFinalSnippet`, `progressSummaryEntryCount`, `recordedWrapupSummaryForRun`, `readTryLog`, `boundedFinalSnippetTail`, `finalSnippetErrorIndicator`, `readLastNLines`.
- [x] 3.7 `progress.go`: `newProgressRunState`, `storeLapAttempts`, `mergeStrings`, `hasDirtyChangesSince`, `handoffCreatedLapIDs`, `recoveryClassificationForRun`, `progressLapsCompletedForRun`, `progressRunEntryLapIDs`, `pinnedLapCompleteElsewhere`, `lapDoneInLapsState`, `stringSliceContains`, `recordedHandoffEntryForRun`, `handoffEntryFromRunEntry`, `recordedRunEntryForRun`, `tryOutcomeForAttempt`, `validatePinnedLap`, `detectLapsMarkerInText`, `maybeWriteStubAndClearState`.
- [x] 3.8 `action_loop.go`: `tryResult`, `actionMonitor`, `actionLoopDeps`, `actionLoopResult`, `CancellationSource` (+consts, `String`), `forceKillGroup`, `drainTimedOut`, `runActionLoop`, `drainOperatorCancellation`. Then `liveness.go`: `stallCheckInterval`, `newStallController`, `buildLivenessProbe` (design Decision 5). Run `go test -race -shuffle=on -count=1 ./internal/relay/...` after.
- [x] 3.9 `handoff_only.go`: `noHandoffResumeReason`, `buildHandoffOnlyPrompt`, `runBoundedHandoffOnly`.
- [x] 3.10 Relocate the in-package stragglers: `logf` → the moved-in `log.go`; `prepareExecutorForSelection` → the moved-in `route_runtime.go`.

## 4. Phase C — decompose the big lifecycle functions (highest risk; LAST)

- [ ] 4.1 Move `runOne` verbatim into `run_one.go` with `runOutcome`, `routeFallbackCause` (+`addTo`), `executeTry`, and `containsInt` (place `containsInt` with its caller). No logic change. `go test -race` green.
- [ ] 4.2 Decompose `Run` into named private step-methods in `relay_steps.go` (start/resume, relay- & run-scoped message consumption, route select/wait, fallback emit, apply-outcome-to-resilience, update-progress, print-summary) + `tallyRuns`. Each method is a verbatim lift of an existing contiguous block (design Decision 3). `go test -race` after.
- [ ] 4.3 Decompose `runOne` into named private step-methods in `run_one.go` (budget setup, monitored execution, outcome classification, final-snippet resolution, laps/progress reconciliation, retry/complete decision — boundaries follow existing blocks; names not pre-committed). Block-for-block; `go test -race` after.
- [ ] 4.4 Confirm `runner.go` is now a thin top-level orchestrator (~250–400 lines aspirational; not enforced here) holding `Config`, `Runner`, `NewRunner`, `RequestStop`, `SetTelemetry`/`tel`, `outWriter`, `newBoundTimer`, and the slimmed `Run` skeleton.

## 5. Test reshard

- [ ] 5.1 Complete the test split begun in Phase A: split `runner_test.go` into `package runner` files mirroring the production files (`terminal_test.go`, `failure_display_test.go`, `telemetry_test.go`, `task_test.go`, `git_test.go`, `final_snippet_test.go`, `progress_test.go`, `action_loop_test.go`, `liveness_test.go`, `handoff_only_test.go`, `run_one_test.go`, `relay_steps_test.go`). Move whole `func TestXxx` blocks; do not rewrite assertions. Move existing focused `runner_*_test.go` files into the `runner` package when they exercise runner symbols, but split mixed files so primitive tests stay in `internal/relay` (notably `runner_test.go`, `runner_hourly_retry_test.go`, `route_runtime_test.go`, `bench_state_machine_test.go`, `runner_telemetry_test.go`, and shared helpers).
- [ ] 5.2 Split helpers by package. `internal/relay` keeps only primitive-test helpers; `internal/relay/runner` keeps runner-test helpers. Tiny helpers may be duplicated when clearer; move truly package-neutral helpers to `internal/testutil` only if both packages need them and they require no unexported access. Do NOT create a second large catch-all (design Decision 8). Note: tests that exercise relay primitives (resilience/mix/relay-record) stay in `internal/relay`; tests of runner behaviour move to `internal/relay/runner`.
- [ ] 5.3 Verify the inventory from 1.3: every pre-change `Test*`/`Benchmark*` function appears exactly once across `internal/relay` and `internal/relay/runner`, expected package/file moves are recorded, and no assertions were dropped or duplicated. Any new tests are limited to the explicit route-label regression coverage from 2.2 and are recorded separately from the relocation inventory.

## 6. Verification

- [ ] 6.1 `go test -count=1 ./...` green.
- [ ] 6.2 `go test -race -shuffle=on -count=1 ./internal/relay/...` green.
- [ ] 6.3 Exported-surface diff matches the intended relocation (`Config`/`Runner`/`NewRunner`/`CancellationSource` now under `runner`; `Resilience`/`AgentMix`/`FormatMixLabel`/`CreateRelay`/… still under `relay`) and nothing else changed; `go build ./...` compiles all callers.
- [ ] 6.4 Coverage for the post-split `./internal/relay/...` packages is not below the 1.4 baseline: `go test -coverprofile=/tmp/rally-relay-cover-after.out ./internal/relay/...`, then compare the `go tool cover -func=/tmp/rally-relay-cover-after.out` total to the recorded baseline.
- [ ] 6.5 Confirm zero behaviour-surface change: no telemetry-field / CLI-string / store-shape / laps-semantic / git-message edits; `internal/buildinfo/VERSION` untouched (no version bump).
- [ ] 6.6 `openspec validate decompose-relay-runner --strict` passes.
