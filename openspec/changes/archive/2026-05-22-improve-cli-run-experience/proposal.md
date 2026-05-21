## Why

Rally v0.2.0 establishes the core architecture (executor, store, relay runner) but ships with minimal CLI output — plain text, no visual hierarchy, no live feedback while agents run. When running multi-hour relays with dozens of tries, the operator needs to quickly scan what happened, know what's happening now, and trust that the current agent hasn't frozen. The CLI is rally's only interface (no TUI yet), so it needs to be good.

Every harness already writes a per-try transcript log to disk. That log is the most authoritative liveness signal available — when a harness is producing tokens, the log file mtime advances; when it isn't, the log goes still. The monitor should lean on this instead of workspace-file mtime, which produces false signals in both directions (stale when the agent is thinking, noisy when it touches files in tight loops).

Network metrics (connection count, I/O bytes) are useful for diagnosing frozen state but too noisy for constant display. They should surface only when something looks wrong, with smoothing to avoid false alarms.

The operator also needs ways to intervene mid-relay without killing the process — skip a wedged try or stop after the current one. These should be inline keyboard shortcuts during the running relay, not separate CLI commands, since the operator is already watching the terminal.

## Prerequisites

- `consolidate-rally-gry` (session/run/relay architecture) shipped.

## What Changes

### Ctrl+C Guard
- Add double-tap Ctrl+C to exit during try execution — first press shows "Press Ctrl+C again to exit" for 4 seconds, second press triggers graceful stop
- Single Ctrl+C works normally outside of try execution (e.g. at relay resume prompt, between runs)

### Styled CLI Output
- Add styled try headers with separator, agent name, run index, attempt number, start time:
  ```
  ══════════════════════════════════════
    [3/10] claude — started 15:04
  ══════════════════════════════════════
  ```
- Add styled try footers: outcome (pass/fail), runtime, files changed, commit hash
- Add relay summary at completion: total runs, pass/fail counts, total runtime
- Add colour scheme: green for success, red for failure, yellow for retries, dim grey for timestamps
- Use lipgloss for all styling (already retained in v0.2.0)
- Per-run timestamps in local `HH:MM` format (no seconds — cleaner)

### Live Try Monitor
- Show a periodically-updating status line while an agent is executing:
  ```
  ⏱ 5m 34s  │  📁 11 files  │  last activity: 4s ago
  ```
- **Agent runtime**: elapsed wall-clock time since try started
- **File changes**: count of dirty files via `git status --porcelain`
- **Last activity**: time since last log file modification (mtime of the active try's log file — NOT workspace-file mtime)
- Process group tracking: set `Setpgid: true` on agent subprocess, enumerate all PIDs in the group for connection/IO monitoring
- Update interval: every 5 seconds

### Network Warnings with Smoothing
- Network metrics are **not** shown in steady state — only surface when the heuristic flags concern:
  - No TCP connections for 30s → append `No TCP… (30s)` to the status line
  - Connected but no I/O for 30s → append `No network I/O… (30s)`
- Connection count: TCP connections for the agent's process group (Linux `/proc/net/tcp`)
- I/O throughput: cumulative read+write bytes from `/proc/<pid>/io` for the process group (Linux-only)
- Linux-first: full network monitoring on Linux/WSL. macOS gracefully degrades — if rally can't read `/proc/` data at runtime, network warnings are silently disabled (no crash, no error). macOS gets everything else (runtime, file count, log-based last activity)

### Keyboard Shortcuts
- Display available shortcuts below the status line while a try is running:
  ```
  ⏱ 5m 34s  │  📁 11 files  │  last activity: 4s ago
  [Ctrl+S skip]  [Ctrl+P pause]  [Ctrl+X stop]  [Ctrl+C quit]
  ```
- **Ctrl+S** (double-press): skip — cancel current try, assign this lap to the next runner in the rotation (new run, same task). Continues the normal round-robin rotation; does not reset or jump the rotation sequence. Neither skip nor the resulting new run affect lap task state
- **Ctrl+P** (double-press): pause — cancel current try, relay waits for operator to press Enter to resume. On resume, starts a new try within the same run (same runner). Useful when the operator needs to update API keys, fix lap descriptions, or intervene manually
- **Ctrl+X** (double-press): stop — let the current try finish, then end the relay without starting the next run
- All shortcuts require double-press within a confirmation window (same pattern as Ctrl+C guard) — first press shows confirmation prompt, second press executes
- The relay applies the action at the next safe boundary (between try invocations or at the next monitor tick)
- **Note**: Ctrl+R (retry — new try, same runner, consuming retry budget) is deferred to `resilient-execution`, which adds explicit retry budget management and operator override of exhausted budgets

### `rally tail` Subcommand
- `rally tail [--try N]` streams the current (or specified) try's log file with `tail -f` semantics
- Works while the relay is running — no lock contention with the writer
- Resolves the target log path by reading `.rally/tries.jsonl`; defaults to most recent try

## Capabilities

### New Capabilities
- `ctrl-c-guard`: Double-tap Ctrl+C interrupt protection during try execution with 4-second confirmation window
- `styled-output`: Styled CLI output with lipgloss — coloured try headers/footers, separators, relay summaries, timestamps
- `live-monitor`: Real-time status line during try execution — runtime, file changes, last activity from log file mtime. Network warnings (connection count, I/O bytes) shown only when concern heuristic triggers (Linux-only, macOS gracefully degrades)
- `keyboard-interrupts`: Ctrl+S (skip to next runner), Ctrl+P (pause for manual intervention), Ctrl+X (stop relay) keyboard shortcuts with double-press confirmation, displayed below the status line during try execution. Ctrl+R (retry) deferred to `resilient-execution`
- `log-tail`: `rally tail` streams the current try's log file

### Modified Capabilities
- `relay-runner`: Try execution loop updated to integrate Ctrl+C guard, keyboard interrupt handler (skip, pause, stop), and live monitor lifecycle

## Impact

- New packages: `internal/monitor/` (process group tracking, `/proc/` parsing, file change polling, log mtime tracking), `internal/keyboard/` (raw terminal input, shortcut handling, double-press confirmation, pause/resume flow)
- New CLI subcommand: `tail`
- Go dependencies: none new (lipgloss already in v0.2.0)
- Linux-specific: TCP connection counting and I/O byte tracking require `/proc/`. Gracefully disabled at runtime on macOS/other — no build tags needed, just runtime OS checks
- Cross-platform: log-based liveness, file counting, keyboard shortcuts, styled output, rally tail — all work everywhere
- CLI output: all relay runner output goes through styled formatters
- No config schema changes (thresholds hard-coded; tunables deferred to a later release)
