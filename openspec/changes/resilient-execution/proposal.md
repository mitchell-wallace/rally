## Why

A long relay routinely hits transient failures: opencode dropping mid-stream on a Kimi 400, gemini-cli exiting 1, claude interrupting on a rate-limit error, GLM stalling mid-session when its quota tips over. Today rally's response is "retry up to 3 times from scratch, then mark failed." That wastes context on every retry, doesn't take advantage of harnesses that *can* resume, treats freezes and exit-1s identically, and has no notion of intra-session rate-limit detection.

With v0.3.0 monitoring signals and v0.5.0/v0.6.0 provider shortcuts in hand, we can do better: resume where supported, rotate providers within a route on error, detect freezes from monitoring data, and (opt-in) probe live sessions when the freeze signal is ambiguous.

## Prerequisites

- v0.3.0 (`improve-cli-run-experience`) — log-file mtime, connection/IO warning signals, live monitor
- v0.5.0 (`rally-config-and-harness-shortcuts`) — provider shortcuts to refer to alternatives
- v0.6.0 (`role-aware-routing`) — within-list fallback iterator reusable for provider rotation

## What Changes

### Resume-aware retries
- For harnesses that support session resume (capability declared in the executor adapter), retries pass the harness-specific resume flag instead of restarting from scratch when a session existed at failure time
- Per-harness capability matrix lives in the adapter; rally falls back to a fresh start when resume is unsupported or the session ID is lost
- Bump default per-try retry budget from 3 to 5 (configurable via `[reliability].retry_budget`)
- Resume preserves run-scoped state in `.rally/run-state.json` (the v0.4.0 handoff flag, `mb done`-accumulated bead IDs). On a fresh start retry, that state is cleared so the resumed-vs-fresh boundary is clean. A handoff flag set in a crashed-before-finalisation run is also cleared on fresh start; on resume, it is left in place so the agent can complete the second `mb handoff` call.

### Provider rotation within a harness
- v0.6.0 already advances the active route's cursor on retry-budget exhaustion. v0.7.0 adds a cheap-rotation path: when the next entry uses the **same harness** with a different model, rally swaps the model string in-place rather than tearing down and respawning the harness process
- Specifically targets the opencode case: GLM → Kimi → Gemini routed through the same opencode harness, where switching the model string is cheap
- For cross-harness advances within the route list, fresh harness invocation is unavoidable — the path is unchanged from v0.6.0
- Applies to the `default` route in no-backend mode too, since `default` is just a route like any other

### Freeze detection via monitoring
- Use v0.3.0 log-file mtime + connection signal + IO delta to detect stalled tries:
  - No log writes for >`freeze_threshold_secs` AND no active connections AND no IO delta → mark stalled
  - Default `freeze_threshold_secs = 180`, configurable
- Stalled tries are graceful-killed, counted as a retry-eligible failure, and re-attempted via the resume-aware retry path

### Intra-session liveness probe (experimental, opt-in)
- Optional background "respond with OK" probe when freeze signal is ambiguous (e.g. log writes happening but content is repetitive or non-progressing — exact heuristic deferred)
- Sends a side-channel prompt to the running session and times its response
- Behind `[reliability].liveness_probe = true`; default off because behaviour varies across harnesses:
  - Claude interrupts on a second prompt — incompatible
  - Codex tolerates parallel prompts — viable
  - Opencode/Gemini behaviour untested — adapter declares support
- Probe is skipped automatically for harnesses whose adapter declares `liveness_probe = false`

### Better error classification
- Parse known harness error patterns and map each to a retry strategy:
  | Pattern                                  | Strategy            |
  |------------------------------------------|---------------------|
  | opencode "API bad request" from provider | rotate (next route) |
  | gemini-cli exit 1                        | resume + retry      |
  | claude rate-limit interrupt              | wait + resume       |
  | codex completion despite limit warning   | no-op               |
  | unknown failure                          | fresh restart       |
- Pattern table in `internal/reliability/patterns.go`, easy to extend
- Patterns are matched against last N lines of the try log (deterministic, no heuristics on partial output)

## Capabilities

### New Capabilities
- `resume-retries`: Per-harness session resume on retry where supported, fresh start otherwise
- `provider-rotation`: In-route provider switching on error, reusing v0.6.0's within-list iterator
- `freeze-detection`: Stalled-try detection via log mtime + connection + IO signal, with graceful kill
- `liveness-probe`: Opt-in side-channel prompt to disambiguate freeze vs. silent-progress
- `error-classification`: Harness-specific error pattern matching with strategy mapping

### Modified Capabilities
- `executor`: Adapters declare `resume_supported` and `liveness_probe_supported` capabilities; accept resume parameters; surface session IDs
- `relay-runner`: Retry loop integrates resume + rotation + freeze detection; default retry budget raised from 3 to 5; clears `.rally/run-state.json` on fresh-start retry, preserves it on resume
- `live-monitor`: Surfaces freeze/stall state to the operator
- `quota-scheduler`: Same-harness next-entry advance gains a cheap-rotation path that swaps the model string without harness teardown

## Impact

- New package: `internal/reliability/` (freeze detector, error classifier, liveness probe, retry orchestrator)
- Config: new `[reliability]` table — `freeze_threshold_secs`, `liveness_probe`, `retry_budget`
- Per-harness adapter changes: resume flag wiring in claude, codex, opencode, gemini executors; capability declarations
- Risk: false-positive freeze kills could waste partial work — threshold is conservative by default; resume-retry softens the cost
- Risk: liveness probe could itself induce the failure it's diagnosing on harnesses that don't support concurrent prompts — gated behind explicit opt-in + adapter capability check
- Risk: error patterns drift as harness CLIs evolve — patterns table is the only place to update; integration tests exercise each pattern
- Risk: stale handoff flag in `.rally/run-state.json` if a run crashes between the first and second `mb handoff` calls AND resume isn't supported by the harness. On fresh-start retry rally clears the flag (a stale prompt would confuse the new agent); the original handoff intent is lost, but the bead remains open so the next run picks it up normally
