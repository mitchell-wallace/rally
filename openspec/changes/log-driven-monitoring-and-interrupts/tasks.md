## 1. Live monitor refactor

- [ ] 1.1 In `internal/monitor/`, change the last-activity source from workspace-file mtime to active-try log file mtime; thread the log path through from the relay runner
- [ ] 1.2 Add a concern-heuristic predicate (`logSilentSecs > 30 && tcpConns == 0`) and gate the connection/IO renderers on it
- [ ] 1.3 Remove the workspace-file mtime scanner if it has no remaining callers
- [ ] 1.4 Unit tests: steady-state line excludes conn/IO; heuristic flips on at the threshold; non-Linux path renders without conn/IO even when triggered
- [ ] 1.5 Snapshot test of the rendered line in steady-state and concern-state

## 2. Token estimator

- [ ] 2.1 Add `CharsPerToken() float64` to the executor adapter interface; default zero (= unknown)
- [ ] 2.2 Implement the divisor for each in-tree adapter (claude, codex, opencode, gemini); pick a sensible default per harness based on observed log content
- [ ] 2.3 Add a `tokenEstimate(logBytes int64, divisor float64) string` helper in `internal/monitor/`; format as `~Nk tok` or `~N tok` or `—`
- [ ] 2.4 Wire the helper into the live monitor render path
- [ ] 2.5 Unit tests: each format range; zero-divisor → `—`; large values use `k` suffix

## 3. `rally tail` subcommand

- [ ] 3.1 Add `internal/cli/tail.go` with the `rally tail [--try N]` cobra subcommand
- [ ] 3.2 Resolve the target log path by reading `.rally/tries.jsonl`; default to most recent
- [ ] 3.3 Implement `tail -f` semantics in pure Go (open at start, read existing content, then poll for appends or use fsnotify)
- [ ] 3.4 Handle file rotation gracefully: if the inode changes, reopen
- [ ] 3.5 Unit tests: latest selection, `--try N` with valid/invalid index, empty `.rally/tries.jsonl`
- [ ] 3.6 Integration test: spawn a writer that appends to a fake log; run `rally tail` and verify content streams

## 4. Interrupt control: relay side

- [ ] 4.1 Add `internal/control/server.go` with PID file write and UDS bind on relay startup
- [ ] 4.2 On startup, detect stale PID files (recorded PID dead or not a `rally` process) and clean up; error if a live relay is already running here
- [ ] 4.3 Listen for short text commands (`SKIP`, `STOP`); flag the request in shared state read by the relay loop
- [ ] 4.4 Apply requests at iteration boundaries — `SKIP` terminates the current try gracefully and continues; `STOP` lets the current try finish and then exits
- [ ] 4.5 Skip MUST NOT consume the retry budget for the skipped iteration
- [ ] 4.6 Clean up PID file and socket on relay exit (defer in main)
- [ ] 4.7 Unit tests: stale PID cleanup, concurrent-relay rejection, command application at the next tick

## 5. Interrupt control: client side

- [ ] 5.1 Add `internal/cli/skip.go` and `internal/cli/stop.go` cobra subcommands
- [ ] 5.2 Locate the relay's UDS relative to cwd; error with `no active rally relay in this workspace` if absent
- [ ] 5.3 Send the command, print the action taken, exit immediately (no ack wait)
- [ ] 5.4 Unit tests: missing socket, stale PID file, successful send

## 6. Cross-platform gating

- [ ] 6.1 Build-tag the UDS code paths so Windows builds compile cleanly
- [ ] 6.2 On Windows, `rally skip` and `rally stop` print "not supported on Windows" and exit non-zero
- [ ] 6.3 On Windows, `rally relay` startup skips the UDS bind without erroring
- [ ] 6.4 Verify in CI on each supported platform

## 7. Documentation

- [ ] 7.1 Update README with the three new subcommands and the live-monitor display change
- [ ] 7.2 Document the `.rally/relay.pid` and `.rally/relay.sock` files (ephemeral, workspace-scoped)
- [ ] 7.3 v0.3.0 release notes: livenes-signal change rationale, demoted metrics, new subcommands, Windows caveat

## 8. Verification

- [ ] 8.1 End-to-end: long relay running, `rally tail` streams the active log; `rally skip` advances to next iteration without consuming retry budget; `rally stop` halts cleanly after current try
- [ ] 8.2 Live-monitor display: confirm steady-state line and concern-state line on Linux; confirm steady-state line on macOS (no conn/IO even when log goes quiet)
- [ ] 8.3 Token estimator: confirm `~Nk tok` displays for each in-tree harness; confirm `—` displays when divisor is zero
- [ ] 8.4 Concurrent-relay protection: starting a second `rally relay` in the same workspace errors out cleanly
