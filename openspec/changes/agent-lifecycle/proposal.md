## Why

Rally controls long-running agent subprocesses but handles their lifecycle bluntly,
and the interactive shutdown shortcuts (skip / pause / stop / quit) are unreliable —
especially once an agent is stalled. Concretely, against the **current** code (after
`harden-relay-run-lifecycle` landed):

- **Cancel is a bare SIGKILL.** Pause/skip/quit cancel the attempt context, which
  flows through `exec.CommandContext` and sends SIGKILL with no SIGINT and no grace
  window (`internal/agent/exec.go` `SetProcessGroup` sets only `Setpgid`). The harness
  gets no chance to flush its session or reap child processes.
- **"stop" and "quit" don't act immediately — the root of the "shortcuts don't
  respond when frozen" complaint.** `ActionStop` and `ActionQuit` are handled
  identically: both only set `r.stopFlag` and let the *current try run to completion*
  (`internal/relay/runner.go:978-980`). They never cancel the attempt. For a stalled
  agent the try only ends when the stall detector kills it (up to
  `DefaultStallThreshold` = 180s) — or never, if the stall is ambiguous. So operators
  press Ctrl+C/Ctrl+X and nothing visibly happens. (Skip and pause *do* respond — they
  call `cancelAttempt()`.)
- **Resume silently no-ops on most harnesses.** Pause-now and session resume already
  exist (`ActionPause` cancels immediately, the next attempt passes the resume flag via
  `RunOptions.ResumeSessionID`), but only **claude** (`--resume`) and **antigravity**
  (`--conversation=`) actually wire it. **gemini, opencode, and codex** return
  `ResumeSupported()=true` yet their `Execute` never passes the session — so resume
  appears to work and silently starts fresh.
- **Shortcut labels don't convey timing.** "stop" vs "quit" gives no hint that one acts
  after the current try and the other should act now.

Two QA findings are routed here because they are lifecycle/routing concerns, not new
features:
- **R9 route/runner fallback** — the fallback chain (e.g. `senior =
  ['claude','kimi','gpt']`) already exists; the routing Scheduler rotates a lane to the
  next entry when the current one is `Benched`/`Exhausted`. The stalled Prayer-app relay
  had a single-entry lane (`senior=['claude']`) with nothing to rotate to. So the gap is
  not a feature build — it is operator guidance for single-runner lanes plus the
  already-landed infra-failure classification from `harden-relay-run-lifecycle`.
- **R12/R13 VERIFY boundary** — VERIFY should default to reporting-focused (trivial
  fixes allowed, substantial gaps → head lap); the "mark off `tasks.md`" behavior is
  OpenSpec-specific and belongs in `prepare-laps`, not the generic role doc or rally
  core.

## What Changes

- **Graceful subprocess shutdown on cancel.** In `SetProcessGroup`, set `Cmd.Cancel` to
  send SIGINT to the process group and `Cmd.WaitDelay = 5s` so Go escalates to SIGKILL
  only if the subprocess doesn't exit in time. All executors already call
  `SetProcessGroup`. **Unify the signal:** the stall detector's existing
  `processGroupKiller` currently sends SIGTERM→drain→SIGKILL; switch it to SIGINT so both
  the cancel path and the stall-kill path use the same graceful-interrupt signal that
  CLI agents handle for clean flush.
- **Make stop/quit responsive (the responsiveness fix).** Differentiate the two handlers
  that are identical today:
  - **"quit now" (Ctrl+C)** SHALL cancel the current attempt immediately (via the
    graceful shutdown), then abort the relay — instead of waiting for the try to finish.
  - **"graceful stop" (Ctrl+X)** keeps "finish the current try, then stop" semantics.
  - Keep the action loop responsive *while* a cancel drains: after `cancelAttempt()` the
    runner currently blocks on `<-tryCh`; keep selecting on `actionCh` so a second
    Ctrl+C during the (≤5s) drain can escalate to immediate SIGKILL and the UI shows a
    "stopping…" state rather than appearing frozen.
- **Honest resume support.** A harness whose `ResumeSupported()` returns true SHALL
  actually pass its resume flag when `ResumeSessionID` is set. Audit each executor;
  wire the real resume flag for **gemini / opencode / codex** (verifying each CLI's
  actual resume invocation) or make `ResumeSupported()` return false until wired. claude
  and antigravity are already correct. Resume continues to fire on every retry that has
  a tracked session (no new "meaningful progress" heuristic) and `FreshRestart` still
  clears the session — behavior preserved, just made truthful across harnesses.
- **Shortcut label renames.** "stop" → **"graceful stop"** (Ctrl+X, stops after the
  current try); "quit" → **"quit now"** (Ctrl+C, immediate via graceful shutdown), in
  `style.shortcutHintTiers`.
- **Single-runner lane warning (R9).** At relay start, warn when a lane has only one
  runner entry (no fallback), so the operator knows a single dead harness can stall that
  lane. Plus docs/defaults encouraging multi-runner lanes.
- **VERIFY role default boundary (R12/R13).** The generic VERIFY role doc
  (`.rally/agents/verify.md`) stays reporting-focused (trivial fixes allowed,
  substantial gaps → head lap) and OpenSpec-agnostic; the "mark off `tasks.md`" behavior
  is injected per-lap by `prepare-laps` only when a lap has a related OpenSpec change. No
  separate OpenSpec↔laps sync mechanism.

## Capabilities

### Added Capabilities
- `agent-lifecycle`: graceful subprocess shutdown, responsive stop/quit, honest session
  resume across harnesses, shortcut-label clarity, single-runner-lane warning, and the
  generic VERIFY role reporting boundary.

## Impact

- **Code**: `internal/agent/exec.go` (`SetProcessGroup`: `Cmd.Cancel`, `WaitDelay`),
  `internal/reliability/freeze_unix.go` + `stall.go` (unify `signalTerminate` →
  SIGINT), the action loop and stop/quit handlers (`internal/relay/runner.go`
  ~`:961-982`, `:1288-1308`), resume wiring per executor
  (`internal/agent/{gemini,opencode,codex}.go`), `internal/style/style.go`
  (`shortcutHintTiers`), relay-start lane validation (`internal/routing/` + relay
  setup), and the default `verify.md` role doc.
- **Behavior**: subprocesses exit cleanly on cancel; Ctrl+C aborts the running try
  immediately; Ctrl+X stops after the current try; pause/resume reuses a session on
  every harness that claims support; shortcut labels state timing; operators are warned
  about single-runner lanes; VERIFY defaults to reporting.
- **Already landed in `harden-relay-run-lifecycle` (archived 2026-05-29)** — do not
  re-build; verify against and align with:
  - **Graceful stall kill**: the stall detector already kills via
    `processGroupKiller` (SIGTERM→drain→SIGKILL). This change only switches that signal
    to SIGINT for consistency; it is *not* a bare-SIGKILL path to be replaced.
  - **`Benched`/`Exhausted` scheduler state**: the `Frozen`→`Benched` rename is done;
    R9's rotation already consumes it and infra-failure classification already marks
    entries unavailable.
  - **Pause-now + resume-on-retry**: `ActionPause` already cancels immediately and the
    next attempt resumes via `ResumeSessionID`; this change makes that resume *actually
    fire* on gemini/opencode/codex and adds regression coverage — it does not introduce
    pause/resume from scratch.
- **Coordination with `cli-polish` (#4)**: `style.shortcutHintTiers` is owned by both —
  this change owns the label text ("graceful stop" / "quit now"), #4 already shipped the
  width-aware tier layout. Update the tier strings here; do not re-do the layout.
- **Note**: `FreeRunConfig`/`FreeRunPrompt` (formerly `FallbackConfig`) is the free-run
  default prompt and is unrelated to runner failover.
