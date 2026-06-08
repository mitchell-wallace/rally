# Session handoff — agent-lifecycle adversarial verification (2026-06-08)

Built rally from source (`/tmp/rally`, v0.8.7-dev). Goal: adversarially verify the
`agent-lifecycle` OpenSpec change is actually functional (all tasks were marked done),
focusing on resume behavior across every harness incl. antigravity.

## Verdict: implementation is solid; 2 real defects found + fixed, 2 stale tests fixed.

### Verified working (no change needed)
- **Graceful shutdown** (`exec.go SetProcessGroup`): `Cmd.Cancel` → group-wide SIGINT via
  `reliability.KillProcessGroup`, `WaitDelay` backstop. `freeze_unix.go` maps
  `signalTerminate`→SIGINT (unified with cancel path). Tests:
  `TestSetProcessGroupCancelKillsEntireProcessGroup`, `...SignalsGroupThenKills`,
  `TestProcessGroupKillerGracefulDrain/EscalatesAfterDrain`.
- **Responsive stop/quit** (`runner.go runActionLoop`): ActionQuit cancels+drains+breaks,
  ActionStop finishes try, second-quit escalates via `forceKillGroup`, `SetStopping`
  indicator. Tests: `runner_action_loop_test.go` (QuitCancelsAndAbortsWithoutWaiting,
  StopLetsTryFinish, StalledAttemptQuitsPromptly, SecondQuitForceKills,
  PauseCapturesSessionID).
- **Shortcut labels**: tier 0 = "graceful stop"/"quit now" — confirmed rendering live in
  real-backend monitor output.
- **Single-runner lane warning**: `route_runtime.go:154/162` emits at relay start;
  observed firing in real-backend logs.
- **codex resume**: end-to-end CLI proven (memorize codeword → resume → recalled
  PURPLE-WALRUS-42). Rally's flag-before-subcommand ordering
  (`codex exec [flags] resume <id> ...`) works; `exec resume` accepts `--output-schema`/`-o`/`--json`.
- **gemini ResumeSupported()=false**: CORRECT — gemini `--resume` is index/`latest` only,
  not a session UUID. Do not "fix" to true.

### Defects found + FIXED (commit d880dac, v0.8.7)
1. **opencode honest-resume was dead.** It passed `--session` and reported
   `ResumeSupported()=true`, but `parseOpenCodeOutput` never captured the `sessionID`
   field → `result.SessionID` always empty → resume never fired. Fixed: added
   `SessionID` to `opencodeJSONEvent`, capture first non-empty into all post-scan return
   paths. New test `TestParseOpenCodeOutput_CapturesSessionID`. CLI resume proven
   (ORANGE-FALCON-7 recalled with `--session`).
2. **codex pipe race.** `runCodexCommand` called `cmd.Wait()` before draining the stdout
   scanner goroutine → intermittent `read |0: file already closed` (flaky codex resume
   contract tests under load). Fixed: `<-scanErr` before `cmd.Wait()`.

### Stale tests fixed (same commit) — pre-existing, NOT from agent-lifecycle
Pause semantics changed 2026-05-28 (`harden-relay-run-lifecycle`): pause now requires
`FailureInfra && infraFailures > 1`; plain failures no longer pause. Two real-backend
tests still asserted the old "any failure pauses" behavior:
- `TestRealBackend_ResilienceRetryBudget`: now writes a `rate limit` line to
  `opts.LogPath` to exercise the real infra-pause path (needs `r.sleepFunc` stub +
  `Resolver` pinning model `default`). PASS (30s, deterministic).
- `TestRealBackend_OpenCodeRelay`: only requires a pause event when the failure log shows
  an infra signal; a plain "no changes" failure is a valid non-paused outcome. PASS.

### Test status
- `go test ./...` (unit): all green.
- Real-backend suite: all green after fixes. (Pre-fix: OpenCodeRelay + ResilienceRetryBudget
  failed on stale assertions.)

## Followups completed (commit set 2, v0.8.8)
- **opencode workspace isolation FIXED.** Root cause: opencode's client/server model
  resolves file paths against the SERVER cwd, not the `opencode run` client's `cmd.Dir`,
  so it wrote task files into the `go test` process cwd (`internal/relay/`). Fix: pass
  `--dir opts.WorkspaceDir` to `opencode run` (verified: file lands in target, nothing
  leaks to cwd; real OpenCodeRelay run no longer recreates `opencode-e2e.txt`).
- **`git rm internal/relay/opencode-e2e.txt`** — the tracked artifact (originally
  auto-committed by a rally self-relay, commit 39fef5a).
- **Session-capture contract test** `TestResumeSupportImpliesSessionCapture`: enumerates
  all executors; every `ResumeSupported()==true` harness must have a fixture proving its
  parse path captures a non-empty session ID from realistic output. Proven to catch the
  opencode bug (temporarily reverted capture → test goes red with the exact message).
  Guards future resume-harness additions.
- **Skill: added §6 "Adversarial verification of an OpenSpec change (reference)"** —
  spec-scenarios-as-oracle, behavioural 2-step probes, verify-both-ends-of-the-data-flow,
  distrust contract tests that inject their own precondition, common looks-done defect
  classes, and the catch→patch→prove-the-regression-test loop.

## Outstanding / for next session
- The unrelated openspec "Update Notes" edits + `.laps/laps.json` + `.rally/summary.jsonl`
  in the working tree are pre-existing/from other work — left untouched.
- Did not drive an interactive Ctrl+P→resume through the real TUI (hard to automate);
  resume proven by composition (unit capture test + CLI resume proof + runner plumbing
  trace + action-loop tests).
- Other harnesses use `cmd.Dir` only; they didn't leak in testing, but if a future
  harness adopts a daemon model, re-check workspace isolation with a `git status` after a
  real run.
