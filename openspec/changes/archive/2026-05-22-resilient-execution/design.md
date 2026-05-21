## Context

Long relays accumulate transient failures: opencode dropping mid-stream on a Kimi 400, gemini-cli exiting 1, claude interrupting on a rate-limit error, GLM stalling mid-session when its quota tips over. Today rally's response is uniform: "retry up to 3 times from scratch, then mark failed and advance." That has four problems:

1. **Wastes context every retry.** Harnesses that *can* resume an interrupted session are forced to re-prime, paying for tokens we already paid for in the failed try.
2. **Treats freezes and exit-1s identically.** A frozen session burns wall-clock time before its retry triggers; an exit-1 retries immediately. Both go through the same path.
3. **No notion of intra-session rate-limit detection.** When a provider partially throttles a session (continuing to respond but at degraded quality or with retry-after hints), rally doesn't notice.
4. **Provider rotation always means full harness teardown.** Even when the next entry uses the same harness with a different model (e.g. opencode swapping GLM → Kimi), rally tears down and respawns the whole process — slow and unnecessary.

With v0.3.0 monitoring signals (log-mtime, conn count, IO bytes), v0.5.0 provider shortcuts, and v0.6.0's quota-scheduler in hand, we can do better: resume where supported, rotate within a harness in-place, detect freezes from monitoring data, classify errors to choose the right strategy per failure type, and (opt-in) probe sessions actively when the freeze signal is ambiguous.

## Goals / Non-Goals

**Goals:**
- Replace the uniform "retry from scratch" with per-failure-type strategies driven by an error-classification table
- Resume sessions on retry where the harness supports it (declared per-adapter)
- Detect frozen tries from monitoring data and graceful-kill them as retry-eligible failures
- Cheap in-place provider rotation for same-harness next-entry advances
- Operator-tunable thresholds for the new behaviours via `[reliability]` config table

**Non-Goals:**
- Cross-harness "smart rotation" (semantic understanding of which alternative is best for the work) — that's a much larger effort and not v0.7.0
- Replacing v0.6.0's scheduler — v0.7.0 layers on top by emitting `OnAgentFailed`/`OnAgentRecovered` from the new sources (freeze detector, error classifier) into the existing scheduler hooks
- Auto-tuning thresholds — every knob is operator-set with sensible defaults; learning is out of scope
- Probing every harness — liveness probe is opt-in and gated by per-adapter declared support

## Decisions

### Resume on retry where supported, fresh start otherwise
**Chosen**: Each executor adapter declares `ResumeSupported() bool` and (where applicable) returns a session-id string from `Execute`. On retry, the relay-runner passes the harness-specific resume flag if support is declared and a session-id was captured. If support is absent or the session-id is missing, the retry uses a fresh start.

**Alternative considered**: Always resume; let the harness fail if it doesn't actually support it.

**Why**: Fail-on-unsupported-resume produces obscure error messages from the harness (some happily restart fresh, others error opaquely). An explicit per-adapter declaration makes the behaviour predictable and lets us add resume to additional harnesses incrementally without reshaping the retry loop.

### Bump default retry budget from 3 to 5
**Chosen**: Default `[reliability].retry_budget = 5`. Configurable.

**Alternative considered**: Keep at 3.

**Why**: With resume-aware retries, each retry is much cheaper than today's fresh-start retry. The marginal cost of a 4th or 5th attempt is small (fast resume, log re-attach) while the operator-time savings are substantial (one more agent recovery rather than a route-advance to a different model). Operators who want stricter behaviour can set the value lower in config.

### `.rally/run-state.json` lifecycle: clear on fresh-start retry, preserve on resume
**Chosen**: The v0.4.0 run-state file (handoff flag, accumulated lap IDs from `laps done` hook) is cleared at relay-runner start of next run. On resume retries, it is preserved so the agent can complete a partial workflow. On fresh-start retries, it is cleared to avoid confusing the new agent context with stale state.

**Alternative considered**: Always preserve, or always clear.

**Why**: Always-clear loses progress: a resume retry that already had `laps done` calls accumulated would lose them. Always-preserve risks: a fresh-start retry inherits a handoff flag from a crashed-mid-handoff run, and the new agent sees stale instructions. The split-by-retry-mode rule reflects that resume continues a session (state should follow) while fresh-start is a new session (state should reset).

### Same-harness next-entry advance uses cheap rotation
**Chosen**: When the scheduler advances to a next entry whose `harness` matches the current entry's, the executor adapter's `RotateModel(newModel string) error` is invoked instead of full teardown/respawn. Cross-harness advances continue to use the v0.6.0 path.

**Alternative considered**: Always teardown/respawn (current v0.6.0 behaviour).

**Why**: Same-harness model swaps are a common pattern (opencode routing GLM → Kimi → Gemini all through the same harness process). Teardown/respawn pays a multi-second startup cost per advance for no functional gain — the harness can swap its model string and continue. Adapters that can't rotate cleanly declare `RotateSupported() bool = false` and rally falls back to teardown.

### Freeze detection: log-mtime + conn + IO, conservative default threshold
**Chosen**: A try is flagged frozen when ALL of: (a) log file mtime hasn't advanced in `freeze_threshold_secs` (default 180), (b) the agent process group has zero active TCP connections (Linux only; on macOS this clause is treated as satisfied by default), (c) IO byte counters haven't advanced. On freeze, rally graceful-kills the try (SIGTERM, 5-second drain, then SIGKILL), counts it as a retry-eligible failure, and advances through the resume-aware retry path.

**Alternative considered (a)**: Faster default (60s).
**Alternative considered (b)**: Use only log-mtime.

**Why**: 180s is conservative but correct: a real freeze costs the operator zero bandwidth (the frozen agent isn't producing output anyway), so the cost of waiting is minor. False positives — graceful-killing a slow-but-progressing agent — are far more expensive (lost work, retry context fee). Combining all three signals reduces false positives substantially over log-mtime alone, while keeping the behaviour predictable across platforms.

### Liveness probe: opt-in, per-adapter support, default off
**Chosen**: A `[reliability].liveness_probe = true` config opt-in enables an active "respond with OK" prompt during ambiguous freeze scenarios. The probe is skipped automatically for harnesses whose adapter declares `LivenessProbeSupported() bool = false`. Default off because:
- Claude interrupts on a second prompt (incompatible)
- Codex tolerates parallel prompts (viable)
- Opencode/Gemini behaviour is untested (adapter declares support per-runtime)

**Alternative considered**: Always-on probe with smart per-harness gating built into the relay-runner.

**Why**: Building harness-specific behaviour into the relay-runner couples it to harness internals, which evolve faster than rally. Per-adapter declaration centralises the harness-specific knowledge with the harness adapter. Default-off because the failure mode (probe induces the failure it's diagnosing) is severe and the benefit is modest — most freezes are detected by the passive monitoring path.

### Error classification table drives retry strategy
**Chosen**: A static lookup table in `internal/reliability/patterns.go` maps known harness error patterns to retry strategies:

| Pattern                                  | Strategy            |
|------------------------------------------|---------------------|
| opencode "API bad request" from provider | rotate (advance route) |
| gemini-cli exit 1                        | resume + retry      |
| claude rate-limit interrupt              | wait + resume       |
| codex completion despite limit warning   | no-op               |
| unknown failure                          | fresh restart       |

Patterns are matched against the last N lines of the try log (deterministic, no heuristics on partial output). New harness CLIs add rows; the table is the only place to update.

**Alternative considered**: Generic retry policy with no harness-specific paths.

**Why**: Generic policy treats every failure identically and misses the obvious wins (claude rate-limit → wait + resume is the right answer; opencode API-bad-request → rotate is the right answer; treating both as "fresh restart" loses information the executor already has). A static table is brittle if harness CLIs change unannounced, but it's also the cheapest place to encode this knowledge and changes are localised. Integration tests exercise each pattern.

### `[reliability]` config table for the new tunables
**Chosen**: Add a `[reliability]` table to `.rally/config.toml` with `freeze_threshold_secs` (default 180), `liveness_probe` (default false), `retry_budget` (default 5), and per-harness `chars_per_token` overrides (the v0.3.0 tokens estimator's divisors, surfacing as config now that we have a place for them).

**Alternative considered**: Spread the new fields across existing sections.

**Why**: The reliability/retry/freeze knobs are conceptually related and operators will tune them together. A dedicated section keeps the surface coherent. The `chars_per_token` migration from v0.3.0 hardcoded constants to v0.7.0 config-overridable is a small win that lands cleanly here.

### Stale handoff flag handling on crash-between-handoff-calls
**Chosen**: If a run crashes between the first and second `laps handoff` calls AND resume isn't supported by the harness, the fresh-start retry clears the handoff flag in `.rally/run-state.json` (a stale handoff prompt would confuse the new agent). The original handoff intent is lost, but the lap remains open so the next run picks it up normally.

**Alternative considered**: Persist the flag across fresh-start retries.

**Why**: Carrying state across fresh starts conflates two different agent contexts. The lap-still-open property guarantees the work isn't lost; only the half-formed handoff intent is lost, which is the cheaper failure mode. Operators see in the progress log that the lap remained open.

## Risks / Trade-offs

- **False-positive freeze kills waste partial work** → Mitigation: 180s default is conservative; resume-retry softens the cost (the killed try's session can be resumed). Tunable per workspace.
- **Liveness probe could induce the failure it's diagnosing** → Mitigation: opt-in + per-adapter capability check. Default off. Adapters that don't declare support are skipped silently even when the global flag is on.
- **Error patterns drift as harness CLIs evolve** → Mitigation: static table is the only place to update; integration tests exercise each pattern with fixture log content. Pattern misses fall through to "fresh restart" (the safe default), so a missing pattern degrades gracefully rather than mis-routing.
- **Stale handoff flag in run-state when crash happens at the wrong moment** → Mitigation: cleared on fresh-start retry; lap remains open so work isn't lost; progress log shows the handoff intent absent. v0.7.0 doesn't try to reconstitute the handoff intent (deferred to a future change if needed).
- **Cheap-rotation path adds an `RotateModel` adapter method that has to be implemented per harness** → Mitigation: defaults to `false` (use teardown/respawn), so unimplemented harnesses still work correctly; adapters opt in incrementally.
- **Resume + run-state preservation could leak across run boundaries if retry budget is exhausted** → Mitigation: retry-budget exhaustion always triggers route advance, which clears run-state at next-run start (per v0.4.0). Resume preserves only across retries within a single run.

## Migration Plan

1. **Adapter capabilities**: extend the executor interface with `ResumeSupported() bool`, `RotateSupported() bool`, `LivenessProbeSupported() bool`, `CharsPerToken() float64`, `RotateModel(newModel string) error`, `ProbeLiveness(ctx) (bool, error)`. Existing adapters return `false` / no-ops; per-harness implementations land in subsequent commits.
2. **Resume wiring**: relay-runner's retry loop captures the session-id from `TryResult`, stashes it in `.rally/run-state.json` for the duration of the run, and passes it to the next try if `ResumeSupported() == true`.
3. **Freeze detector**: `internal/reliability/freeze.go` consumes the v0.3.0 monitoring signals, emits `OnAgentFailed(entry, "freeze")` to the scheduler when the threshold trips. Uses graceful-kill (SIGTERM → 5s → SIGKILL) on the agent process group.
4. **Cheap rotation**: scheduler emits a `(prevEntry, nextEntry)` pair on advance; relay-runner checks if the harnesses match and calls `RotateModel` instead of teardown when they do.
5. **Error classification**: `internal/reliability/patterns.go` table; matched against the last N lines of the try log post-failure; the resulting strategy drives the retry-loop's next action (`rotate`, `resume + retry`, `wait + resume`, `no-op`, `fresh restart`).
6. **Liveness probe**: `internal/reliability/probe.go` implements the side-channel prompt; gated by config + adapter capability.
7. **Config**: extend the v0.5.0 schema with `[reliability]`. Defaults match the values listed above.
8. **Run-state lifecycle**: relay-runner clears on fresh-start retry, preserves on resume retry, clears on next-run start (v0.4.0 behaviour preserved).

Rollback: revert v0.7.0. The `[reliability]` config table is additive and would be ignored after revert. Adapter capability methods stay (unused by old code paths). No persistent state on disk needs cleaning up.

## Open Questions

- The exact heuristic for "ambiguous" freeze that triggers the liveness probe (e.g. log writes happening but content is repetitive). v0.7.0 ships a placeholder ("log-mtime advancing but no IO progress for 60s") and the heuristic is tunable as we gather data. Documented as a known approximation.
- Whether error patterns should be hot-reloadable (e.g. update the table without redeploy). v0.7.0 ships them as compiled-in. If operators need updates without a release, a future change can add a `.rally/error-patterns.toml` overlay.
- Whether `OnAgentRecovered` should be triggered for entries that were exhausted by retry budget (vs only those frozen by detector signal). v0.7.0 only auto-recovers detector-marked entries on cycle wrap; budget-exhausted entries require an explicit operator action (or a new run).
