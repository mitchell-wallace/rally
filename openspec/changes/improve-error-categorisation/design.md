## Context

Failure classification today lives in `internal/reliability/patterns.go`:
`ClassifyError(logLines, ctx...)` returns a `StrategyDecision{Strategy, Cooldown,
Reason, FailureClass}`. It checks the incomplete/dirty-tree context first
(`patterns.go:241`), then walks an ordered `ErrorPatterns` table of global
`containsSubstring` matches (`patterns.go:250`), defaulting to `agent_error`. The
single call site is `runner.go:1272`, inside `runOne`'s attempt loop
(`runner.go:1269-1301`), where the failing harness is already in scope as
`picked.Harness`. `runOne` returns only the coarse `reliability.FailureClass`
(`runner.go:906`) to the routing dispatch loop (`runner.go:520-593`).

Resilience and routing already separate three concepts (from
`harden-relay-run-lifecycle`): **stall** (liveness, `reliability/stall.go`),
**frozen** (the consecutive-infra circuit breaker, per `ResilienceKey{Harness,
Model}`, `resilience.go`), and **benched** (a scheduler route-entry out of
rotation, `EntryState.Benched` in `routing/scheduler.go`). Benching is driven by
`OnAgentFailed(entry, reason, bench)` / `OnAgentRecovered`, and recovery is
synced from resilience `AgentState` by `syncRecoverySignals`
(`route_runtime.go:247`). The dirty-path exclusion that governs `incomplete`
lives in `internal/gitx/git.go` (`WorkspaceDirtyPaths:96`, with the `.rally/`/
`.laps/` skip duplicated at `git.go:82` and `:112`).

## Goals / Non-Goals

**Goals:**
- One stable taxonomy surfaced consistently across footer, retry line, try
  records, telemetry, and Sentry.
- Separate short rate limits from long usage/quota exhaustion; parse reset/retry
  timing where exposed.
- Provider/config/quota evidence beats `incomplete`; harness-local dirt never
  masks a stronger failure.
- Harness-scoped classification (no cross-harness mislabelling).
- Usage limits bench the affected quota scope until reset.

**Non-Goals:**
- The full harness-adapter normalization / conformance suite
  (`improve-harness-consistency`).
- A per-relay runner-disable UI (`build-new-tui`).
- Changing which failures become Sentry Issues vs spans (telemetry spec).

## Decisions

**1. Typed evidence on `TryResult`, executor-populated, runner fallback.**
Add an optional `FailureEvidence` to `TryResult` (`executor.go:44`). Executors
populate it where they can (starting with harnesses we have signatures for);
`ClassifyError` builds evidence from the log tail when absent.
`improve-harness-consistency` later moves population fully into the adapters. The
`Executor` *interface* is unchanged this round.

```go
type FailureEvidence struct {
    Category   FailureCategory
    Harness    string
    Provider   string        // provider/account segment where known
    QuotaScope string        // harness-aware bench key (Decision 4)
    Message    string        // human-readable, bounded
    StatusCode int
    ResetAfter time.Duration // parsed "resets in …"
    ResetAt    *time.Time
    RetryAfter time.Duration // parsed Retry-After
    RawSignal  string        // bounded raw match for debugging
}
```

Process-level failures (`harness_launch`: `fork/exec`, non-zero exit) surface as
the executor's returned `error` with a nil/partial `TryResult`, so evidence is
absent exactly for those — `ClassifyError` MUST treat `Evidence` as optional and
the fallback parser owns those cases.

**2. Taxonomy and classification priority.** Stable `FailureCategory` constants
with display labels:

| Category | Display | Strategy |
|---|---|---|
| `usage_limit` | `usage limit` (+ `resets in 118h44m`) | bench quota scope until reset; route away; one attempt then bench |
| `short_rate_limit` | `rate limit` (+ `waiting 2m`) | wait parsed `Retry-After`, else short default; resume |
| `provider_overloaded` | `provider overloaded` | resume within retry budget |
| `invalid_model` | `invalid model` | exhaust entry, route away; no identical retry |
| `auth_or_proxy` | `auth/proxy error` | route away; one attempt then route |
| `harness_launch` | `harness launch error` | fresh restart or rotate by subtype |
| `incomplete_finalization` | `incomplete: file changes without finalization` | resume + retry with finalization guidance |
| `agent_error` | `agent error` | existing retry/fresh (default) |

`ClassifyError` is reordered to: (1) typed `FailureEvidence` from the executor;
(2) provider/config/quota evidence from structured data or bounded snippets;
(3) meaningful task-file change → `incomplete_finalization`; (4) harness-scoped
text patterns; (5) default `agent_error`. The dirty-tree check thus moves *below*
provider/config/quota detection — the inverse of today's order.

**3. Category → FailureClass mapping (load-bearing).** The new `Category` is
orthogonal to the existing three-value `FailureClass` (`infra`/`agent`/
`incomplete`) that drives the freeze cascade and `infraFailures++`
(`runner.go:1275-1276`). A category that maps to `FailureInfra` *also* feeds the
freeze counter, so a `usage_limit` mapped to infra would be both frozen (per
harness+model) **and** benched (per quota scope). Required mapping:

| Category | FailureClass | Notes |
|---|---|---|
| `usage_limit` | **not** `infra` | bench only; bounded by the attempt-loop break, not freeze |
| `invalid_model` | **not** `infra` | exhaust entry only |
| `auth_or_proxy` | **not** `infra` | route away; bounded by the attempt-loop break |
| `short_rate_limit` | `infra` | transient; existing wait-resume + freeze accounting |
| `provider_overloaded` | `infra` | transient |
| `harness_launch` | `infra` | transient launch fault |
| `incomplete_finalization` | `incomplete` | unchanged |
| `agent_error` | `agent` | unchanged default |

**4. Harness-aware quota scope.** A standalone pure resolver
`QuotaScope(harness, model) string` (routing/config layer, **not** an `Executor`
method) groups entries sharing a quota bucket:
- **antigravity** — per model *family*. Antigravity model names are free-form
  display labels (`antigravity.go:16`, e.g. `"Gemini 3.5 Flash (High)"`), not
  slugs, so the resolver uses case-insensitive substring matching: `claude` /
  `flash` / `pro` are separate quotas.
- **opencode** — per *provider*, the segment before the `/` in
  `ResolvedAgent.Model` (`provider/model`, e.g. `zai-coding-plan/glm-5.1`,
  `openai/…`).
- **direct harnesses** (`claude`, `codex`, `gemini`) — per harness (the account
  front); the model is ignored, so a stray `/` cannot mis-split.

**5. Benching mechanism (current code does not yet support benching from a
category).** Five plumbing changes, because the category never reaches the bench
layer today and the existing recovery/wait paths would undo or mis-handle a
bench:

1. **Break the attempt loop on first detection.** `usage_limit` and
   `auth_or_proxy` MUST terminate `runOne`'s attempt loop
   (`runner.go:1269-1301`) on first detection (reuse the skip short-circuit or a
   terminal strategy) so they make exactly one attempt — their non-`infra`
   mapping means the freeze counter does not bound them.
2. **Surface the category up.** Extend `runOne`'s return contract
   (`runner.go:906`) to carry the resolved `FailureCategory` + reset evidence to
   the routing dispatch loop (`runner.go:520-593`) where `OnAgentFailed` is
   reachable.
3. **Reset-deadline state + scope-keyed benching.** Add `BenchUntil *time.Time`
   (or equivalent) to `EntryState` (`scheduler.go:8`) and a scope-keyed bench
   helper that benches every entry whose `QuotaScope` matches.
4. **Make `BenchUntil` survive recovery sync.** `syncRecoverySignals`
   unconditionally unbenches benched-but-*active* entries
   (`route_runtime.go:257-260`). Because `usage_limit` benches without touching
   resilience `AgentState`, the entry stays `StateActive` and would be unbenched
   on the next `Next()` cycle, evaporating the bench. The `StateActive` branch
   MUST NOT unbench an entry whose `BenchUntil` is still in the future;
   `BenchUntil` takes precedence. Recovery fires when `now >= BenchUntil`, then
   re-probes once.
5. **Wait out an all-benched lane.** When the scope-keyed bench takes down every
   entry in a lane, `Next()` returns "all exhausted" and `selectionWaitError`
   (`route_runtime.go:313`) currently derives a wait only from `StatePaused`
   entries (`:330`), falling through to `AllFrozen` → the relay-stall capture
   (`runner.go:433`) fails the relay. `selectionWaitError` MUST derive a wait
   from the minimum pending `BenchUntil` and treat "all benched with a future
   reset" as a wait, not a frozen lockout.

**6. Reset persistence (resolved product call).** The reset deadline is persisted
as a new `agent_status.jsonl` resilience event carrying `reset_at` + `quota_scope`
(the store is the only persistence layer; `EntryState` is in-memory only). The
bench **persists across relays** on the same machine — a still-unreset quota is
still unreset for the next relay — and on the first selection after a persisted
deadline passes the scope is **re-probed once**, so a manually-topped-up quota
recovers without operator action. (Default chosen per plan review; reversible.)

## Risks / Trade-offs

- **Antigravity label variance** (versions, `(High)`/`(Low)` suffixes) may outgrow
  substring matching → may later need a maintained label→family table; start with
  substrings.
- **Stale persisted reset** if quota is topped up out of band → mitigated by the
  re-probe-once-after-deadline behavior; an early manual recovery still waits out
  the original deadline.
- **Evidence absent for `harness_launch`** → the fallback parser owns process-level
  failures; `ClassifyError` handles nil `Evidence`.
- **Misattributing a real provider error over genuine task work** → when a try
  produced meaningful task changes *and* hit a provider error, prefer the provider
  error only when structured evidence / tool-call count shows the subprocess never
  reached useful work.

## Open Questions

- Exact full set of harness-local paths beyond `.claude/settings.local.json`
  (start conservative, grow with evidence).
- When *every* entry in a lane is `invalid_model`/benched with no reset, fail the
  relay immediately vs. surface an operator prompt where a TTY is available.
