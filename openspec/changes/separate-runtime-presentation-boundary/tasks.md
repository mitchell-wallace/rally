## 1. Baseline and re-grounding

- [x] 1.1 Confirm the tree is green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go test -count=1 ./...`, `go run ./tools/archguard --ci` (exit 0). If red, STOP. — all green at HEAD `56f2b9d`.
- [x] 1.2 Regenerate the presentation wiring inventory (the design's Context section, baselined at `f55712c`): runner `internal/style`/`internal/keyboard` import sites, direct `os.Stdout`/`os.Stderr`/`os.Stdin` uses (`run_one.go`, `relay_steps.go`, `task.go`, `terminal.go`, `handoff_only.go`), keyboard construction sites, monitor lifecycle calls, and the print-site output inventory (design Decision 3 table). This inventory is the emit-site checklist for phases 3–5. — recorded in `wiring-inventory.md`; all Decision 3 rows verified, drifts flagged (RouteWarning +1, pause read now `run_one.go:1340`, OS-signal `RequestStop` now `relay_start.go:187`).
- [x] 1.3 Archive-order dependency: this change's `composition-root-structure` delta MODIFIES the same "Presentation-neutral relay-start seam" requirement as `modularize-harness-adapters`' un-archived delta, and includes #4's harnessapi wording. **#4 MUST archive/sync before this change**; if that order is ever broken, rebase whichever delta archives last to contain the other's full requirement text before archiving, or the later archive silently drops the earlier contract. — **STATUS CHANGED**: at HEAD `56f2b9d` #4 is already archived (commit `2da5e06`), so #4 archived first (correct order). Verified this change's delta is a proper superset of #4's requirement (keeps the harnessapi "Executor registry" scenario, adds the presentation-adapter sentence + scenario), so the risk is CLOSED. See `wiring-inventory.md` "Archive-order caveat" — operator note: design/tasks prose describing #4 as un-archived is now stale.

## 2. `runtimeevent` contract package

- [ ] 2.1 Create `internal/relay/runner/runtimeevent` (stdlib-only): the `Event` surface + typed data-only payloads for the design Decision 3 vocabulary; `Sink` interface with synchronous `Emit(context.Context, Event)`; no-op sink; a `Recording` sink for tests.
- [ ] 2.2 Add the operator-control vocabulary: `OperatorAction` (quit/skip/pause/stop), `Press{Action, Confirmed}`, `ArmMessage`/`ActMessage` helpers and `ConfirmWindow` (moved from `internal/keyboard` semantics — text and 4s window identical), and the `ControlSource` interface (`Start(ctx) (<-chan Press, error)`, `Stop()`, `WaitResume(ctx) error`).
- [ ] 2.3 Unit-test the package (message text parity with today's `keyboard.ArmMessage`/`ActMessage`; no-op/recording sinks). Confirm `go list -deps ./internal/relay/runner/runtimeevent` shows no internal package.

## 3. Plumb injection and parallel-emit

- [ ] 3.1 Add `EventSink runtimeevent.Sink` and `Controls runtimeevent.ControlSource` to `runner.Config` (nil-safe: no-op sink / no operator input); add the same fields to `app.RelayStartOptions`; forward in `app.StartRelay` (`internal/app/relay_start.go`) without inspection. `internal/cli/start.go` passes nil for now. `go build ./...` green.
- [ ] 3.2 Emit events at every print site from the task 1.2 inventory **alongside** the existing prints (parallel-emit; no output change). Do not move any code across `setupRunBudget`/`runBudgetCh` construction (design risk note).
- [ ] 3.3 Add recording-sink event-order tests for: simple success, retry-then-fail, operator cancellation (skip and quit), stall, rate-limit wait, and all-paused wait paths. Assert order only — no styling assertions.

## 4. Terminal sink and output cut-over

- [ ] 4.1 Create `internal/presentation/terminal`: `Sink` implementing `runtimeevent.Sink` over out/err writers, relocating each print's format strings/escape sequences (`\r\x1b[2K`, `\r\x1b[J`, `\r\n` raw-mode countdown, footer in-place redraw with cursor parking) **verbatim** from the runner; internal imports limited to `runtimeevent`, `style`, `keyboard`.
- [ ] 4.2 Wire `internal/cli/start.go` to construct the terminal sink over `os.Stdout`/`os.Stderr` and pass it through `RelayStartOptions`.
- [ ] 4.3 Cut over print-site-by-print-site: delete each runner inline print once the sink renders its event; preserve exact call order relative to `mon.Stop()`/monitor renders (footer-parking contract). Finish with zero direct operator-facing `fmt.Print*`/`Fprint*` writes to `os.Stdout`/`os.Stderr` in runner production code (the one permitted direct-stream use is the monitor residual `mon.Start(os.Stdout)`) and the `internal/style` import removed from all runner files.
- [ ] 4.4 Relocate the byte-level output tests (`terminal_test.go` wait-loop/footer cases, footer-cadence tests from `run_one`/`runner_*` suites that assert on `r.out` bytes) to `internal/presentation/terminal`, preserving expected bytes; replace their runner-side originals with recording-sink assertions where run-flow coverage must stay in-package.
- [ ] 4.5 `go test -count=1 ./internal/relay/... ./internal/presentation/... ./internal/app ./cmd/rally` green; manually smoke one relay run (`test-driving-rally` style) confirming header/status/footer/summary render identically, including an interrupted (Ctrl+C-armed) run.

## 5. ControlSource cut-over

- [ ] 5.1 Implement `terminal.Controls` with an injectable-stream constructor `terminal.NewControls(in io.Reader, out io.Writer)` (CLI passes `os.Stdin`/`os.Stdout`): wraps `keyboard.NewKeyboard(in, out)` with raw-mode session per `Start`/`Stop`, translates `keyboard.Press` → `runtimeevent.Press`, `WaitResume` performs today's leave-raw-then-read-Enter sequence against `in`. Adapter test for repeated Start/Stop cycles using pipes (no leaked readers — mirror `keyboard_test.go`'s pipe-driven approach, no global `os.Stdin` mutation).
- [ ] 5.2 Convert the runner's two keyboard sessions (`terminal.go` wait countdown; `run_one.go` active try) and the pause read (`run_one.go:1339` at baseline) to `Config.Controls`; `runActionLoop`/`actionLoopDeps` switch channel element type to `runtimeevent.Press` with **no select-structure, drain, flag, or PID-handling change**; monitor armed/acting text now via `runtimeevent.ArmMessage`/`ActMessage`.
- [ ] 5.3 Update the action-loop tests (`runner_action_loop_test.go`) and any timeout tests feeding presses to use `runtimeevent.Press` fakes; keep all seven action-loop cases plus stall/timeout precedence cases green unchanged in substance.
- [ ] 5.4 Remove the runner's `internal/keyboard` import entirely; confirm no runner production file reads `os.Stdin`. `go test -race -shuffle=on -count=1 ./internal/relay/...` green.

## 6. Guardrails and durable guidance

- [ ] 6.1 Update `tools/archguard` policy tables per design Decision 7: `relay/runner` −`style` −`keyboard` +`relay/runner/runtimeevent`; `app` +`relay/runner/runtimeevent`; new `presentation/terminal` row (`relay/runner/runtimeevent`, `style`, `keyboard`); diagnostics explain the runner-emits/adapters-render intent. Update the pinned policy tests.
- [ ] 6.2 `go run ./tools/archguard --report` regenerated (no new grandfather entries; note any runner line-count reductions for #6's re-grounding) and `--ci` exit 0.
- [ ] 6.3 Update the `build-new-tui` stub proposal to name `internal/relay/runner/runtimeevent` as the boundary the TUI consumes (events in, `Press` controls out; monitor status line to be replaced, not adapted — design Decision 8).

## 7. Verification

- [ ] 7.1 Full: `go test -count=1 ./...`, `go test -race -shuffle=on -count=1 ./internal/relay/... ./internal/presentation/...`, `go vet ./...`, `gofmt -l .` empty, `go mod tidy` no diff, `just check` green.
- [ ] 7.2 Behaviour preservation: CLI output byte-identical on the smoke flows (4.5); shortcut semantics identical (arm/confirm window, skip/pause/stop/quit, second-quit force-kill during drain, pause Enter-resume); no telemetry/store/laps/config/commit-message diff; `internal/buildinfo/VERSION` untouched.
- [ ] 7.3 Confirm residuals are intact and documented: monitor status line still runner-driven (`mon.Start(os.Stdout)`), relay-log lines unchanged and not events; app OS-signal Ctrl+C path untouched.
