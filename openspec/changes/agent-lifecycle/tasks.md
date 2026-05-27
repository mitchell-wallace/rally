## 1. Graceful subprocess shutdown

- [ ] 1.1 In `SetProcessGroup` (`internal/agent/exec.go`), set `Cmd.Cancel` to send SIGINT to the process group and `Cmd.WaitDelay = 5s`
- [ ] 1.2 Confirm all executors route through `SetProcessGroup` so the change applies uniformly
- [ ] 1.3 Align the stall detector's kill path (renamed freeze→stall in `harden-relay-run-lifecycle`) to use this graceful shutdown, not bare SIGKILL
- [ ] 1.4 Tests: cancel sends SIGINT then escalates to SIGKILL after `WaitDelay`

## 2. Pause-now + session resume

- [ ] 2.1 Make pause cancel the current attempt immediately (via graceful shutdown) instead of waiting for the try to complete
- [ ] 2.2 Capture the harness session ID from the partial result and store it in run-state
- [ ] 2.3 Add `ResumeSupported()` to the harness/executor interface; implement for claude and antigravity (`--resume <session>`); gate others (gemini/opencode/codex) until verified
- [ ] 2.4 On resume, pass `--resume <session-id>` when supported and a session ID exists; otherwise start fresh
- [ ] 2.5 On retry with meaningful partial progress (ran > ~3 min or > ~3 file changes excluding `.rally/` + logs), attempt resume unless the run was explicitly skipped
- [ ] 2.6 Tests: pause captures session ID; resume reuses it when supported; unsupported harness falls back to fresh; explicit skip starts fresh; resume failure degrades to fresh try
- [ ] 2.7 Run a `test-driving-rally` validation pass after implementation

## 3. Shortcut label renames

- [ ] 3.1 In `style.ShortcutHint()`, rename "stop"→"graceful stop" (Ctrl+X) and "quit"→"quit now" (Ctrl+C)
- [ ] 3.2 Coordinate with `cli-polish` (width-aware layout edits the same function) — labels here, layout there
- [ ] 3.3 Tests/fixtures: hint contains the renamed labels

## 4. Single-runner lane warning (R9)

- [ ] 4.1 At relay start, detect lanes with a single runner entry and warn that one dead harness can stall the lane
- [ ] 4.2 Encourage multi-runner lanes in docs/defaults
- [ ] 4.3 Confirm the dependency: rotation only triggers once `harden-relay-run-lifecycle` classifies infra failures and marks entries `Benched`/`Exhausted`
- [ ] 4.4 Tests: single-entry lane warns; multi-entry lane does not

## 5. VERIFY role default boundary (R12/R13)

- [ ] 5.1 Make the default `verify.md` role doc read-only/reporting: large gaps become a new head lap, not inline fixes
- [ ] 5.2 Keep the generic role doc OpenSpec-agnostic — no "mark off tasks.md" in rally core or the default doc
- [ ] 5.3 Confirm the OpenSpec-specific tasks.md behavior is injected per-lap by `prepare-laps` only for laps with a related change (no separate sync mechanism)
- [ ] 5.4 Cross-check against the boundary rules in `AGENTS.md`

## 6. Docs & coordination

- [ ] 6.1 Document graceful stop / quit now, pause-now/resume behavior, and the single-runner-lane warning in `README.md`/`AGENTS.md`
- [ ] 6.2 Confirm sequencing after `harden-relay-run-lifecycle` (#1): stall-kill path, run-state shape, and `Frozen`→`Benched` rename are settled first
- [ ] 6.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
