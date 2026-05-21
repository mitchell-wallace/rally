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
