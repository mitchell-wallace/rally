## Why

Rally controls long-running agent subprocesses but handles their lifecycle bluntly.
Cancelling a try (double Ctrl+C, Ctrl+X) sends a bare SIGKILL via
`exec.CommandContext`, so claude/gemini/opencode get no chance to flush or clean up.
Pause (Ctrl+P) cancels the try and, on resume, throws away the partial work and starts
a fresh try — even though claude and antigravity support `--resume <session-id>`. And
the shortcut labels ("stop", "quit") don't say whether they act now or after the
current try, so operators can't tell graceful from immediate.

Two QA findings are routed here because they are lifecycle/routing concerns, not new
features:
- **R9 route/runner fallback** — the fallback chain (e.g. `senior =
  ['claude','kimi','gpt']`) already exists; the routing Scheduler rotates a lane to the
  next entry when the current one is unavailable. The stalled Prayer-app relay had a
  single-entry lane (`senior=['claude']`) with nothing to rotate to. So the gap is not
  a feature build — it is a dependency on `harden-relay-run-lifecycle`'s failure
  classification (so a dying runner is actually marked unavailable) plus operator
  guidance for single-runner lanes.
- **R12/R13 VERIFY boundary** — VERIFY should default to reporting-focused (trivial
  fixes allowed, substantial gaps → head lap); the
  "mark off `tasks.md`" behavior is OpenSpec-specific and belongs in `prepare-laps`,
  not the generic role doc or rally core.

## What Changes

- **Graceful subprocess shutdown.** In `SetProcessGroup`, set `Cmd.Cancel` to send
  SIGINT to the process group and `Cmd.WaitDelay = 5s` so Go escalates to SIGKILL only
  if the subprocess doesn't exit in time. All executors already call
  `SetProcessGroup`.
- **Pause-now + session resume.** Make pause cancel the current attempt immediately
  (via the graceful shutdown), capture the harness session ID, and store it in
  run-state. On resume — and on retry of a try with meaningful partial progress
  (running > ~3 min or > ~3 non-`.rally`/non-log file changes) — pass
  `--resume <session-id>` when the harness declares resume support, instead of starting
  fresh. A run explicitly skipped does not resume.
- **Shortcut label renames.** "stop" → **"graceful stop"** (Ctrl+X, stops after the
  current try); "quit" → **"quit now"** (Ctrl+C, immediate via graceful shutdown), in
  `style.ShortcutHint()`.
- **Single-runner lane warning (R9).** At relay start, warn when a lane has only one
  runner entry (no fallback), so the operator knows a single dead harness can stall that
  lane. Plus docs/defaults encouraging multi-runner lanes.
- **VERIFY role default boundary (R12/R13).** The generic VERIFY role doc
  (`.rally/agents/verify.md`) stays reporting-focused (trivial fixes allowed,
  substantial gaps → head lap) and OpenSpec-agnostic; the
  "mark off `tasks.md`" behavior is injected per-lap by `prepare-laps` only when a lap
  has a related OpenSpec change. No separate OpenSpec↔laps sync mechanism.

## Capabilities

### Added Capabilities
- `agent-lifecycle`: graceful subprocess shutdown, pause-now + session resume,
  shortcut-label clarity, single-runner-lane warning, and the generic VERIFY role
  reporting boundary.

## Impact

- **Code**: `internal/agent/exec.go` (`SetProcessGroup`: `Cmd.Cancel`, `WaitDelay`),
  the pause/resume + run-state path (`internal/relay/runner.go`, run-state storage),
  harness `ResumeSupported()` / `--resume` plumbing per executor (`internal/agent/`),
  `internal/style/style.go` (labels), relay-start lane validation
  (`internal/routing/` + relay setup), and the default `verify.md` role doc.
- **Behavior**: subprocesses exit cleanly on cancel; pause/resume reuses a session
  instead of restarting; shortcut labels state timing; operators are warned about
  single-runner lanes; VERIFY defaults to reporting.
- **Coordination with `harden-relay-run-lifecycle` (#1)** — #1 ships first:
  - **Stall-kill path**: #1 renames the liveness detector freeze→**stall** and owns its
    kill→recovery path; align so the stall kill uses this change's graceful shutdown,
    not bare SIGKILL.
  - **Resume / run-state**: pause-now + session resume here overlaps #1's freeze-decay +
    `--new` reset (both touch resume and run-state) — sequence to avoid conflicts.
  - **`Frozen`→`Benched`**: #1 renames scheduler `EntryState.Frozen`→`Benched`; the
    rotation referenced by R9 uses that renamed field, and R9's effectiveness depends on
    #1's infra-failure classification marking entries unavailable.
- **Coordination with `cli-polish` (#4)**: `style.ShortcutHint()` is edited by both —
  this change owns the label text ("graceful stop" / "quit now"), #4 owns the layout
  (width-aware truncation, left-align). Co-implement or sequence.
- **Note**: `FallbackConfig` is unrelated to runner failover — it is the free-run
  default prompt, renamed to `FreeRunPrompt` in `cli-polish`.
