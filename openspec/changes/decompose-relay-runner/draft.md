## Draft: Decompose Relay Runner

Status: drafted 2026-06-29 - initial architecture concept only.

This change is an architectural refactor. It should preserve Rally's runtime
behaviour, CLI output, telemetry fields, persisted store shape, laps semantics,
and public `internal/relay` entry points.

## Why

`internal/relay/runner.go` has grown to 3,782 lines. Its primary test file,
`internal/relay/runner_test.go`, is 6,915 lines. This is now too much context
for a maintainer or future agent to absorb in one pass.

The file currently mixes multiple concerns:

- relay startup, resume, and completion,
- route selection, fallback, and resilience updates,
- run and try execution,
- keyboard actions and countdown rendering,
- live monitor wiring,
- telemetry event assembly,
- git commit and state-folding behaviour,
- laps claim/finalization validation,
- bounded handoff-only continuation,
- final-snippet normalization,
- assorted pure formatting and aggregation helpers.

The code already has many tested seams. The first improvement should exploit
those seams without changing package boundaries, exported APIs, or behaviour.

## Intent

Turn `internal/relay` into a progressively discoverable module:

- `runner.go` should explain the top-level relay/run flow.
- Supporting files should be named by responsibility.
- Tests should be split near the code they exercise.
- Deeper package extraction should wait until same-package sharding has made
  the real boundaries obvious.

The goal is not to make many tiny files. The goal is to make each file answer a
clear architectural question.

## Candidate Work

### A. Preserve the existing package and public surface

Keep all moved code in `package relay` for this change. Do not introduce new
subpackages yet. Do not rename exported `relay.Config`, `relay.Runner`,
`relay.NewRunner`, `Runner.Run`, `Runner.SetTelemetry`, `relay.FormatMixLabel`,
or the relay/resilience APIs that callers already use.

This keeps the change mechanically reviewable and avoids import churn while the
largest file is split.

### B. Split pure terminal and display helpers

Create `internal/relay/runner_terminal.go` or similar.

Move:

- `renderRunFooter`,
- `waitOutcome` and wait constants,
- `waitWithCountdown`,
- `waitLoop`,
- `formatRemaining`.

These are a low-risk first extraction because they already have focused tests
and mostly depend on `keyboard`, `style`, `io`, and `time`.

Create `internal/relay/runner_failure_display.go` or similar.

Move:

- `formatCategorizedDisplay`,
- `usageResetDuration`,
- `formatHoursMinutes`,
- `formatMinutesSeconds`,
- `benchResetDeadline`.

### C. Split telemetry helpers

Create `internal/relay/runner_telemetry.go`.

Move:

- `applyTags`,
- `rallyContext`,
- `applyRallyContext`,
- `rallyFailure`,
- `failureStateEvent`,
- `limitSignalEvent`,
- `runnerLimitCategory`,
- `applyEvidenceToFailureState`,
- `applySafeExecErrorEvidence`,
- `addFailureEvidenceTelemetry`,
- `lapPinMismatchDiagnosticEvent`,
- `agentStateName`,
- `firstNonEmpty`,
- `resolvedRunnerModel`.

Keep the function names unchanged initially so existing tests remain meaningful.

### D. Split the in-try action loop

Create `internal/relay/action_loop.go`.

Move:

- `tryResult`,
- `actionMonitor`,
- `actionLoopDeps`,
- `actionLoopResult`,
- `CancellationSource` and constants,
- `forceKillGroup`,
- `drainTimedOut`,
- `runActionLoop`,
- `drainOperatorCancellation`.

This should make the operator-control state machine discoverable without
requiring a reader to navigate the full run lifecycle.

### E. Split task, role, and prompt resolution

Create `internal/relay/runner_task.go`.

Move:

- `runTask`,
- `runTask.promptAssignee`,
- `headPullLap`,
- `queueSize`,
- `errQueueEmpty`,
- `resolveRunTask`,
- `resolveInstructions`,
- `loadFreeRunPrompt`,
- `resolveRoleInstructions`,
- free-run and incomplete-retry prompt constants.

This isolates the bridge between laps, role instructions, and agent prompts.

### F. Split git/state and final-snippet helpers

Create `internal/relay/runner_git.go`.

Move:

- `commitLeftoverSummary`,
- `headHash`,
- `commitRange`,
- `autoCommit`,
- `filesChangedList`,
- `nonEmptyLines`.

Create `internal/relay/runner_final_snippet.go`.

Move:

- final-snippet constants,
- `normalizeFinalSnippet`,
- `progressSummaryEntryCount`,
- `recordedWrapupSummaryForRun`,
- `readTryLog`,
- `boundedFinalSnippetTail`,
- `finalSnippetErrorIndicator`,
- `readLastNLines` if it remains final-snippet related after inspection.

### G. Split laps/progress validation helpers

Create `internal/relay/runner_progress.go`.

Move:

- `newProgressRunState`,
- `storeLapAttempts`,
- `mergeStrings`,
- `hasDirtyChangesSince`,
- `handoffCreatedLapIDs`,
- `recoveryClassificationForRun`,
- `progressLapsCompletedForRun`,
- `progressRunEntryLapIDs`,
- `pinnedLapCompleteElsewhere`,
- `lapDoneInLapsState`,
- `stringSliceContains`,
- `recordedHandoffEntryForRun`,
- `handoffEntryFromRunEntry`,
- `recordedRunEntryForRun`,
- `tryOutcomeForAttempt`,
- `validatePinnedLap`,
- `detectLapsMarkerInText`,
- `maybeWriteStubAndClearState`.

If this file becomes too large, split durable handoff helpers into
`runner_handoff_progress.go`.

### H. Split bounded handoff-only continuation

Create `internal/relay/handoff_only.go`.

Move:

- `noHandoffResumeReason`,
- `buildHandoffOnlyPrompt`,
- `runBoundedHandoffOnly`,
- `lastOutputAge` if it is only used by this path.

This gives the recovery-role timeout lifecycle a clear home.

### I. Split tests along the same seams

Move tests out of the giant `runner_test.go` into files such as:

- `runner_terminal_test.go`,
- `runner_task_test.go`,
- `runner_progress_test.go`,
- `runner_git_test.go`,
- `handoff_only_test.go`.

Keep shared test fixtures in one small same-package helper file. Do not create a
large `helpers_test.go` catch-all that repeats the same problem.

### J. Optional follow-up within the same change: slim `Runner.Run`

After the mechanical file split is green, consider extracting named private
methods from `Runner.Run` for:

- `startOrResumeRelay`,
- `consumeRelayScopedMessage`,
- `consumeRunScopedMessage`,
- `selectRouteOrWait`,
- `emitRouteFallback`,
- `applyRunOutcomeToResilience`,
- `updateRelayProgress`,
- `printRelaySummary`.

This should only happen if it reduces local complexity without introducing a
new abstraction layer.

## Testing Strategy

This change should be intentionally boring to test because it should mostly move
code.

Before editing:

- Run `go test -count=1 ./internal/relay` to establish the local baseline.
- If the baseline is not green, capture the failures and do not mix fixes into
  this refactor unless they are required by the move.

After each small group of moves:

- Run `go test -count=1 ./internal/relay`.
- Run targeted tests where available, such as action-loop, timeout, final
  snippet, telemetry, route runtime, and laps pin tests.

Before completion:

- Run `go test -count=1 ./...`.
- Run `go test -race -shuffle=on -count=1 ./internal/relay` if the edit touched
  action-loop, monitor, timeout, or cancellation code.
- Compare `go test` coverage output for `internal/relay` before and after if
  coverage is used during implementation. Coverage should not decrease because
  behaviour should not change.

## Sequencing

1. Move pure helpers and tests first.
2. Move action-loop and telemetry helpers next.
3. Move git/progress/handoff helpers.
4. Only then slim `Runner.Run` and `runOne` with private method extraction.
5. Leave subpackage extraction for a later architecture change.

## Open Questions

- Should this change set a target maximum for the remaining `runner.go` file,
  such as under 600 lines, or simply split until each file has one clear reason
  to exist?
- Should `runOne` stay in `runner.go` for now, or move to `run_one.go` once its
  helpers are extracted?
- Should test files have a different line budget than production files during
  this first split?

## Out of Scope

- Behaviour changes to retry, routing, laps, git, telemetry, or terminal output.
- New public package boundaries.
- TUI integration.
- Adding new harnesses or roles.
- Architecture guardrail CI. That belongs in `add-architecture-guardrails`.
