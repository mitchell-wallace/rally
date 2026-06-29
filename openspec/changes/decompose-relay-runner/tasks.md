## 1. Baseline & checksums

- [ ] 1.1 Run `go test -count=1 ./internal/relay` and confirm green. If red, capture the failures and STOP — do not fold unrelated fixes into this refactor (design Migration step 1).
- [ ] 1.2 Capture the exported-identifier set of `internal/relay` as a checksum (e.g. `go doc ./internal/relay` or a grep of exported `func`/`type`/`const`/`var` decls), saved to compare at the end. Note `CancellationSource` and its consts are exported (design Risks).
- [ ] 1.3 Capture the `Test*`/`Benchmark*` function count in `internal/relay` as a checksum for the test reshard (`grep -c '^func Test\|^func Benchmark' internal/relay/*_test.go` total).
- [ ] 1.4 (If coverage is used during implementation) capture `go test -cover ./internal/relay` baseline; coverage must not drop by the end.

## 2. Pure helper files (lowest risk — verbatim symbol moves)

Move each cluster into a new `package relay` file, then run `go test -count=1 ./internal/relay` after each move. No logic edits.

- [ ] 2.1 `terminal.go`: `renderRunFooter`, `waitOutcome` (+ its const block), `waitWithCountdown`, `waitLoop`, `formatRemaining`.
- [ ] 2.2 `failure_display.go`: `formatCategorizedDisplay`, `usageResetDuration`, `formatHoursMinutes`, `formatMinutesSeconds`, `benchResetDeadline`.
- [ ] 2.3 `runner_telemetry.go` (qualified — `telemetry` is an imported pkg): `applyTags`, `rallyContext`, `applyRallyContext`, `rallyFailure`, `failureStateEvent`, `limitSignalEvent`, `runnerLimitCategory`, `applyEvidenceToFailureState`, `applySafeExecErrorEvidence`, `addFailureEvidenceTelemetry`, `lapPinMismatchDiagnosticEvent`, `agentStateName`, `firstNonEmpty`, `resolvedRunnerModel`.
- [ ] 2.4 `task.go`: `runTask` (+`promptAssignee`), `headPullLap`, `queueSize`, `errQueueEmpty`, `resolveRunTask`, `resolveInstructions`, `loadFreeRunPrompt`, `resolveRoleInstructions`, the free-run / incomplete-retry prompt consts, `buildRecentContext`, `recentContextStatus`. (Keeps the laps/role/prompt coupling confined here.)
- [ ] 2.5 `git.go`: `commitLeftoverSummary`, `headHash`, `commitRange`, `autoCommit`, `filesChangedList`, `nonEmptyLines`.
- [ ] 2.6 `final_snippet.go`: final-snippet consts, `normalizeFinalSnippet`, `progressSummaryEntryCount`, `recordedWrapupSummaryForRun`, `readTryLog`, `boundedFinalSnippetTail`, `finalSnippetErrorIndicator`, `readLastNLines`.
- [ ] 2.7 `runner_progress.go` (qualified — `progress` is an imported pkg): `newProgressRunState`, `storeLapAttempts`, `mergeStrings`, `hasDirtyChangesSince`, `handoffCreatedLapIDs`, `recoveryClassificationForRun`, `progressLapsCompletedForRun`, `progressRunEntryLapIDs`, `pinnedLapCompleteElsewhere`, `lapDoneInLapsState`, `stringSliceContains`, `recordedHandoffEntryForRun`, `handoffEntryFromRunEntry`, `recordedRunEntryForRun`, `tryOutcomeForAttempt`, `validatePinnedLap`, `detectLapsMarkerInText`, `maybeWriteStubAndClearState`.
- [ ] 2.8 Confirm the exported-surface checksum (1.2) is still unchanged after the pure-helper moves.

## 3. Try-level files + relocations (touches monitor/cancellation — run with -race)

- [ ] 3.1 `action_loop.go`: `tryResult`, `actionMonitor`, `actionLoopDeps`, `actionLoopResult`, `CancellationSource` (+ its consts and `String`), `forceKillGroup`, `drainTimedOut`, `runActionLoop`, `drainOperatorCancellation`.
- [ ] 3.2 `liveness.go`: `stallCheckInterval`, `newStallController`, `buildLivenessProbe` (design Decision 4 — kept distinct from the action loop).
- [ ] 3.3 Relocate `logf` into the existing `log.go`, and `prepareExecutorForSelection` into the existing `route_runtime.go` (design Relocations).
- [ ] 3.4 `go test -race -shuffle=on -count=1 ./internal/relay` green after the try-level moves.

## 4. Handoff-only continuation

- [ ] 4.1 `handoff_only.go`: `noHandoffResumeReason`, `buildHandoffOnlyPrompt`, `runBoundedHandoffOnly`, `lastOutputAge` (confirm `lastOutputAge` has no other caller before moving; if shared, leave it with its other caller).
- [ ] 4.2 `go test -count=1 ./internal/relay` green.

## 5. Lifecycle split (highest risk — sequenced LAST; block-for-block)

- [ ] 5.1 Move `runOne` verbatim into `run_one.go` along with `runOutcome`, `routeFallbackCause` (+`addTo`), `executeTry`, and `containsInt` (place `containsInt` with its caller). No logic change yet. `go test -race` green.
- [ ] 5.2 Decompose `Run` into named private step-methods in `relay_steps.go` (start/resume, relay- & run-scoped message consumption, route select/wait, fallback emit, apply-outcome-to-resilience, update-progress, print-summary) plus `tallyRuns`. Each method is a verbatim lift of an existing contiguous block (design Decision 2). `go test -race` after the extraction.
- [ ] 5.3 Decompose `runOne` into named private step-methods in `run_one.go` (budget setup, monitored execution, outcome classification, final-snippet resolution, laps/progress reconciliation, retry/complete decision — boundaries follow existing blocks, names not pre-committed). Block-for-block; `go test -race` after the extraction.
- [ ] 5.4 Confirm `runner.go` is now a thin top-level orchestrator (~250–400 lines aspirational; not enforced here — design Open Questions) holding `Config`, `Runner`, `NewRunner`, `RequestStop`, `SetTelemetry`/`tel`, `outWriter`, `newBoundTimer`, and the slimmed `Run` skeleton.

## 6. Test reshard (mirror the new files)

- [ ] 6.1 Split `runner_test.go` into files mirroring the production files (`terminal_test.go`, `failure_display_test.go`, `runner_telemetry_test.go`, `task_test.go`, `git_test.go`, `final_snippet_test.go`, `runner_progress_test.go`, `action_loop_test.go`, `liveness_test.go`, `handoff_only_test.go`, `run_one_test.go`, `relay_steps_test.go`). Move whole `func TestXxx` blocks; do not rewrite assertions.
- [ ] 6.2 Keep shared fixtures (`CopyFixtureProject`, `InitGitRepo`, `NewFixtureExecutor`, etc.) in one small helper file; do NOT create a second large catch-all (design Decision 7).
- [ ] 6.3 Absorb overlaps with existing focused `runner_*_test.go` files where sensible, but never drop or duplicate a test.
- [ ] 6.4 Verify the `Test*`/`Benchmark*` count checksum (1.3) is unchanged.

## 7. Verification

- [ ] 7.1 `go test -count=1 ./...` green.
- [ ] 7.2 `go test -race -shuffle=on -count=1 ./internal/relay` green.
- [ ] 7.3 Exported-surface diff against the 1.2 checksum is empty; `go build ./...` (all callers) compiles.
- [ ] 7.4 Coverage for `internal/relay` is not below the 1.4 baseline (behaviour did not change).
- [ ] 7.5 Confirm zero behaviour-surface change: no diff outside `internal/relay/`, `internal/buildinfo/VERSION` untouched (no version bump), and no telemetry-field / CLI-string / store-shape / git-message edits crept in.
- [ ] 7.6 `openspec validate decompose-relay-runner --strict` passes.
