## 1. Baseline and re-grounding

- [x] 1.1 Confirm the tree is green before refactoring: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go test -count=1 ./internal/relay/...`, `go run ./tools/archguard --ci` (exit 0). If red, STOP — do not fold unrelated fixes into this refactor.
- [x] 1.2 Re-ground the inventories (this change runs after #5, which moves rendering out of these files): regenerate line counts for `run_one.go` / `route_runtime.go` / `relay_steps.go` and the `func` inventory (`grep -n "^func" internal/relay/runner/{run_one,route_runtime,relay_steps}.go`). Adjust the design.md file layout membership if functions were rehomed by #5; the per-phase-file decisions hold regardless.
- [x] 1.3 Record the pre-change function inventory (name → file) for the three files so the move can be verified one-for-one afterward.

## 2. Split run_one.go into per-phase files

- [x] 2.1 Create the phase files per design Decision 2 (`run_attempt_prepare.go`, `run_attempt_monitor.go`, `run_attempt_reconcile.go`, `run_attempt_classify.go`, `run_attempt_record.go`, `run_attempt_cancel.go`, `run_retry_decide.go`, `run_handoff.go`, `run_finalize.go`), moving each phase function **verbatim**; `run_one.go` keeps only `Runner.runOne`, `newRunOneState`, `runOneState.outcome`; move `captureRunStartWorkspaceState` and `setupRunBudget` to `run_attempt_prepare.go` unless the re-grounded inventory shows #5 removed or renamed them. Move small utilities (`containsInt`) with their only caller.
- [x] 2.2 Extract named unexported sub-step helpers inside `run_attempt_classify.go` (`classifyAttemptOutcome`, ~229 lines at baseline) — straight-line regions only, explicit inputs/outputs, byte-identical error strings and telemetry fields.
- [x] 2.3 Same for `run_attempt_record.go` (`recordAttemptOutcome`, ~261 lines at baseline). Apply to `recordCancelledAttempt` only if a clean seam exists; do not force it.
- [x] 2.4 `go build ./...` and `go test -count=1 ./internal/relay/...` green; review the commit with `git diff --color-moved` to confirm moves are moves.

## 3. Split route_runtime.go

- [x] 3.1 Create `route_runtime_construct.go`, `route_runtime_select.go`, `route_runtime_recovery.go`, `route_runtime_bench.go` per design Decision 4; `route_runtime.go` keeps the `routeRuntime` type, accessors, and `routeSelectionError`. All moves verbatim; `next` and `selectionWaitError` stay whole.
- [x] 3.2 `go test -count=1 ./internal/relay/...` green.

## 4. Split relay_steps.go

- [x] 4.1 Create `relay_route_wait.go`, `relay_run_progress.go`, `relay_summary.go` per design Decision 5 (membership re-grounded per task 1.2, since #5 relocates summary/warning rendering); `relay_steps.go` keeps start/resume, spans, and scoped-message consumption.
- [x] 4.2 `go test -count=1 ./internal/relay/...` green.

## 5. Guardrail ratchet and verification

- [ ] 5.1 Regenerate the grandfather baseline: `go run ./tools/archguard --report`; confirm the `internal/relay/runner/run_one.go` production entry is gone, no new `internal/relay/runner` production file needs an entry (target: all new files < 500 lines; hard-require < 800), and runner `_test.go` entries are unchanged.
- [ ] 5.2 Verify the function inventory: every pre-change function from task 1.3 appears exactly once across the new files; the only new functions are the unexported sub-step helpers from tasks 2.2/2.3.
- [ ] 5.3 Full verification: `go test -count=1 ./...`, `go test -race -shuffle=on -count=1 ./internal/relay/...`, `go vet ./...`, `gofmt -l .` empty, `go run ./tools/archguard --ci` exit 0, `just check` green.
- [ ] 5.4 Confirm behaviour preservation: no CLI-output, telemetry-field, store-shape, laps-semantic, or error-string diff; exported `runner`/`relay` API unchanged; `internal/buildinfo/VERSION` untouched.
- [ ] 5.5 Record the final production file list of `internal/relay/runner` in this change (append to design.md or the wrapup notes) so #8 `decompose-large-test-files` can mirror the test split one-for-one.
