## Why

The per-harness reliability parsers in `internal/reliability/` are correct for
the shapes they recognise, but the integration around them is lopsided. New
Relic data (30-day window, 77 `RallyFailure` / 310 `RallyTry` events for app
`Rally CLI`) shows the parsers populate `failure_evidence` on only 3 of 77
captured failures; the other 78% carry a correct category but no diagnostic
context. Codex's silent exit-1 burst (the 0.11.2 spike, 10 failures in one
relay) leaves no in-band signal because codex writes nothing to stdout/stderr
before dying — yet codex keeps rich session logs on disk we never read. The
`runner` telemetry tag drops its model component whenever a route uses a bare
alias, hiding per-model signal exactly when it matters most. 39
`RallyTry.outcome IS NULL` events are routing decisions mis-emitted as try
events. And the upstream `gemini` CLI is now deprecated — its own auth error
tells operators to "migrate to the Antigravity suite of products".

The 0.12.0 release is the right moment to cut `gemini` and tighten the
adapter contract around the gaps the data exposes.

## What Changes

- **BREAKING**: Remove the deprecated `gemini` CLI harness entirely.
  `antigravity` becomes the only Google-owned harness. The `ge`/`gemini`
  aliases fail to resolve with a one-time warning pointing operators at
  `antigravity`; there is no transitional alias mapping.
- Add a codex session-log fallback so a codex exit-1 with no in-band output
  is enriched from `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`, reading
  only structural events (`session_meta`, the last `event_msg`) and
  skipping the verbose `token_count` / `response_item` / `base_instructions`
  payloads. Codex silent-exit-with-no-session-log populates
  `FailureEvidence{Category: harness_launch, Source: "codex_no_session_log"}`
  at the executor level, so `ClassifyError` Priority 1 picks it up directly.
  The win is the correct category label and repro marker (and the session-log
  tail when available) instead of an uncategorised `agent_error` — the
  existing `harness_launch` semantics (FreshRestart within budget, infra-class
  freeze pressure after 2+ failures) apply unchanged, so the runner keeps
  retrying codex launch failures up to the budget and the freeze cascade
  caps it when codex repeatedly fails to launch.
- Extend opencode's existing disk-log fallback so a try that hits the
  runner-side try-budget cap with no parseable stdout still surfaces a
  bounded `WARN|ERROR` plus structural `created`/`loop`/`stream` tail
  from `~/.local/share/opencode/log/opencode.log`, correlated by opencode
  session id.
- Add a `ResolvedModel` field to `TryResult`. Executors populate it with
  the model actually passed to the CLI (the executor's own default when
  the route is a bare alias). The `runner` telemetry tag uses it whenever
  non-empty, so per-model NRQL queries no longer collapse on bare-alias
  routes.
- Populate `failure_evidence` on every categorised `RallyFailure`. Extend
  `StrategyDecision` with an optional `*FailureEvidence` so classification
  via Priority 3 (dirty-tree incomplete), Priority 4 (text patterns), and
  Priority 5 (default `agent_error`) attaches the same `RawSignal` /
  `Message` / `Source` fields Priority 1 already populates. New source
  values: `dirty_tree`, `text_pattern`, `unmatched`, `codex_session_log`,
  `opencode_disk_log`.
- Add a runner-side try-budget-exhaustion classification: when a try times
  out with no Evidence, the recorded category / fail-reason distinguishes
  "we killed it on budget" from a real harness crash, without changing the
  resilience cascade (still agent-class, does-not-freeze).
- Stop polluting `RallyTry` with non-try routing events. Add a
  `RallyRoute` custom event for route-fallback decisions (today they are
  mis-emitted as `RallyTry` with `outcome IS NULL`, polluting every
  failure-rate query). Every remaining `RallyTry` emit path SHALL set a
  non-empty `outcome`.

## Capabilities

### New Capabilities

None. All changes modify existing capabilities.

### Modified Capabilities

- `executor`: cut `GeminiExecutor`; add `ResolvedModel` field to
  `TryResult`; codex executor reads its session-log on silent exit;
  opencode executor's disk-log fallback covers try-budget exhaustion.
- `relay-runner`: `ClassifyError` populates `Evidence` on every
  classification priority; runner-side try-budget-exhaustion
  classification; runner uses `result.ResolvedModel` for the `runner`
  telemetry tag.
- `telemetry`: `failure_evidence` context block populated for 100% of
  categorised failures (not just the executor-evidence path); add
  `RallyRoute` custom event for routing decisions; every `RallyTry`
  event carries a non-empty `outcome`.
- `cli-config`: drop the `gemini` model default, the `ge`/`gemini` aliases,
  and `[harness.ge.models]` from the configuration surface; emit a
  one-time warning when an operator route resolves to the removed alias.

## Impact

- **Code**:
  - `internal/agent/{gemini.go}` deleted; `internal/agent/{codex,opencode}.go`
    extended with session/disk-log fallbacks and `ResolvedModel` population.
  - `internal/reliability/{patterns.go, antigravity.go (renamed parser)}`:
    `StrategyDecision.Evidence`, removed gemini-only patterns, parser rename.
  - `internal/relay/{runner.go (multiple sites), route_runtime.go, mix.go}`:
    alias removal, classification plumbing, runner-tag resolution,
    try-budget classification, routing-event split.
  - `internal/telemetry/{sink.go, newrelic.go, attributes.go}`:
    `EmitRouteEvent` + `RallyRoute` event; `failure_evidence` plumbing.
  - `internal/config/{config_v2.go, providers.go}` and
    `internal/cli/{config.go, routes_check.go}`: gemini removal.
  - `cmd/rally/{main.go, init_roles.go}`: gemini removal.
- **Behaviour**: silent harness failures (codex exit-1, opencode budget
  exhaustion) get a structured category and a bounded diagnostic signal
  instead of a generic `agent_error`; routing decisions stop inflating
  `RallyTry` counts; bare-alias routes no longer erase the model from
  telemetry.
- **Migration**: operators with `[harness.ge.models]`, `gemini_model`, or
  `routes x = ["ge:…"]` blocks see a one-time warning on the first relay
  after upgrade. The config keys are silently ignored (not rejected), so
  malformed configs do not block startup.
- **Out of scope**: the antigravity-via-Claude routing policy (a separate
  routing-policy question); the larger adapter conformance suite and
  capability matrix (deferred past 0.12.0); rewriting the resilience
  cascade or freeze counter.
