## Draft: Improve Error Categorisation And Handling

## Why

Recent real Rally relays showed the failure taxonomy is too coarse and sometimes
actively misleading. The CLI surface has improved, but the underlying
classification still conflates distinct operational causes, so the labels that
drive operator decisions, retry timing, benching, and Sentry issues are often
wrong. The objective of this change is **cleaner, more consistent, and more
understandable error handling and reporting**: one taxonomy, one priority order,
and one typed evidence shape used everywhere a failure is decided, displayed, or
recorded.

Concretely, today (`internal/reliability/patterns.go`,
`internal/relay/runner.go:1270`):

- `rate limit` is one bucket for five different conditions (short throttle,
  provider overload, multi-day subscription exhaustion, proxy block,
  invalid-model fallback). They need different actions.
- **`incomplete` wins first.** `ClassifyError` checks the dirty-tree context
  before the pattern table (`patterns.go:239`), so a dirty harness-local file
  (e.g. `.claude/settings.local.json`) masks a stronger `RESOURCE_EXHAUSTED` or
  `model_not_found` as `incomplete: file changes without finalization`.
- **Patterns are global substring matches** (`containsSubstring` over the last
  50 log lines), not harness-scoped, so a Codex VERIFY run gets labelled
  `claude rate-limit interrupt` because the log tail incidentally contains the
  prose "rate-limit".
- Classification reads arbitrary text only; harnesses that emit structured error
  events get no benefit.
- Display label and retry strategy are coupled to the same `Reason` string;
  machine consumers (telemetry, scheduler) have to string-match.

## Evidence From Recent Runs

Condensed from the relay forensics that motivated this change:

- **Antigravity Claude quota, zero changed files** (container `30cb9624d27c`,
  tries 57-61): five `RESOURCE_EXHAUSTED (429) … Resets in 123h50m` failures
  shown as generic `agent error`. A days-long subscription limit, not a per-minute
  rate limit.
- **Antigravity Claude quota, only harness-local dirty** (`ca5990ebcc41`,
  tries 242-246): same `RESOURCE_EXHAUSTED … Resets in 118h44m`, but the only
  dirty path was `.claude/settings.local.json`, so it displayed
  `incomplete: file changes without finalization` — the dirty-path rule masked
  the provider failure.
- **Invalid model masked as incomplete** (`ca5990ebcc41`, tries 247-251): model
  intentionally renamed to `claude-opus-4-8-disabled`; produced `model_not_found`
  / 404 then `403 CONNECT blocked`, again shown as `incomplete` because the same
  harness-local path was dirty.
- **Codex mislabelled as Claude rate limit** (`30cb9624d27c`, tries 70-74): a
  Codex (`gpt-5.4`) VERIFY lap produced useful summaries but was labelled
  `claude rate-limit interrupt` / `rate limit generic` from incidental prose, and
  then pointlessly waited one minute per try.
- **Incomplete retry-to-success** (`30cb9624d27c`, try 62 → retry): a genuine
  incomplete-finalization retry succeeded — the flow is good, but the resumed
  prompt should carry explicit finalization guidance.

## Goals

- One taxonomy of stable categories, surfaced consistently in the footer, retry
  line, try records, telemetry, and Sentry.
- Separate short rate limits from long usage/quota exhaustion, and parse reset /
  retry timing when providers expose it (no one-minute waits for multi-hour
  limits).
- Provider / config / quota evidence beats `incomplete`; harness-local dirty
  files never mask a stronger failure.
- Harness-scoped classification: a Codex failure can never be labelled a Claude
  rate limit.
- A typed `FailureEvidence` carried on `TryResult`, so display, retry, scheduler,
  and telemetry all read structured fields instead of re-parsing a string.
- Usage limits **bench** the affected quota scope until reset; better categories
  feed benching, fallback, and retry/resume policy.
- Regression coverage built from the concrete signatures above.

## Non-Goals

- Not the full harness-adapter normalization — that is `improve-harness-consistency`
  (next-up #3). This change introduces `FailureEvidence` and begins populating it;
  #3 completes moving population into every adapter under a conformance suite.
- Not a per-relay runner-disable UI. `invalid_model` is classified correctly so
  the invalid-model-name workaround is no longer *needed*, but the ergonomic
  "disable a runner for this relay" flow is deferred to `build-new-tui` / steering.
- Not a change to which failures become Sentry Issues vs spans (owned by the
  telemetry spec).
- Rally core stays OpenSpec-agnostic.

## Decisions

These four forks were settled on 2026-06-09:

1. **Typed evidence lives on `TryResult` now, executor-populated.** Add a
   `FailureEvidence` value to `TryResult`; executors begin populating it this
   change (starting with the harnesses we have signatures for), and
   `improve-harness-consistency` finishes the job for all adapters with a
   conformance suite. Runner-side log parsing remains the fallback path for
   harnesses not yet emitting structured evidence.
2. **Usage limits bench the quota scope until reset.** A `usage_limit` does not
   `frozen` the runner (that is the consecutive-infra circuit breaker, per
   `ResilienceKey{Harness, Model}`) and does not loop one-minute waits — it
   **benches** the affected route entries. The existing bench machinery
   (`routing/scheduler.go` `OnAgentFailed(entry, reason, bench=true)` /
   `OnAgentRecovered`) is necessary but **not sufficient** as-is: today
   `EntryState` carries no time field and recovery is driven only by
   `syncRecoverySignals` (`route_runtime.go:247`) reading resilience
   `AgentState`. This change adds reset-deadline-driven recovery and scope-keyed
   benching — see "Retry, Benching, And Routing" for the concrete mechanism. If
   no reset is parseable, bench with a long conservative default and route away.
3. **Quota scope is a harness-aware key, not flat `harness+model`.** Benching one
   model must bench everything sharing the same account/quota bucket. The scope
   key is computed by a single pure resolver `QuotaScope(harness, model) string`
   (a standalone routing/config-layer function, **not** a new `Executor` method —
   keep the `Executor` interface unchanged this round), testable in isolation:
   - **antigravity** — per model *family*. Antigravity model names are free-form
     display labels (`DefaultAntigravityModel = "Gemini 3.5 Flash (High)"`,
     `antigravity.go:16`), **not** slugs, so the resolver uses case-insensitive
     substring matching over the label: `claude` / `flash` / `pro` (gemini-pro)
     are separate quotas (Claude exhaustion does not bench Gemini).
   - **opencode** — per *provider*, the segment before the `/` in the model id
     (`ResolvedAgent.Model` is `provider/model`, e.g. `zai-coding-plan/glm-5.1`,
     `openai/…`). There is no global "opencode sub"; a Codex sub used via opencode
     appears as `openai/…`.
   - **direct harnesses** (`claude`, `codex`, `gemini`) — per harness, which is
     effectively the account front.
   Benching applies to every route entry whose resolved scope key matches. (Label
   variance for antigravity — versions, `(High)` suffixes — may later need a
   maintained mapping table; see Residual Open Questions.)
4. **`invalid_model` is classified, not actioned with new UI.** It marks the
   route entry exhausted and routes away (it must not spend the retry budget on
   identical 404s), and surfaces the reason. No new disable flag here.

## Proposed Taxonomy

Stable internal `FailureCategory` constants with short display labels. This
replaces the current `claude rate-limit interrupt` / `rate limit generic` /
`usage limit hit` pattern names with a typed set:

| Category | Display | Strategy |
|---|---|---|
| `usage_limit` | `usage limit` (+ `resets in 118h44m`) | bench quota scope until reset; route away; never 1-min loop |
| `short_rate_limit` | `rate limit` (+ `waiting 2m`) | wait parsed `Retry-After`, else short default; resume |
| `provider_overloaded` | `provider overloaded` | resume within retry budget; do not bench for days |
| `invalid_model` | `invalid model` | exhaust entry, route away; do not retry identically |
| `auth_or_proxy` | `auth/proxy error` | route away, surface operator action |
| `harness_launch` | `harness launch error` | fresh restart or rotate by subtype |
| `incomplete_finalization` | `incomplete: file changes without finalization` | resume + retry **with finalization guidance** |
| `agent_error` | `agent error` | existing retry/fresh behaviour (default) |

Signatures that map into each (parser targets):

- `usage_limit` — Antigravity/Gemini `RESOURCE_EXHAUSTED … Individual quota
  reached … Resets in <dur>`; Codex "you've hit your usage limit"; Claude
  `rate_limit_event` of `five_hour` / `seven_day` when overage is rejected.
- `short_rate_limit` — `429 Too Many Requests` with a small `Retry-After`.
- `provider_overloaded` — Claude `529 Overloaded`, `503 Service Unavailable`.
- `invalid_model` — Claude `model_not_found`, "selected model may not exist or you
  may not have access".
- `auth_or_proxy` — `Failed to authenticate`, `CONNECT blocked`, OAuth failures.
- `harness_launch` — `fork/exec`, `argument list too long`, `exec format error`,
  executable missing (the existing harness/launch patterns).

## Classification Priority

The key behaviour change. `ClassifyError` is reordered to:

1. **Typed `FailureEvidence` from the executor** (if present) — authoritative.
2. **Provider / config / quota evidence** from structured data or bounded error
   snippets (`usage_limit`, `invalid_model`, `auth_or_proxy`,
   `provider_overloaded`, `short_rate_limit`).
3. **Meaningful task-file change** → `incomplete_finalization`.
4. **Harness-scoped text patterns** as fallback.
5. Default `agent_error`.

So a dirty path can no longer mask `RESOURCE_EXHAUSTED`, `model_not_found`, or
`Failed to authenticate` — the dirty-tree check moves *below* provider/config/quota
detection, the inverse of today's ordering (`ClassifyError` at `patterns.go:237`
runs the incomplete pre-check at `patterns.go:241`, before the pattern loop at
`patterns.go:250`).

### Category → FailureClass mapping

The new `Category` is **orthogonal** to the existing three-value `FailureClass`
(`infra`/`agent`/`incomplete`) that drives the freeze cascade and `infraFailures++`
(`runner.go:1275-1276`). The mapping is load-bearing and must be stated explicitly,
because a category that maps to `FailureInfra` *also* feeds the freeze counter — so a
`usage_limit` mapped to infra would be **both** frozen (per harness+model) and benched
(per quota scope), contradicting Decision §2. Required mapping:

| Category | FailureClass | Notes |
|---|---|---|
| `usage_limit` | **not** `infra` | bench only; must not increment freeze counter; retries bounded by terminating the attempt loop on first detection (Mechanism §1), not the freeze counter |
| `invalid_model` | **not** `infra` | exhaust entry only; not a transient infra fault |
| `auth_or_proxy` | **not** `infra` | route away; bounded by the same attempt-loop break (Mechanism §1), not freeze |
| `short_rate_limit` | `infra` | transient; existing wait-resume + freeze accounting fine |
| `provider_overloaded` | `infra` | transient |
| `harness_launch` | `infra` | transient launch fault |
| `incomplete_finalization` | `incomplete` | unchanged |
| `agent_error` | `agent` | unchanged default |

## Harness-Scoped Patterns

`ClassifyError` (and the pattern table) gain the harness identity, which is
already in scope at the call site as `picked.Harness`
(`runner.go:1272`). Each `Pattern` carries an optional harness constraint; a
pattern only matches when the failing try's harness matches (or the pattern is
harness-agnostic, e.g. generic network errors). Harness names are removed from
the *display label* of generic reasons — `usage limit` not
`claude rate-limit interrupt`, unless the harness genuinely is Claude and the
label is intentionally Claude-specific.

## Harness-Local Dirty Path Handling

Meaningful-task-change detection excludes paths that are not task work, so they
cannot trigger `incomplete_finalization` or mask a provider error. The exclusion
that actually governs the `incomplete` decision lives in **`internal/gitx/git.go`**,
not the runner: the `incomplete` flag derives from `WorkspaceDirtyPaths`
(`git.go:96`), and the same `.rally/`/`.laps/` prefix skip is duplicated in
`IsWorkspaceDirty` (`git.go:82`) and `WorkspaceDirtyPaths` (`git.go:112`) (and a
third copy in the `filesChanged` path used for records). Adding a path in only one
place will not stop the masking.

- Already excluded: `.rally/`, `.laps/` (two+ duplicated copies today).
- **Extract a single shared exclusion helper** (e.g.
  `gitx.IsRallyOwnedOrTransientPath`) consumed by all call sites, so the rule is
  consistent and `incomplete` actually stops triggering on harness-local-only dirt.
- Add known harness-local transient state to that helper, starting with
  `.claude/settings.local.json`.
- A try whose *only* dirty paths are excluded is treated as having no meaningful
  task changes.

Conservative and test-covered:

- only `.claude/settings.local.json` dirty + `RESOURCE_EXHAUSTED` → `usage_limit`.
- `src/foo.go` dirty, no finalization → `incomplete_finalization`.
- meaningful task file dirty *and* a provider error where the subprocess never
  reached useful work → prefer the provider error (relies on the structured
  evidence / tool-call count, not just the dirty tree).

## Structured Error Evidence

Add a typed evidence value carried on `TryResult` and produced by executors,
with runner-side log parsing as the fallback populator:

```go
type FailureEvidence struct {
    Category   FailureCategory
    Harness    string
    Provider   string        // provider/account segment where known
    QuotaScope string        // harness-aware bench key (see Decisions §3)
    Message    string        // human-readable, bounded
    StatusCode int
    ResetAfter time.Duration // parsed "resets in …"
    ResetAt    *time.Time
    RetryAfter time.Duration // parsed Retry-After
    RawSignal  string        // bounded raw match for debugging
}
```

`StrategyDecision` carries the stable `Category` and a separate display label so
nothing downstream has to string-match `Reason`. Executors populate
`TryResult.Evidence` (`executor.go:44`) where they can; `ClassifyError` falls back
to building evidence from the log tail for harnesses that don't yet.

Note the population gap: process-level failures (`harness_launch` — `fork/exec`,
non-zero exit) surface as the executor's returned `error` with a nil or partial
`TryResult`, so executor-populated evidence is **absent exactly for those**.
`ClassifyError` must therefore treat `Evidence` as optional (handle nil/empty
gracefully) and the fallback log parser owns `harness_launch` and any other
failure where `TryResult` is nil.

Observed parser targets: Antigravity/Gemini `RESOURCE_EXHAUSTED` /
`Individual quota reached` / `Resets in <dur>`; Claude stream-JSON
`rate_limit_event`, `api_error_status`, `model_not_found`,
`authentication_failed`, 529 overload; Codex `turn.failed` usage-limit messages;
opencode JSON error events and provider API errors (kept out of generic substring
labels).

## Retry, Benching, And Routing

- `usage_limit` — bench the quota scope (Decisions §2/§3) until parsed reset;
  long conservative default + route away when no reset is available. Scheduler
  recovery becomes reset-time-driven, not only the frozen→probation decay.
- `short_rate_limit` — wait parsed `Retry-After`, else a short default, then
  resume.
- `provider_overloaded` — resume within the existing retry budget; never bench
  for days.
- `invalid_model` — exhaust the entry, route away; do not retry the same model.
- `auth_or_proxy` — route away and surface operator action.
- `harness_launch` — fresh restart or rotate by subtype (unchanged intent).
- `incomplete_finalization` — resume + retry with finalization guidance.

### Mechanism (current code does not yet support benching from a category)

Five plumbing changes are required, because today the category/evidence never
reaches the layer that benches, the per-run attempt loop would burn the whole
budget against a dead quota first, and the existing recovery/wait paths would
undo or mis-handle the bench:

1. **Break the attempt loop on first detection.** `ClassifyError` and strategy
   dispatch run *inside* `runOne`'s attempt loop (`runner.go:1269-1301`), which
   only short-circuits when a strategy sets `r.skipFlag` (rotate) or NoOp-succeeds.
   `usage_limit` and `auth_or_proxy` must terminate the loop on first detection
   (reuse the skip short-circuit or add a terminal strategy) so they make
   **exactly one attempt** and never re-run the exhausted quota across
   `maxAttempts`. (Their non-`infra` mapping means the freeze counter does not
   bound them — the loop break is what bounds them.)
2. **Surface the category up to the routing loop.** `runOne` currently returns
   `(…, reliability.FailureClass, int, error)` (`runner.go:906`) — only the
   coarse class. Extend its return contract (or add a side channel) to carry the
   resolved `FailureCategory` + reset evidence to the routing dispatch loop
   (`runner.go:520-593`), where `OnAgentFailed` is reachable. Without this, a
   `usage_limit` cannot trigger a bench.
3. **Add reset-deadline state + scope-keyed benching.** `EntryState`
   (`scheduler.go:8`) has no time field, and `OnAgentFailed` benches a *single*
   entry. Add a `BenchUntil *time.Time` (or equivalent) and a scope-keyed bench
   helper that benches every entry whose `QuotaScope(harness, model)` matches.
4. **Make `BenchUntil` survive recovery sync.** `syncRecoverySignals`
   (`route_runtime.go:247`) unconditionally unbenches any benched-but-*active*
   entry (`route_runtime.go:257-260`). Because `usage_limit` benches **without**
   touching resilience `AgentState` (it is not a freeze), the entry stays
   `StateActive` and this branch would unbench it on the very next `Next()` cycle,
   evaporating the quota bench. The `StateActive` recovery branch must **not**
   unbench an entry whose `BenchUntil` is still in the future; `BenchUntil` takes
   precedence over the resilience-driven unbench. Drive actual recovery off the
   deadline (`now >= BenchUntil`, then re-probe once). Persist the deadline via a
   new `agent_status.jsonl` event carrying `reset_at` + `quota_scope` (the store
   is the only persistence layer; scheduler `EntryState` is in-memory only).
5. **Wait out an all-benched lane instead of failing it.** When the scope-keyed
   bench takes down every entry in a lane, `Next()` returns "all exhausted" and
   `selectionWaitError` (`route_runtime.go:313`) currently derives a wait only
   from `StatePaused` entries (`:330`), so an all-benched-but-active lane falls
   through to `AllFrozen: true` and the relay-stall capture
   (`runner.go:433`) **fails the relay immediately**. `selectionWaitError` must
   derive a wait from the minimum pending `BenchUntil` across benched entries and
   treat "all benched with a future reset" as a wait, not a frozen lockout.
   (All-benched with *no* reset deadline ties into the Residual Open Question on
   fail-fast vs conservative-default wait.)

## Resume Prompt Follow-Up

When retrying/resuming after `incomplete_finalization` (and on operator
pause/resume of a laps-backed run), include the normal finalization instructions
plus a short resume snippet:

```text
Continue the current lap. If you need to double-check the scope, run `laps get`.
Finish with `laps done` when complete, or `laps handoff` if blocked.
```

## CLI Display And Records

- Footer and collapsed retry line show the accurate category, with reset/wait
  detail when known (`usage limit, resets in 123h50m`, `rate limit, waiting 2m`,
  `invalid model`).
- Try records and telemetry store the stable `Category` separately from the
  human display reason; `fail_reason` stays human-readable but machine consumers
  read `Category` / `FailureEvidence` fields, not the string.

## Coordination

- **enrich-failure-telemetry (next-up #2)** consumes this change's
  `FailureCategory`, `QuotaScope`, reset evidence, and the **benched** state
  rather than re-deriving its own `infra/agent/incomplete` trio. Its
  "agent state on failure" requirement is realigned onto this baseline.
- **improve-harness-consistency (next-up #3)** moves `FailureEvidence`
  *population* out of runner-side log parsing into the adapters, under a
  per-adapter conformance suite. This change deliberately leaves the fallback
  parser in place so #3 has a clear before/after.
- **build-new-tui** owns the ergonomic per-relay runner disablement that would
  retire the invalid-model-name workaround entirely.

## Candidate Work

- Add `FailureCategory` constants + display labels; extend `StrategyDecision`
  with `Category` and display label; add `FailureEvidence` to `TryResult`
  (`executor.go:44`), treated as optional (may be nil for `harness_launch`).
- Define the Category → `FailureClass` mapping (above) so `usage_limit` /
  `invalid_model` / `auth_or_proxy` do **not** map to `FailureInfra` and thus do
  not increment the freeze counter (`runner.go:1275-1276`).
- Reorder `ClassifyError` so executor evidence + provider/config/quota beats the
  dirty-tree `incomplete` pre-check (`ClassifyError` `patterns.go:237`, pre-check
  `:241`, pattern loop `:250`).
- Make patterns harness-scoped; thread `picked.Harness` into `ClassifyError`
  (`runner.go:1272`); strip harness names from generic display labels.
- Add parser helpers + tests for the signatures listed under Taxonomy.
- Extract a single shared dirty-path exclusion helper in `internal/gitx/git.go`
  (replacing the duplicated `.rally/`/`.laps/` skips at `git.go:82` and `:112`),
  consumed by all call sites; add `.claude/settings.local.json` to it.
- Add a standalone `QuotaScope(harness, model) string` resolver (antigravity
  label-substring family / opencode provider-prefix / direct-harness); keep the
  `Executor` interface unchanged.
- Break the attempt loop (`runner.go:1269-1301`) on first `usage_limit` /
  `auth_or_proxy` detection so they make exactly one attempt before benching.
- Extend `runOne`'s return contract (`runner.go:906`) to surface the resolved
  `FailureCategory` + reset evidence to the routing dispatch loop
  (`runner.go:520-593`).
- Add `BenchUntil` (or equivalent reset-deadline) + a scope-keyed bench helper to
  the scheduler (`scheduler.go:8`); guard the `StateActive` unbench in
  `syncRecoverySignals` (`route_runtime.go:257-260`) so a future `BenchUntil`
  is not undone, and unbench when the reset passes; persist the deadline via a
  new `agent_status.jsonl` event carrying `reset_at` + `quota_scope`.
- Teach `selectionWaitError` (`route_runtime.go:313`) to derive a wait from the
  minimum pending `BenchUntil` so an all-benched lane waits out the reset instead
  of failing as `AllFrozen` (`runner.go:433`).
- Begin executor population of `FailureEvidence` for harnesses we have
  signatures for.
- Tests for the three plumbing changes: (a) `usage_limit` makes exactly one
  attempt then benches the whole quota scope (not one-minute loops); (b) a
  benched-but-active entry is **not** unbenched before `BenchUntil`; (c) an
  all-benched lane with a future reset produces a wait, not an `AllFrozen` relay
  failure.
- Run-level regression tests from the concrete signatures (zero-change quota →
  `usage_limit`; quota + only `.claude/settings.local.json` → `usage_limit`;
  Codex prose "rate-limit" → not a Claude rate limit; invalid model + settings
  dirty → `invalid_model`; real task file dirty, no finalize →
  `incomplete_finalization`).
- Resume-prompt finalization coverage for incomplete retries.

## Residual Open Questions

- Exact full set of harness-local paths safe to exclude beyond
  `.claude/settings.local.json` (start conservative, grow with evidence).
- When *every* entry in a lane is `invalid_model`/benched, fail the relay
  immediately vs. surface an operator prompt where a TTY is available.
- Whether `usage_limit` bench state should persist across relays on the same
  machine (a still-unreset quota is unreset for the next relay too) or reset per
  relay. Leaning persist-with-reset-time via a new `agent_status.jsonl` resilience
  event carrying `reset_at` + `quota_scope` (reuses the existing store; scheduler
  `EntryState` is in-memory only and cannot persist on its own). **This is the
  decision most worth settling before implementation**, since it determines
  whether `QuotaScope` needs a persisted home.
- Antigravity label variance (model versions, `(High)`/`(Low)` suffixes) may
  outgrow substring matching and need a maintained label→family table.

## Acceptance Direction

- The observed examples classify accurately.
- Retry waits align with parsed reset/retry evidence; usage limits bench instead
  of looping.
- No Codex run can display `claude rate-limit interrupt`.
- Harness-local dirty files never mask provider/config errors as incomplete.
- Genuine incomplete finalization still resumes and carries finalization guidance.
- Try records and telemetry expose both stable `Category` and human display reason.
