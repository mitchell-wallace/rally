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
| `transient_infra` | `infra error` | API timeout, network/connection/TLS failure, non-overload 5xx; resume within budget |
| `invalid_model` | `invalid model` | exhaust entry, route away; no identical retry |
| `auth_or_proxy` | `auth/proxy error` | route away; one attempt then route |
| `harness_launch` | `harness launch error` | fresh restart or rotate by subtype |
| `incomplete_finalization` | `incomplete: file changes without finalization` | resume + retry with finalization guidance |
| `agent_error` | `agent error` | existing retry/fresh (default) |

Discriminator for the two transient infra categories: a 5xx whose status/body
matches a known provider-overload signal (HTTP 529, `overloaded_error`,
`503 Service Unavailable` "Overloaded") classifies `provider_overloaded`; all
other 5xx, API timeouts, and connection/TLS failures classify `transient_infra`.
Both map to infra-class and resume within budget, so a misclassification between
the two is behaviourally harmless — the split exists for display/triage clarity.

The liveness **stall** kill is not a log-text category: the stall path sets an
infra `FailureClass` directly (`reliability/stall.go`), independent of the
`FailureCategory` assigned from output. It is left as-is.

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
| `usage_limit` | `agent` | bench only; bounded by the attempt-loop break, not freeze; mapped to agent-class so it does not increment the freeze counter |
| `invalid_model` | `agent` | exhaust entry only; mapped to agent-class so it does not increment the freeze counter |
| `auth_or_proxy` | `agent` | route away; bounded by the attempt-loop break; mapped to agent-class so it does not increment the freeze counter |
| `short_rate_limit` | `infra` | transient; existing wait-resume + freeze accounting |
| `provider_overloaded` | `infra` | transient |
| `transient_infra` | `infra` | API timeout / network / TLS / non-overload 5xx |
| `harness_launch` | `infra` | transient launch fault |
| `incomplete_finalization` | `incomplete` | unchanged |
| `agent_error` | `agent` | unchanged default |

Freeze accounting derives **solely** from the mapped `FailureClass`: the
`infraFailures++` increment (`runner.go:1275`) already gates on
`FailureClass == FailureInfra`, so this mapping table is the single source of
truth for what counts toward the freeze cascade. The agent-class assignment for
`usage_limit`/`invalid_model`/`auth_or_proxy` is the deliberate lever that keeps
them out of freeze accounting; no separate per-`Category` check is added at the
increment site (a category-keyed predicate there would be a second, redundant
source of truth — the mapping table is authoritative).

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

**5. Benching mechanism — a deadline-carrying resilience state, not a parallel
rotation axis.** The scheduler's `EntryState.Benched` bit is **not** an
independent source of truth: `syncRecoverySignals` (`route_runtime.go:247`)
recomputes it from `resilience.GetState(key)` every selection cycle
(`StatePaused`→bench, elapsed→reset, `StateFrozen`→bench, `StateActive`→unbench).
`StatePaused` already implements "bench until a deadline, wait it out via
`selectionWaitError`, then reset → reselect → re-pause if still failing" — exactly
the usage-limit lifecycle, missing only (a) a *variable* deadline (vs the fixed
`PauseDuration`) and (b) *quota scope* (pause keys `{Harness, Model}`). So
usage-limit benching is modelled as a new resilience state reusing that pipeline,
rather than a second out-of-rotation axis on `EntryState`. Changes:

1. **Break the attempt loop on first detection.** `usage_limit` and
   `auth_or_proxy` MUST terminate `runOne`'s attempt loop
   (`runner.go:1269-1301`) on first detection (reuse the skip short-circuit or a
   terminal strategy) so they make exactly one attempt — their non-`infra`
   mapping means the freeze counter does not bound them.
2. **Surface the category up.** Extend `runOne`'s return contract
   (`runner.go:906`) to carry the resolved `FailureCategory` + reset evidence to
   the routing dispatch loop (`runner.go:520-593`) where the route runtime is
   reachable.
3. **New `StateBenched` resilience state with a reset deadline.** Add
   `StateBenched` to `AgentState` (`resilience.go:13`) and a `benched`
   `AgentStatusEvent` type (`records.go:62`) carrying `reset_at` + `quota_scope`.
   `GetState` (`resilience.go:59`) returns `StateBenched` while `now < reset_at`
   and surfaces the key as `StateActive` for a single re-probe once the deadline
   passes — mirroring the existing pure-read frozen→probation decay
   (`resilience.go:89-93`). A `BenchAgent(key, resetAt, scope, relayID)` method
   persists the event alongside `PauseAgent`/`FreezeAgent`.
4. **Bench the whole quota scope from one write site.** On a `usage_limit`, a
   route-runtime `benchQuotaScope` helper (mirroring `forceUnpauseAll`,
   `route_runtime.go:360`) iterates every scheduler's entries, resolves each
   `{Harness, Model}` key and its `QuotaScope` (Decision 4), and writes a
   `benched` event for every distinct matching key. All scope fan-out is
   contained here; `GetState`, the sync, and the wait stay per-key and unchanged.
5. **One new sync arm — no guard.** `syncRecoverySignals` (`route_runtime.go:256`)
   gains `case StateBenched: OnAgentFailed(state, "quota", true)`. Because a
   benched key is **never** `StateActive`, the existing `StateActive`→unbench arm
   never touches it, and the `StatePaused`/`StateProbation`/`StateFrozen` arms are
   untouched. The fragile "unbench guard scoped to `StateActive` only" that a
   parallel `EntryState` axis would require is unnecessary.
6. **Wait out an all-benched lane.** `selectionWaitError` (`route_runtime.go:313`)
   currently derives a wait only from `StatePaused` entries (`:330`); widen that
   predicate to also include `StateBenched` and derive the wait from the earliest
   `reset_at`, so an all-benched lane waits instead of falling through to
   `AllFrozen` → the relay-stall capture (`runner.go:433`). `forceUnpauseAll`
   likewise widens to clear `StateBenched` on operator skip.

**6. Reset persistence and recovery semantics (resolved product call).**
Persistence is **intrinsic** to Decision 5's `StateBenched`: the `benched` event
in `agent_status.jsonl` is the only persistence layer (`EntryState` is in-memory),
so the bench **persists across relays** on the same machine with *no dedicated
restoration path* — `GetState` replays it exactly as it does `frozen`. The
`benched` event MUST be added to the `truncateAgentStatus` retention allow-list
(`store.go:128`, currently `frozen`/`probation` only) so a multi-day reset is not
truncated away. On the first selection after a persisted deadline passes, the
scope surfaces as active for a **single re-probe**; if that re-probe again returns
`usage_limit`, the scope is re-benched for a fresh window (parsed reset or
default) rather than treated as permanently exhausted. (Default chosen per plan
review; reversible.)

This drops the earlier "BenchUntil on `EntryState` + separate persistence event +
restoration scanner" design, which re-implemented the pause pipeline as a second
mechanism. The `harden-relay-run-lifecycle` three-concept split (stall / frozen /
benched) is preserved: `StateBenched` stays distinct from `StateFrozen`, sharing
the recovery mechanism without conflating the infra circuit-breaker with quota
exhaustion.

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
