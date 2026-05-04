## 1. Styled CLI output

- [x] 1.1 Add lipgloss-based try header renderer: separator line, agent name, run index, attempt number, start time in `HH:MM`
- [x] 1.2 Add try footer renderer: outcome (pass/fail), runtime, files changed, commit hash
- [x] 1.3 Add relay summary renderer: total runs, pass/fail counts, total runtime
- [x] 1.4 Add colour scheme constants: green (success), red (failure), yellow (retries), dim grey (timestamps)
- [x] 1.5 Wire formatters into the relay runner's print paths
- [x] 1.6 Snapshot tests for header, footer, and summary rendering

## 2. Ctrl+C guard

- [x] 2.1 Add signal handler with double-press detection: first press shows confirmation message, second press within 4s triggers graceful stop
- [x] 2.2 Single Ctrl+C works normally outside of try execution (resume prompt, between runs)
- [x] 2.3 Unit tests: single press shows message and resets; double press triggers stop; timeout resets state

## 3. Live monitor

- [x] 3.1 Add `internal/monitor/` package with status line renderer
- [x] 3.2 Implement agent runtime tracking (wall-clock elapsed since try started)
- [x] 3.3 Implement dirty-file count via `git status --porcelain`
- [x] 3.4 Implement last-activity tracking from active try's log file mtime
- [x] 3.5 Run the monitor on a 5-second tick during try execution; clear the status line on try completion
- [x] 3.6 Process group tracking: set `Setpgid: true` on agent subprocess, enumerate PIDs in the group
- [x] 3.7 Unit tests: status line formatting, mtime-based activity detection, file count

## 4. Network warnings

- [x] 4.1 Add `/proc/net/tcp` parser for TCP connection count per process group (Linux-only)
- [x] 4.2 Add `/proc/<pid>/io` parser for cumulative I/O bytes per process group (Linux-only)
- [x] 4.3 Runtime `GOOS` check: if not Linux or `/proc/` paths unreadable, silently disable network monitoring
- [x] 4.4 Implement 30s smoothing: track last time connections were seen and last time I/O advanced
- [x] 4.5 Append warning text to status line when triggered: `No TCP… (30s)` or `No network I/O… (30s)`
- [x] 4.6 Unit tests: threshold logic, smoothing resets on activity, non-Linux path produces no warnings

## 5. Keyboard shortcuts

- [x] 5.1 Add `internal/keyboard/` package: put terminal in raw mode during try execution, restore on exit
- [x] 5.2 Capture Ctrl+S, Ctrl+P, Ctrl+X, Ctrl+C in raw mode; route to handlers
- [x] 5.3 Implement double-press confirmation for all four shortcuts (shared pattern with Ctrl+C guard)
- [x] 5.4 Ctrl+S handler: signal skip to relay runner — cancel current try, assign same microbead to next runner in round-robin rotation (new run). Does not affect microbead task state. Continues normal rotation sequence
- [x] 5.5 Ctrl+P handler: signal pause to relay runner — cancel current try, display "Paused — press Enter to resume", wait for Enter. On resume, start new try within same run (same runner)
- [x] 5.6 Ctrl+X handler: signal stop to relay runner — let current try finish, exit without starting next run
- [x] 5.7 Render shortcut hints below the status line: `[Ctrl+S skip]  [Ctrl+P pause]  [Ctrl+X stop]  [Ctrl+C quit]`
- [x] 5.8 Unit tests: double-press detection, timeout reset, skip/pause/stop flag propagation, pause-resume flow

## 6. `rally tail` subcommand

- [x] 6.1 Add `rally tail [--try N]` cobra subcommand
- [x] 6.2 Resolve target log path from `.rally/tries.jsonl`; default to most recent try
- [x] 6.3 Implement tail-follow semantics in pure Go (read existing content, then poll/fsnotify for appends)
- [x] 6.4 Handle missing or empty `.rally/tries.jsonl` with clear error message
- [x] 6.5 Handle `--try N` out of range with error naming valid range
- [x] 6.6 Unit tests: latest selection, `--try N` valid/invalid, empty tries file

## 7. Integration and verification

- [x] 7.1 End-to-end: relay running → status line updates → Ctrl+S skips to next runner → Ctrl+P pauses and resumes → Ctrl+X stops
- [x] 7.2 Verify styled output renders correctly in common terminals
- [x] 7.3 Verify network warnings trigger on Linux when agent stalls; verify no warnings on macOS
- [x] 7.4 Verify `rally tail` streams actively during a relay and works for reviewing past tries
- [x] 7.5 Verify double-press confirmation pattern works consistently for all four shortcuts
