## Context

Rally launches agents as subprocesses through `internal/agent` executors, all of which
call `SetProcessGroup(cmd)` (`internal/agent/exec.go`). This change builds on
`harden-relay-run-lifecycle` (archived 2026-05-29), so the descriptions below reflect
**current** code, not pre-#1 assumptions.

**Cancel path.** Cancellation flows through `exec.CommandContext`, which sends SIGKILL
when the attempt context is cancelled — no SIGINT, no grace period. `SetProcessGroup`
sets only `Setpgid`. So the harness can't flush its session or reap children on cancel.

**Stall-kill path (already graceful).** The stall detector kills via
`reliability.processGroupKiller.Kill` (`internal/reliability/stall.go:298`), which sends
`signalTerminate` (SIGTERM, `freeze_unix.go:14`), polls for up to a 5s drain, then sends
SIGKILL. So Rally already has a graceful kill — but on a *different* signal (SIGTERM)
than the cancel path will use, and CLI agents handle SIGINT (their Ctrl+C interrupt)
more reliably for clean flush.

**Shortcuts (the responsiveness bug).** `internal/keyboard` reads bytes in a goroutine
and emits double-press `Action`s; that part is sound. The problem is in the runner's
action loop (`internal/relay/runner.go:961-982`):
- `ActionSkip` and `ActionPause` call `cancelAttempt()` immediately → responsive today.
- `ActionStop` **and** `ActionQuit` are handled by the *same* branch that only does
  `r.stopFlag.Store(true)` and does **not** cancel the attempt or break the loop. The
  current try runs to completion; the relay stops afterward. For a stalled agent the
  try ends only when the stall detector fires (≤180s) or never — so Ctrl+C/Ctrl+X feel
  dead. This is what operators reported as "stop/quit don't respond when frozen."

**Pause-now + resume (already implemented, partly broken).** `ActionPause` cancels the
attempt, prints "Paused — press Enter to resume", captures `result.SessionID`
(`runner.go:1300`), and `continue`s; the next attempt passes `ResumeSessionID`
(`runner.go:791-822`) when `exec.ResumeSupported()` and a session exists. Every retry
with a tracked session resumes; `FreshRestart` clears it (`runner.go:1157-1160`). But
the executor wiring is inconsistent:

| Harness     | `ResumeSupported()` | Actually passes session?                          |
|-------------|---------------------|---------------------------------------------------|
| claude      | true                | ✅ `--resume <id>` (`claude.go:49-50`)            |
| antigravity | true                | ✅ `--conversation=<id>` (`antigravity.go:80-81`) |
| gemini      | true                | ❌ `Execute` ignores `ResumeSessionID` (`gemini.go:54`) |
| opencode    | true                | ❌ `Execute` ignores `ResumeSessionID` (`opencode.go:60`) |
| codex       | true                | ❌ main `Execute` builds fresh `exec` (`codex.go:173`); `exec resume` is used only by the liveness probe (`codex.go:125-134`) |
| generic     | false               | n/a (correct)                                     |
| fixture     | false               | n/a (correct)                                     |

So three harnesses claim resume support and silently start fresh.

**QA items.** **R9** (runner fallback) is already implemented as Scheduler lane rotation
(`internal/routing/scheduler.go`): when a lane's current entry is `Benched`/`Exhausted`,
the scheduler advances to the next entry. The Prayer-app stall was a single-entry lane
with nothing to rotate to. So R9 reduces to operator guidance, not a feature. **R12/R13**
(VERIFY boundary) is governed by the rally/laps/OpenSpec boundary in `AGENTS.md`.

## Goals / Non-Goals

**Goals:**
- Subprocesses get a SIGINT + grace window before SIGKILL on cancel, on a signal unified
  with the stall-kill path.
- Ctrl+C ("quit now") cancels the running try immediately and aborts the relay; Ctrl+X
  ("graceful stop") stops after the current try; the action loop stays responsive during
  the cancel drain.
- Every harness that reports `ResumeSupported()` actually resumes (or honestly reports
  false). No regression to the existing pause-now / resume-on-retry behavior.
- Shortcut labels state whether they act now or after the current try.
- Operators are warned when a lane has no fallback runner.
- The default VERIFY role is reporting-focused and OpenSpec-agnostic.

**Non-Goals:**
- Re-building pause-now or resume-on-retry (already present; this change fixes wiring and
  adds regression coverage).
- Adding a "meaningful progress" resume heuristic — current behavior (resume on any
  retry with a tracked session; `FreshRestart` clears it) is retained.
- Building a new fallback/rotation mechanism (it already exists; R9 is docs + the
  already-landed classification).
- A separate OpenSpec↔laps sync mechanism (R13 — subsumed by the `prepare-laps`
  coupling).
- Adding resume to harnesses that genuinely lack a resume CLI (leave
  `ResumeSupported()=false`).

## Decisions

**1. Graceful shutdown via `Cmd.Cancel` + `WaitDelay`, unified on SIGINT.**
In `SetProcessGroup`, set:
```go
cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT) }
cmd.WaitDelay = 5 * time.Second
```
SIGINT to the process group lets the harness clean up; Go auto-escalates to SIGKILL
after `WaitDelay`. Small, isolated, applies to every executor at once. **Also** change
the stall killer's `signalTerminate` mapping from SIGTERM to SIGINT
(`internal/reliability/freeze_unix.go`) so both kill paths use the same signal. (Decision
accepted by the user: unify on SIGINT.) Keep the 5s drain consistent between the two
paths.

**2. Responsive stop/quit.**
Split the shared `ActionStop`/`ActionQuit` branch:
- `ActionQuit` → set `stopFlag`, `cancelAttempt()`, drain `tryCh` (like pause/skip),
  `break actionLoop`. The graceful shutdown kills the try; the outer loop sees `stopFlag`
  and aborts the relay. Immediate.
- `ActionStop` → keep current behavior: set `stopFlag`, let the current try finish, stop
  after. (Bounded by the stall detector for a frozen agent.)
- During the cancel drain the runner blocks on `<-tryCh`; keep `actionCh` in the select
  so a second `ActionQuit` within the ≤5s window can force an immediate SIGKILL
  (cancel a second time / signal the process group directly), and so the monitor can show
  a "stopping…" state. This prevents the "appears frozen for 5s" regression that the
  graceful shutdown would otherwise introduce.

**3. Honest resume wiring.**
Audit each executor; for gemini/opencode/codex, either pass the real resume flag when
`ResumeSessionID != ""` (after confirming each CLI's resume invocation against its
`--help`) or change `ResumeSupported()` to false until it is wired. Do not change the
runner-side resume policy: it already resumes on any retry with a tracked session and
clears on `FreshRestart`. Add a contract test asserting `ResumeSupported()==true` implies
the resume flag appears in the built args when a session is set.

**4. Shortcut label renames.**
`style.shortcutHintTiers` labels become "graceful stop" (Ctrl+X) and "quit now" (Ctrl+C)
across all width tiers. The width-aware tier layout already shipped in `cli-polish`; only
the label text changes here. Keep the compact tiers short (e.g. `^X stop`→`^X stop`* may
need to stay terse at narrow widths — see Risks).

**5. Single-runner lane warning (R9).**
At relay start, detect lanes with a single runner entry and warn that one dead harness
can stall the lane. Encourage multi-runner lanes in docs/defaults. The rotation that
consumes this already exists and already keys off `Benched`/`Exhausted`.

**6. VERIFY default boundary (R12/R13).**
The default `verify.md` role doc keeps its current stance: VERIFY is reporting-focused,
may apply trivial clearly-correct fixes, and routes substantial fixes/unclear follow-up
to a new head lap. (The existing doc already says this; this change only reconciles the
spec language — it does not strip the trivial-fix allowance.) The "mark off `tasks.md`"
behavior stays OpenSpec-specific and is injected per-lap by `prepare-laps` only when a
lap has a related OpenSpec change. This subsumes R13. See `AGENTS.md` boundary rules.

## Risks / Trade-offs

- **SIGINT ignored by a misbehaving harness** → `WaitDelay`/drain guarantees SIGKILL
  escalation, so cancel always terminates within ~5s.
- **Switching the stall killer from SIGTERM to SIGINT** → re-validate the existing
  `stall_test.go` signal-sequence assertions (they assert `signalTerminate` then
  `signalKill`; the abstraction stays, only the OS signal mapping changes), and confirm
  agents still exit on SIGINT under stall.
- **Graceful cancel adds up to 5s latency vs today's instant SIGKILL** → mitigated by
  keeping the action loop responsive so a second Ctrl+C escalates immediately, and by the
  monitor showing "stopping…".
- **Resuming a stale/invalid session** → on resume failure the run already degrades to a
  fresh try rather than erroring; preserve that when wiring gemini/opencode/codex.
- **Narrow-width shortcut tiers** → "graceful stop"/"quit now" are longer than
  "stop"/"quit"; verify the narrowest tiers still fit or keep terse abbreviations
  (`^X stop` / `^C quit`) at the smallest widths while the wide tiers carry the full
  labels. Coordinate with `cli-polish`'s tier widths.
