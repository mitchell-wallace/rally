## Why

v0.2.x ships with live monitoring (runtime, file changes, connections, I/O bytes — see `improve-cli-run-experience`) but the operator is still missing two things: a quick way to see what the agent is *actually saying* when something looks stalled, and a way to intervene mid-relay without killing the whole process. Network-connection counts also turned out to be a noisy primary signal — agents frequently idle on local I/O while still making progress, and Linux-only `/proc/` features are useless on macOS.

The full agent transcript is already written to disk per try. Lean on that as the canonical liveness signal, demote the network metrics to a "concerns only" surface, and add explicit interrupt commands so the operator can steer the relay between iterations without `Ctrl+C` collateral.

## Prerequisites

- `improve-cli-run-experience` (live monitor, Ctrl+C guard) shipped in a prior v0.2.x release.

## What Changes

### Log-driven liveness signal
- Treat the active try's log file mtime as the canonical "is it alive" signal — replaces workspace-file mtime as the primary `last activity` source
- Demote `🔗 N conns` and `📡 N MB I/O` to a concern-only display: only render when the heuristic flags concern (e.g. zero log writes for >30s AND zero connections)
- Add `rally tail [--try N]` — streams the current (or specified) try's log file with `tail -f` semantics; works while the relay is running

### Token approximation
- Add a per-harness character-based token estimator surfaced in the live monitor (`~12.4k tok` style)
- Best-effort only — no API calls, no tokenizer dependency; harness-specific divisor (e.g. ~3.5 chars/token for English-leaning prompts) defined per executor
- Silently displays `—` if the harness's log format doesn't expose enough to estimate

### Interrupt commands
- `rally skip` — graceful stop of the current try, advance to next iteration
- `rally stop` — graceful stop after current try completes, no further iterations
- IPC via PID file + Unix-domain socket in the workspace's rally data dir; signal handler in the relay process applies the change at the next safe boundary (between try invocations or at the next monitor tick)
- Both commands print the action taken, exit immediately, and don't block on the relay's response

## Capabilities

### New Capabilities
- `log-tail`: `rally tail` streams the current try's log file
- `token-estimator`: Character-based token estimate displayed in the live monitor, per-harness divisor
- `interrupt-control`: `rally skip` / `rally stop` external commands signalling a running relay via PID-file + UDS

### Modified Capabilities
- `live-monitor`: Activity signal sourced from log file mtime; network/connection/IO metrics demoted to concern-only display

## Impact

- New package: `internal/control/` (PID file, UDS, signal handlers)
- New CLI subcommands: `tail`, `skip`, `stop`
- Cross-platform: PID-file + UDS works on Linux/macOS; Windows deferred (skip/stop print a not-supported message)
- Live monitor display order changes — log activity becomes the primary "alive" indicator; existing concerns-based display preserved when triggered
- No config schema changes (thresholds hard-coded for v0.3.0; tunables added in v0.7.0)
