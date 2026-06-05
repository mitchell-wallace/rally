## 1. Graceful subprocess shutdown (unified on SIGINT)

- [ ] 1.1 In `SetProcessGroup` (`internal/agent/exec.go`), set `Cmd.Cancel` to send SIGINT to the process group (`syscall.Kill(-pid, SIGINT)`) and `Cmd.WaitDelay = 5s`
- [ ] 1.2 Confirm all executors route through `SetProcessGroup` so the change applies uniformly (claude, antigravity, gemini, opencode, codex, generic)
- [ ] 1.3 Switch the stall killer's `signalTerminate` mapping from SIGTERM to SIGINT in `internal/reliability/freeze_unix.go:14` (grep all build-tag variants of `sendProcessGroupSignal` first — today only `freeze_unix.go` maps it; `freeze_windows.go` is an unsupported stub — so a future non-unix variant isn't missed)
- [ ] 1.4 Re-validate `internal/reliability/stall_test.go` signal-sequence assertions still hold (they assert the abstract `signalTerminate`→`signalKill` enums at `:171,:204`, not the OS signal; only the mapping changes)
- [ ] 1.5 Tests: cancel sends SIGINT to the **process group** (negative PID, `-pid`) — not just the leader — then escalates to SIGKILL after `WaitDelay`. Pin the group-reach win: today's default `CommandContext` cancel SIGKILLs only `cmd.Process`, so the test must assert group targeting, not merely SIGINT-vs-SIGKILL

## 2. Responsive stop / quit

- [ ] 2.1 Split the shared `ActionStop`/`ActionQuit` branch (`internal/relay/runner.go:978-980`): `ActionQuit` SHALL `cancelAttempt()`, set `stopFlag`, drain `tryCh`, and `break actionLoop` (immediate); `ActionStop` keeps "set `stopFlag`, finish current try, stop after"
- [ ] 2.2 Keep the action loop responsive during the cancel drain: after `cancelAttempt()` keep selecting on `actionCh` so a second Ctrl+C within the ≤5s `WaitDelay` window escalates to an immediate SIGKILL of the process group
- [ ] 2.3 Surface a "stopping…" indicator (monitor/footer) while a cancel drains so the UI never looks frozen
- [ ] 2.4 Tests: Ctrl+C cancels the running attempt and aborts the relay without waiting for the try to finish; Ctrl+X lets the current try finish then stops; a frozen/stalled agent ends promptly on Ctrl+C rather than waiting for the stall threshold

## 3. Honest session resume across harnesses

- [ ] 3.1 Audit each executor: a `ResumeSupported()==true` harness MUST pass its resume flag when `opts.ResumeSessionID != ""`. Current truth — claude (`--resume`) ✅, antigravity (`--conversation=`) ✅; gemini, opencode, codex ❌ (claim support, ignore the session)
- [ ] 3.2 Wire the real resume flag for gemini (`internal/agent/gemini.go`), opencode (`internal/agent/opencode.go`), and codex's main `Execute` (`internal/agent/codex.go:173` — use `exec resume <sessionID>` like the liveness probe), after confirming each CLI's actual resume invocation against its `--help`; OR set `ResumeSupported()=false` for any harness whose resume cannot be verified
- [ ] 3.3 Preserve the existing runner-side resume policy: resume on any retry with a tracked session; `FreshRestart` clears the session (`runner.go:1157-1160`). Do NOT add a "meaningful progress" heuristic
- [ ] 3.4 On resume failure, preserve the existing degrade-to-fresh-try behavior rather than erroring the run
- [ ] 3.5 Contract test: for every executor, `ResumeSupported()==true` implies the built command args contain the resume flag when `ResumeSessionID` is set
- [ ] 3.6 Regression tests (assert already-working behavior is preserved): pause captures the session ID; resume reuses it on the next attempt; explicit skip starts fresh; `FreshRestart` starts fresh
- [ ] 3.7 Run a `test-driving-rally` pause/resume validation pass after implementation

## 4. Shortcut label renames

- [ ] 4.1 In `style.shortcutHintTiers` (`internal/style/style.go:29-34`, four tiers indexed 0=full, 1=medium, 2=narrow, 3=minimal — see `style_test.go:365-368`), update labels per tier and re-confirm each still fits its width: tier 0 (full) carries "graceful stop"/"quit now"; tiers 1-2 keep terse "stop"/"quit" (already abbreviated-key, width-constrained); tier 3 has no word labels (`^S·^P·^X·^C`) — unchanged. Coordinate any width regression with `cli-polish` tier widths
- [ ] 4.2 Update `internal/style/style_test.go` tier-width fixtures/assertions to the new labels and confirm every tier still fits its width
- [ ] 4.3 Note: the width-aware tier layout already shipped in `cli-polish` — change label text only, do not re-do layout

## 5. Single-runner lane warning (R9)

- [ ] 5.1 Detect single-runner lanes where the per-lane schedulers are built in `internal/relay/route_runtime.go:130-145` (`routing.NewScheduler(resolvedEntries)` per route; a lane with `len(resolvedEntries)==1` has no fallback). Emit a warning to the operator-facing sink (stderr/monitor — pick one and assert it in 5.4) that one dead harness can stall that lane
- [ ] 5.2 Encourage multi-runner lanes in docs/defaults
- [ ] 5.3 Confirm the dependency is already satisfied: `harden-relay-run-lifecycle` already classifies infra failures and marks entries `Benched`/`Exhausted`, and the scheduler already rotates off them
- [ ] 5.4 Tests: single-entry lane warns; multi-entry lane does not

## 6. VERIFY role default boundary (R12/R13)

- [ ] 6.1 Confirm/align the embedded default role doc `internal/agent_prompt/roles/verify.md` (the authoritative source; `.rally/agents/verify.md` is the installed copy): reporting-focused, trivial clearly-correct fixes allowed, substantial gaps become a new head lap. It already states this — reconcile the spec language to it, do not remove the small-fix allowance
- [ ] 6.2 Keep `internal/agent_prompt/roles/verify.md` OpenSpec-agnostic — no "mark off tasks.md" in rally core or the default doc
- [ ] 6.3 Confirm the OpenSpec-specific `tasks.md` behavior is injected per-lap by `prepare-laps` only for laps with a related change (no separate sync mechanism)
- [ ] 6.4 Cross-check against the boundary rules in `AGENTS.md`

## 7. Docs & coordination

- [ ] 7.1 Document graceful stop / quit now timing, pause-now/resume behavior, and the single-runner-lane warning in `README.md`/`AGENTS.md`
- [ ] 7.2 Note in docs that the interactive shortcuts require a double-press within the confirm window (`internal/keyboard`), so a single press intentionally does nothing
- [ ] 7.3 Coordinate label text with `cli-polish` (it owns the tier layout; this change owns the words)
- [ ] 7.4 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
