## Context

Rally launches agents as subprocesses through `internal/agent` executors, all of which
call `SetProcessGroup(cmd)`. Cancellation today flows through
`exec.CommandContext`, which sends SIGKILL when the context is cancelled — no SIGINT,
no grace period, so the harness can't flush its session or clean up child processes.

Pause (Ctrl+P) cancels the attempt context and waits for the process to die; on resume
the relay starts a fresh try, discarding partial work. Claude and antigravity both
support `--resume <session-id>` and rally already tracks the session ID, so the restart
is wasted work.

The shortcut hint labels ("stop", "quit") don't convey timing — operators can't tell
that Ctrl+X stops after the current try while Ctrl+C acts immediately.

Two QA items land here. **R9** (runner fallback) is already implemented as Scheduler
lane rotation (`internal/routing/scheduler.go`): when a lane's current entry is
unavailable (`Benched`/`Exhausted` after `harden-relay-run-lifecycle`'s rename), the
scheduler advances to the next entry. The Prayer-app stall was a single-entry lane with
nothing to rotate to, compounded by a dying runner never being marked unavailable
(fixed by #1's classification). So R9 reduces to a dependency + operator guidance, not a
feature. **R12/R13** (VERIFY boundary) is governed by the rally/laps/OpenSpec boundary
in `AGENTS.md`: the generic role doc stays OpenSpec-agnostic; OpenSpec coupling lives in
`prepare-laps`.

## Goals / Non-Goals

**Goals:**
- Subprocesses get a SIGINT + grace window before SIGKILL on cancel.
- Pause is "pause now"; resume reuses the harness session where supported.
- Shortcut labels state whether they act now or after the current try.
- Operators are warned when a lane has no fallback runner.
- The default VERIFY role is reporting-focused (trivial fixes allowed, substantial gaps → head lap) and OpenSpec-agnostic.

**Non-Goals:**
- Building a new fallback/rotation mechanism (it already exists; R9 is dependency + docs).
- A separate OpenSpec↔laps sync mechanism (R13 — subsumed by the `prepare-laps` coupling).
- Adding resume support to harnesses that lack a `--resume` flag (gemini/opencode/codex
  are TBD; gate on a capability check).
- The freeze-decay / `--new` reset logic itself (owned by #1; this change only consumes
  the renamed scheduler field and aligned kill path).

## Decisions

**1. Graceful shutdown via `Cmd.Cancel` + `WaitDelay`.**
In `SetProcessGroup`, set:
```go
cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
cmd.WaitDelay = 5 * time.Second
```
SIGINT to the process group lets the harness clean up; Go auto-escalates to SIGKILL after
`WaitDelay`. Small, isolated, applies to every executor at once. The stall detector's kill
path (renamed by #1) must route through this rather than bare SIGKILL.

**2. Pause-now + session resume.**
On pause: cancel the attempt context (SIGINT via Decision 1), capture the session ID from
the partial result, store it in run-state. On resume: if the harness declares
`ResumeSupported()` and a session ID exists, pass `--resume <session-id>`. On retry with
meaningful partial progress (try ran > ~3 min, or > ~3 file changes excluding `.rally/`
and log files), attempt resume rather than fresh start — unless the run was explicitly
skipped. This is the largest piece; validate with a `test-driving-rally` pass after
implementation. Harness support matrix: claude `--resume` (tracked sessionID), antigravity
`--resume` (tracked sessionID), gemini/opencode/codex TBD — gate on `ResumeSupported()` so
unsupported harnesses fall back to a fresh try.

**3. Shortcut label renames.**
`style.ShortcutHint()` labels become "graceful stop" (Ctrl+X) and "quit now" (Ctrl+C). The
text is owned here; the layout/truncation is owned by `cli-polish`. The `cli-polish` tiers
already assume these labels.

**4. Single-runner lane warning (R9).**
At relay start, detect lanes with a single runner entry and warn that one dead harness can
stall the lane. Encourage multi-runner lanes in docs/defaults. The rotation that consumes
this already exists; its effectiveness depends on #1's infra-failure classification marking
entries `Benched`/`Exhausted`.

**5. VERIFY default boundary (R12/R13).**
The default `verify.md` role doc keeps its current stance: VERIFY is reporting-focused,
may apply trivial clearly-correct fixes, and routes substantial fixes/unclear follow-up
to a new head lap rather than doing them inline. (The existing doc already says this; #5
only reconciles the spec language, it does not strip the trivial-fix allowance.) The "mark off `tasks.md`" behavior stays
OpenSpec-specific and is injected per-lap by `prepare-laps` only when a lap has a related
OpenSpec change — not in rally core or the generic role doc. This subsumes R13: no separate
sync mechanism. See `AGENTS.md` boundary rules.

## Risks / Trade-offs

- **SIGINT ignored by a misbehaving harness** → `WaitDelay` guarantees SIGKILL escalation,
  so cancel always terminates.
- **Resuming a stale/invalid session** → gate on `ResumeSupported()` + presence of a
  session ID; on resume failure, fall back to a fresh try rather than erroring the run.
- **"Meaningful progress" heuristic misfires** → conservative thresholds (>3 min, >3 file
  changes excluding `.rally/`/logs); explicit skip always starts fresh.
- **Double-edit of `style.ShortcutHint()` with `cli-polish`** → sequence/co-implement; split
  ownership (labels here, layout there).
- **Resume/run-state overlap with #1's freeze-decay + `--new` reset** → #1 ships first; this
  change builds on the settled run-state shape.
