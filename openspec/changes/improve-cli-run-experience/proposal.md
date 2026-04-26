## Why

Rally v0.2.0 establishes the core architecture (executor, store, relay runner) but ships with minimal CLI output — plain text, no visual hierarchy, no live feedback while agents run. When running multi-hour relays with dozens of tries, the operator needs to quickly scan what happened, know what's happening now, and trust that the current agent hasn't frozen. The CLI is rally's only interface (no TUI yet), so it needs to be good.

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
- Add styled try footers: outcome (✓/✗), runtime, files changed, commit hash
- Add relay summary at completion: total runs, pass/fail counts, total runtime
- Add colour scheme: green for success, red for failure, yellow for retries, dim grey for timestamps
- Use lipgloss for all styling (already retained in v0.2.0)
- Per-run timestamps in local `HH:MM` format (no seconds — cleaner)

### Live Try Monitor
- Show a periodically-updating status line while an agent is executing:
  ```
  ⏱ 5m 34s  │  📁 11 files  │  🔗 3 conns  │  📡 12.4 MB I/O  │  last activity: 34s ago
  ```
- **Agent runtime**: elapsed wall-clock time since try started
- **File changes**: count of dirty files via `git status --porcelain`
- **Active connections**: count of TCP connections for the agent's process group (Linux `/proc/net/tcp` — gated behind runtime OS check, skipped on non-Linux)
- **I/O throughput**: cumulative read+write bytes from `/proc/<pid>/io` for the process group (Linux-only, skipped on non-Linux)
- **Last activity**: time since last workspace file modification (mtime scan)
- Process group tracking: set `Setpgid: true` on agent subprocess, enumerate all PIDs in the group for connection/IO monitoring
- Update interval: every 5 seconds (configurable?)

## Capabilities

### New Capabilities
- `ctrl-c-guard`: Double-tap Ctrl+C interrupt protection during try execution with 4-second confirmation window
- `styled-output`: Styled CLI output with lipgloss — coloured try headers/footers, separators, relay summaries, timestamps
- `live-monitor`: Real-time process monitoring during try execution — runtime, file changes, network connections, I/O bytes, last activity (Linux `/proc/` features gated behind OS check)

### Modified Capabilities
- `relay-runner`: Try execution loop updated to integrate Ctrl+C guard and live monitor lifecycle

## Impact

- New package: `internal/monitor/` (process group tracking, `/proc/` parsing, file change polling)
- Go dependencies: none new (lipgloss already in v0.2.0)
- Linux-specific features: active connections and I/O throughput require `/proc/` — gracefully disabled on macOS/other. File changes and last activity work everywhere.
- CLI output: all relay runner output goes through styled formatters
- Tests: Ctrl+C guard behaviour verified via signal simulation tests. Monitor metrics verified via mock `/proc/` data.
