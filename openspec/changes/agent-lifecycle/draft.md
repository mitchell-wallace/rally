# Agent Lifecycle — Graceful Shutdown, Pause/Resume, Shortcut Renames

## Graceful Subprocess Shutdown

Currently `exec.CommandContext` sends SIGKILL when the context is cancelled
(double Ctrl+C or Ctrl+X). Subprocesses (claude, gemini, opencode, etc.) get
no chance to clean up.

**Change**: In `SetProcessGroup` (internal/agent/exec.go), set `Cmd.Cancel` to
send SIGINT to the process group, and `Cmd.WaitDelay = 5s`. If the subprocess
hasn't exited after 5 seconds, Go auto-sends SIGKILL.

```go
cmd.Cancel = func() error {
    return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}
cmd.WaitDelay = 5 * time.Second
```

Small, isolated change — all executors already call `SetProcessGroup(cmd)`.

---

## Pause/Resume Agents

Currently pause (Ctrl+P) cancels the try context and waits for the process to
die. When the relay resumes it starts a fresh try. This is wasteful — agents
like Claude and Antigravity support `--resume <session-id>`.

**Change**: Make pause a "pause now" instead of "pause after next try completes":

1. **On pause**: Cancel the attempt context (sends SIGINT via the new graceful
   shutdown). Capture the session ID from the partial result. Store it in
   run-state for resume.

2. **On resume**: If the harness declares `ResumeSupported()` and we have a
   session ID, pass `--resume <session-id>` to pick up where we left off.

3. **On retry with partial progress**: If a try has been running for >3 min or
   has >3 file changes (excluding `.rally/` and log files), and it fails or
   errors — attempt to resume instead of starting fresh. Unless the run was
   explicitly skipped, we try to resume.

This is the biggest piece of work. Will need a pass with `test-driving-rally`
after implementation to validate and tidy up.

### Harness resume capability

| Harness      | Resume flag            | Notes                          |
|--------------|------------------------|--------------------------------|
| claude       | `--resume <session>`   | Already tracked via sessionID  |
| antigravity  | `--resume <session>`   | Already tracked via sessionID  |
| gemini       | TBD — check CLI docs   | May not support resume yet     |
| opencode     | TBD — check CLI docs   | May not support resume yet     |
| codex        | TBD — check CLI docs   | May not support resume yet     |

---

## Rename Shortcut Headers

Current labels are ambiguous:
- "stop" → rename to **"graceful stop"** (Ctrl+X — stops after current try)
- "quit" → rename to **"quit now"** (Ctrl+C — immediate via graceful shutdown)

Update `style.ShortcutHint()` in `internal/style/style.go`:
```
[Ctrl+S skip] [Ctrl+P pause] [Ctrl+X graceful stop] [Ctrl+C quit now]
```

> **Overlaps `cli-polish`**, which also edits `style.ShortcutHint()` (width-aware
> truncation, left-align). Co-implement or sequence so the rename and the layout
> work don't clobber each other.

---

## Route/runner fallback (QA R9)

The fallback chain (`senior = ['claude','kimi','gpt']`) **already exists** — the
routing Scheduler rotates a lane to the next entry when the current one becomes
unavailable (`internal/routing/scheduler.go`). So this is not a feature build.
What remains:

- **Depends on `harden-relay-run-lifecycle`'s failure classification** so that
  infra failures (rate limit, harness/launch error, API timeout) mark the entry
  unavailable and rotation actually triggers — without that, a dying single-lane
  runner just stalls (as it did in the Prayer-app run).
- **Relay-start warning** when a lane has only one runner entry (no fallback), so
  the operator is told a single dead harness can stall that lane.
- **Docs/defaults** encouraging multi-runner lanes.

(Note: `FallbackConfig` is unrelated — it is the free-run default prompt, renamed
to `FreeRunPrompt` in `cli-polish`.)

## VERIFY role boundary (QA R12 / R13)

VERIFY should default to read-only/reporting; large gaps it finds should become a
new head lap rather than being fixed inline. Keep this split clean:

- The **generic** VERIFY role doc (`.rally/agents/verify.md`) stays
  OpenSpec-agnostic.
- The "mark off `tasks.md`" behavior is OpenSpec-specific and is injected
  **per-lap by the `prepare-laps` skill** only when a lap has a related OpenSpec
  change — not baked into rally core or the default role doc. This subsumes the
  OpenSpec↔laps "bridge" (R13): no separate sync mechanism.

See the rally/laps/OpenSpec boundary rules in `AGENTS.md`.

## Coordination with `harden-relay-run-lifecycle` (#1)

- **Stall-kill path**: the graceful subprocess shutdown above (SIGINT +
  `WaitDelay`) changes how a cancelled/stalled try is killed. #1 renames the
  liveness detector freeze→**stall** and owns its kill→recovery path. Align so
  the stall detector uses the new graceful shutdown, not the old SIGTERM/SIGKILL.
- **Resume / run-state**: pause-now + session resume here, and #1's freeze-decay
  + `--new` reset, both touch the resume path and run-state. Sequence to avoid
  conflicting edits. #1 ships first.
- **`frozen` vs `benched`**: #1 also renames scheduler `EntryState.Frozen`→
  `Benched`; the rotation logic referenced under R9 uses that renamed field.
