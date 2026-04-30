## Context

v0.2.x ships with a live monitor (`improve-cli-run-experience` change) that surfaces wall-clock runtime, dirty-file count, TCP connection count, `/proc/<pid>/io` byte counters, and a workspace-file mtime "last activity" indicator. After running real relays, two findings emerged:

1. **Workspace mtime is a poor liveness signal.** Agents frequently spend minutes thinking before writing a file; mtime stays stale even though the agent is plainly making progress. Conversely, agents sometimes touch files in tight loops without producing forward motion.
2. **Connection/IO metrics are platform-fragile and noisy.** They rely on Linux `/proc/`, are silently zero on macOS, and produce false alarms when an agent legitimately works locally for a while. Operators learned to ignore them.

Meanwhile, every harness already writes a per-try transcript log to disk. That log is the most authoritative liveness signal available — when a harness is producing tokens, the log file mtime advances; when it isn't, the log goes still. The data is already there; the monitor just isn't using it.

The other gap is interruption. Today the only way to influence a running relay is `Ctrl+C`, which (even with v0.2.x's double-tap guard) tears down the whole relay. Operators want to skip the current try ("this run is wedged, move on") or stop after the current try ("done for today, finish what's running") without nuking in-flight state.

## Goals / Non-Goals

**Goals:**
- A primary liveness signal that works equally well on Linux and macOS, and that reflects actual agent progress rather than incidental file activity
- A way to stream the live agent transcript without `tail -f` gymnastics or knowing the data-dir layout
- An out-of-band interrupt mechanism that lets the operator influence iteration boundaries without killing the relay process
- A best-effort token-budget estimate so operators can spot context-pressure issues before the agent crashes
- Cross-platform parity for the new features (log-mtime, tail, interrupts) — Linux-only for the demoted metrics is acceptable

**Non-Goals:**
- Replacing or extending v0.2.x's Ctrl+C guard — that's a separate seam and works fine
- Per-harness deep introspection (token counts from API responses, structured event streams) — best-effort character-based estimation only
- Mid-try mutation (changing the agent, switching models, sending an inline prompt) — interrupts are between-try only
- Windows support for `rally skip` / `rally stop` — UDS isn't a clean fit; deferred

## Decisions

### Log-file mtime is the canonical liveness signal
**Chosen**: The active try's log file mtime is the primary "is it alive" indicator. Workspace-file mtime drops out of the live monitor entirely.

**Alternative considered**: Keep workspace mtime as primary, layer log mtime as secondary.

**Why**: Layering means the monitor has two "sources of truth" with different semantics. Operators learned not to trust workspace mtime; preserving it as a layered fallback would just delay the discovery that log mtime is what they actually care about. A single, well-defined primary signal beats a hierarchy of partial signals.

### Connection and I/O metrics demoted to concerns-only
**Chosen**: `🔗 N conns` and `📡 N MB I/O` only render when the heuristic flags concern (e.g. log file hasn't advanced for >30s AND zero connections). Steady-state runs show only the agent runtime, file count, last log activity, and token estimate.

**Alternative considered**: Keep them in the steady-state line, just deprioritised.

**Why**: They're noisy on Linux and zero on macOS — both modes are bad UX. Showing them only when something looks off makes their presence informative ("the line just grew a connection count, that means the freeze detector triggered") rather than ambient noise. Operators on macOS see the same display as Linux operators in the happy path.

### Interrupt IPC: PID file + Unix-domain socket
**Chosen**: Relay process writes a PID file and listens on a UDS in the workspace's rally data dir. `rally skip` / `rally stop` connect to the UDS, send a short command, exit. Relay applies the change at the next safe boundary (between try invocations or at the next monitor tick).

**Alternative considered (a)**: POSIX signals (SIGUSR1 for skip, SIGUSR2 for stop).
**Alternative considered (b)**: A control file the relay polls (e.g. `.rally/interrupts/skip`).

**Why**: Signals don't carry payload, which makes future extension (e.g. `rally skip --reason "..."`) painful. Polling a control file is racy and adds disk traffic to the steady state. UDS is a clean RPC seam, naturally scoped to the workspace, and gives us an obvious extension path. PID file lets the CLI subcommands locate the active relay deterministically and produce a useful error if no relay is running.

### `rally skip` / `rally stop` are non-blocking
**Chosen**: Both commands send the request, print the action taken, exit immediately. They do not wait for the relay's acknowledgement or for the actual skip/stop to take effect.

**Alternative considered**: Block until the relay confirms the request applied.

**Why**: The operator's mental model is "tell rally what to do next, then go on with my day." A blocking command means the operator has to keep a terminal open watching for the ack. The relay's monitor display already shows pending interrupts at the next tick, so the feedback loop exists without a blocking RPC.

### Cross-platform: skip/stop print not-supported on Windows
**Chosen**: PID-file + UDS works on Linux/macOS. On Windows, the subcommands print a not-supported message and exit non-zero.

**Alternative considered**: Implement a Windows-compatible IPC (named pipes) up front.

**Why**: Rally's user base is currently Linux/macOS-only based on installation telemetry and harness compatibility. Windows IPC parity is a finite engineering cost we don't owe ourselves yet. The not-supported message documents the limitation cleanly; we can revisit when a Windows user actually files an issue.

### Token estimator: per-harness character divisor, no API calls
**Chosen**: Each executor adapter declares a `chars_per_token` divisor (e.g. `3.5` for English-leaning prompts). The monitor reads the harness's log file size and divides. Surfaces as `~12.4k tok` in the live line. If the harness can't expose enough text to estimate (encrypted streams, binary protocols), the monitor displays `—`.

**Alternative considered (a)**: Hit each provider's tokenizer endpoint or library.
**Alternative considered (b)**: Hard-code one global divisor.

**Why**: API calls cost money and introduce a network dependency on the live-monitor path. Per-provider tokenizer libraries are heavy and version-fragile across harnesses. A character divisor is wrong by 10–20% but free, deterministic, and good enough to spot "this run is going to blow context in the next minute" — which is the operator's actual question. Per-harness divisors let us tune the estimate where harnesses have known tokenisation skews.

### Fixed thresholds in v0.3.0; tunables in v0.7.0
**Chosen**: The 30s freeze threshold, the 5s monitor tick, and the per-harness char divisors are hard-coded constants in v0.3.0. v0.7.0 (`resilient-execution`) introduces `[reliability].freeze_threshold_secs` and surfaces the divisors as part of the same config table.

**Alternative considered**: Make them configurable now.

**Why**: We don't yet have enough usage data to know what the right defaults are. Shipping config knobs before we know which knobs matter encourages users to twiddle them based on superstition; locking them in v0.3.0 lets us collect real-world feedback before exposing them. v0.7.0 owns the freeze-detection feature, so the config surface naturally lands there.

## Risks / Trade-offs

- **Log mtime fails when the harness buffers writes** → Mitigation: each executor adapter is responsible for ensuring the log is line-buffered or flushed at reasonable cadence. Where the harness can't be coerced (some Codex modes), the adapter declares `liveness_supported = false` and the monitor displays `—` rather than producing false stalls.
- **UDS file in `.rally/` could conflict with stale entries from a crashed relay** → Mitigation: relay startup checks for an existing PID file; if the PID is dead or doesn't belong to a `rally` process, the stale file is removed and a fresh socket is bound. If the PID is live, startup errors with a clear "another relay is already running in this workspace" message.
- **Concerns-only display can hide a real freeze if the heuristic doesn't trigger** → Mitigation: the heuristic combines log silence AND zero connections, both of which are monotonic indicators. False negatives are possible but cost-bounded — the operator still sees `last activity: 92s ago` ticking up. v0.7.0's freeze detection adds an active killswitch on top.
- **Token estimate accuracy is poor for non-English content** → Mitigation: the value is prefixed `~` and operators are told it's best-effort. The display dims to `—` if the harness can't expose log text.
- **Interrupt commands silently fail if the operator runs them in a different workspace** → Mitigation: PID file lookup is workspace-scoped (we look in the cwd's `.rally/`). If no relay is running here, the command exits non-zero with "no active rally relay in this workspace" — the operator gets feedback rather than a silent no-op.

## Migration Plan

1. **Live-monitor refactor**: keep the existing monitor wiring intact (file count, runtime, last-activity slot) but switch the last-activity source from workspace mtime to log file mtime. Network/IO blocks become conditional renderers gated on the concern heuristic.
2. **Token estimator**: each executor adapter gains a `CharsPerToken() float64` method (default `3.5` if unimplemented). The monitor gains a `tokenEstimate(logSize) string` helper.
3. **Tail subcommand**: `rally tail [--try N]` reads `.rally/tries.jsonl` to locate the latest (or specified) try's log path, then `tail -f`s it. Standalone — no IPC needed since the log is on disk.
4. **Interrupt IPC**: relay startup writes `.rally/relay.pid` and binds `.rally/relay.sock`. Skip/stop subcommands locate the socket relative to cwd, dial, send a one-byte command, exit. Relay loop checks the request flag at the top of each iteration and at every monitor tick.
5. **Cross-platform gates**: skip/stop subcommands check `runtime.GOOS` and print "not supported on Windows" if applicable. The monitor's existing Linux-only `/proc/` paths are unchanged (already gated in v0.2.x).

Rollback: revert the v0.3.0 release. Workspace mtime returns to being the primary liveness signal; tail/skip/stop subcommands disappear. No persistent state on disk needs to be cleaned up — the PID file and socket are workspace-local and ephemeral.

## Open Questions

- The exact concern-trigger heuristic for showing connection/IO metrics is sketched ("zero log writes for >30s AND zero connections") but the constants may need tuning once we see real data. Initial implementation uses the sketched values; tuning is a non-breaking follow-up.
- `rally skip` semantics when invoked during a long retry loop: does it skip the current try or skip the whole retry chain for this iteration? Initial answer: skip the current try, retry budget continues. Revisit if operators want a `--skip-iteration` variant.
