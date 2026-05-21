## Context

Rally v0.2.0 has a working relay runner but minimal operator feedback — no styled output, no live monitoring, no way to intervene mid-relay except killing the process. This change adds the full CLI experience layer: styled headers/footers, a live status line, keyboard-based interrupt controls, and a log tail command.

The live monitor needs a reliable liveness signal. Workspace-file mtime is a poor choice — agents frequently spend minutes thinking before writing a file, and sometimes touch files in tight loops without meaningful progress. Every harness already writes a per-try transcript log to disk; that log's mtime is a more authoritative signal. Network metrics (TCP connections, I/O bytes) are useful for diagnosing frozen state but too noisy for constant display, and Linux-only.

## Goals / Non-Goals

**Goals:**
- Styled, scannable CLI output for relay runs with clear visual hierarchy
- A live status line that tells the operator "it's alive" during long-running tries
- A liveness signal (log file mtime) that works on all platforms and reflects actual agent progress
- Network-based stall detection that surfaces only when something looks wrong, with smoothing
- Keyboard shortcuts for skip/pause/stop so operators can steer the relay without killing it
- A way to stream the live agent transcript (`rally tail`)
- Linux-first for network features; macOS graceful degradation (no crash, just loses network warnings)

**Non-Goals:**
- Token estimation or budget tracking (can't do reliably enough to be useful)
- Mid-try mutation (changing the agent, switching models, sending an inline prompt)
- Full Windows support (macOS shouldn't crash; Windows not a priority)
- Standalone CLI commands for skip/stop (keyboard shortcuts are the interface)
- Configurable thresholds (hard-coded for now; tunables deferred)

## Decisions

### Log-file mtime is the canonical liveness signal
**Chosen**: The active try's log file mtime is the primary "is it alive" indicator. Workspace-file mtime is not used.

**Alternative considered**: Workspace-file mtime (from git status or inotify).

**Why**: Workspace-file mtime is misleading — stale when agents think, noisy when agents touch files in loops. The harness log file mtime directly tracks whether the agent is producing output, which is what the operator actually wants to know.

### Network metrics are warnings-only with smoothing
**Chosen**: TCP connection count and I/O bytes are never shown in steady state. They surface as warning text appended to the status line only when the smoothed heuristic triggers:
- No TCP connections for 30s → `No TCP… (30s)`
- Connected but no I/O for 30s → `No network I/O… (30s)`

**Alternative considered**: Always show connection count and I/O in the status line.

**Why**: Constant display is noise. On Linux the counts fluctuate; on macOS they're always zero. Showing them only when something looks wrong makes their presence informative — "a warning just appeared" is a signal; "the same four numbers are still there" is not. Smoothing (30s threshold) prevents transient dips from triggering false alarms.

### Skip, pause, and stop via keyboard shortcuts, not CLI commands
**Chosen**: Ctrl+S (skip to next runner), Ctrl+P (pause for manual intervention), Ctrl+X (stop relay) as keyboard shortcuts during relay execution, all requiring double-press confirmation. Available shortcuts are displayed below the status line.

**Alternative considered**: Standalone `rally skip` / `rally stop` CLI commands communicating via PID file + Unix-domain socket.

**Why**: The operator is already watching the terminal. Keyboard shortcuts are immediate — no need to open another terminal, find the right workspace, or deal with stale PID files. Double-press confirmation prevents accidental triggers (same UX pattern as the existing Ctrl+C guard). The UDS approach adds complexity (PID file lifecycle, stale socket cleanup, cross-platform IPC) for a use case that's inherently interactive and terminal-bound.

### Skip means "next runner", not "next task"
**Chosen**: Ctrl+S cancels the current try and assigns the same lap to the next runner in the round-robin rotation (a new run). It does not advance to the next lap or modify lap task state. The rotation continues normally — skip just advances the runner pointer the same way a completed run would.

**Alternative considered**: Skip advances to the next iteration/lap entirely.

**Why**: Skip addresses a specific class of problem — "this runner can't handle this task right now" (e.g. API timing out). The task itself isn't invalid; it just needs a different runner. Abandoning the task would leave laps in limbo. The round-robin rotation keeps moving forward naturally; skip doesn't reset or jump the sequence.

### Pause for manual operator intervention
**Chosen**: Ctrl+P cancels the current try and puts the relay in a waiting state. The operator presses Enter to resume, which starts a new try within the same run (same runner).

**Alternative considered**: No pause — operators use Ctrl+C to quit and manually restart.

**Why**: Common scenarios (rotating API keys, fixing a lap description that's sending agents down rabbit holes, checking external state) require the relay to pause but not terminate. Ctrl+C loses relay state and requires the operator to re-invoke. Pause keeps the relay alive and resumes cleanly. Resuming with the same runner is correct — the operator paused to fix the environment, not because the runner was wrong (that's what skip is for).

### Retry deferred to resilient-execution
**Chosen**: Ctrl+R (retry — new try, same runner, consuming retry budget but overridable when budget exhausted) is not included in this change. It ships with `resilient-execution`, which adds explicit retry budget management.

**Why**: Before resilient-execution, automatic retries exist but the operator has no reason to manually trigger one — pause (Ctrl+P) covers the "fix something and try again" case. Ctrl+R becomes valuable when resilient-execution adds configurable retry budgets and the operator needs to override an exhausted budget ("I know this runner should work, try one more time").

### Double-press confirmation for all interrupt shortcuts
**Chosen**: Ctrl+C, Ctrl+S, Ctrl+P, and Ctrl+X all use the same double-press pattern — first press shows a confirmation message, second press within a time window executes the action.

**Alternative considered**: Single-press for skip/pause/stop since they're less destructive than quit.

**Why**: Consistency. The operator builds one mental model for all interrupt actions. Skip can still discard meaningful agent work, and stop can end a relay that the operator intended to finish. The small friction of a double-press is worth avoiding accidental interrupts.

### macOS: graceful runtime degradation for network monitoring
**Chosen**: Network monitoring (TCP connection count, I/O bytes) is silently disabled on macOS at runtime. No build tags — runtime `GOOS` check + attempt to read `/proc/` paths. If the paths don't exist, network warnings never trigger. Everything else (log liveness, file count, keyboard shortcuts, styled output, rally tail) works on macOS.

**Alternative considered**: Build-tag separation with platform-specific implementations.

**Why**: macOS isn't a priority. Build tags add maintenance burden for a second-class platform. A runtime check is simpler: if `/proc/` isn't there, skip it. This also automatically handles WSL (which has `/proc/`) and any future Linux-like platform. If macOS support becomes important later, a platform-specific implementation can be added behind the same interface.

### `rally tail` as a standalone subcommand
**Chosen**: `rally tail [--try N]` reads `.rally/tries.jsonl` to find the log path, then streams it with tail-follow semantics.

**Alternative considered**: Embedding tail into the live monitor (e.g., a keystroke to toggle log streaming inline).

**Why**: Tail is a different modality — the operator wants a full-screen scrolling log, not a single status line. Running it in a separate terminal alongside the relay is the natural UX. It also works after the relay finishes (reviewing past try logs), which an inline toggle can't do.

### Hard-coded thresholds
**Chosen**: The 30s network warning threshold, the 5s monitor tick interval, and the 4s Ctrl+C confirmation window are hard-coded constants.

**Alternative considered**: Configuration file or CLI flags.

**Why**: Not enough usage data to know what the right defaults are. Shipping config knobs before we know which knobs matter encourages tinkering based on superstition. Lock them now, collect feedback, expose tunables in a later release if warranted.

## Risks / Trade-offs

- **Log mtime fails when the harness buffers writes** → Each executor adapter is responsible for ensuring the log is line-buffered or flushed at reasonable cadence. If a harness can't be coerced, the monitor displays `—` for last activity rather than producing false stalls.
- **Keyboard shortcuts conflict with terminal emulator bindings** → Ctrl+S is traditionally XOFF (terminal pause), Ctrl+P may be bound in some terminals. Rally puts the terminal in raw mode during try execution, so it captures the keypress directly. Document that terminal flow control should be disabled (`stty -ixon`) if the user's terminal fights it.
- **Network warnings never trigger on macOS** → Accepted trade-off. macOS operators still see log-based liveness and can use `rally tail` for detailed inspection. If macOS gains importance, `/proc/`-free network monitoring (via `lsof` or `netstat` parsing) can be added later.
- **Double-press can feel slow when the operator knows what they want** → The confirmation window is short (4s). The alternative (accidental skip mid-relay) is worse.
- **`git status --porcelain` for file count could be slow in large repos** → The monitor runs it every 5 seconds. If this proves too slow, switch to watching `.git/index` mtime as a proxy.

## Migration Plan

1. **Styled output**: Add lipgloss-based formatters for try headers, footers, and relay summary. Wire them into the relay runner's existing print paths.
2. **Ctrl+C guard**: Add signal handler with double-press detection and confirmation timeout. Integrate into the try execution loop.
3. **Live monitor**: Build the status line renderer with runtime, file count, and log-mtime-based last activity. Run it on a 5-second tick during try execution.
4. **Network warnings**: Add `/proc/` readers for TCP connections and process I/O (Linux-only, runtime-gated). Wire into the monitor with 30s smoothing threshold. Append warning text to the status line only when triggered.
5. **Keyboard shortcuts**: Put the terminal in raw mode during try execution. Capture Ctrl+S, Ctrl+P, Ctrl+X, Ctrl+C. All four use double-press confirmation. Display available shortcuts below the status line. Ctrl+P handler enters a wait loop for Enter keypress before resuming.
6. **`rally tail`**: Add the subcommand. Read `.rally/tries.jsonl` to resolve log paths. Implement follow semantics in pure Go (poll or fsnotify).
7. **Cross-platform**: Runtime `GOOS` checks gate `/proc/` access. macOS gets everything except network warnings.
