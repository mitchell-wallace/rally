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
- Add a **unified disk-log fallback** for all four harnesses (claude,
  codex, opencode, antigravity) so a silent exit-1 or budget-killed try
  with no in-band output is enriched from the harness's own session or
  server log. Each fallback reads only structural events, skips
  prompt/credential/user-content payloads, and produces a bounded (256
  runes) `RawSignal` with a `failure_evidence.source` marker
  (`codex_session_log`, `claude_session_log`, `opencode_disk_log`,
  `antigravity_glog`, `codex_no_session_log`). The existing opencode
  fallback machinery is the reference implementation.
- Codex silent-exit-with-no-session-log populates
  `FailureEvidence{Category: harness_launch, Source: "codex_no_session_log"}`
  at the executor level, so `ClassifyError` Priority 1 picks it up directly.
  The existing `harness_launch` retry semantics (FreshRestart within budget,
  infra-class freeze pressure after 2+ failures) apply unchanged.
- Add a **`CategoryUnidentifiedIssue`** (`"unidentified_issue"`) as the
  new default failure category when no known pattern matches. Repurpose
  `CategoryAgentError` to be reserved for failures where a specific
  agent-level error was extracted (from stdout/stderr by a text pattern,
  or from a harness disk log that shows an agent-internal fault). Every
  failure SHALL carry a non-empty `failure_category` — `unidentified_issue`
  is the floor, never nil/empty.
- Add a `ResolvedModel` field to `TryResult`. Executors populate it with
  the model actually passed to the CLI (the executor's own default when
  the route is a bare alias). The `runner` telemetry tag uses it whenever
  non-empty, so per-model NRQL queries no longer collapse on bare-alias
  routes.
- Populate `failure_evidence` on every categorised `RallyFailure`. Extend
  `StrategyDecision` with an optional `*FailureEvidence` so classification
  via Priority 3 (dirty-tree incomplete), Priority 4 (text patterns), and
  Priority 5 (default `unidentified_issue`) attaches the same `RawSignal` /
  `Message` / `Source` fields Priority 1 already populates. New source
  values: `dirty_tree`, `text_pattern`, `unmatched`, `codex_session_log`,
  `claude_session_log`, `opencode_disk_log`, `antigravity_glog`,
  `codex_no_session_log`.
- Add a runner-side try-budget-exhaustion classification: when a try
  times out on the try-cap or run-budget and no executor Evidence
  produced a Category, set `failureCategory = unidentified_issue` with
  `failReason = "try budget exhausted; no output"` (try-cap) or
  `failReason = "run timeout"` (run-budget). `FailureClass` stays
  agent-class (does-not-freeze). When executor Evidence DID produce a
  Category (e.g. a real API timeout signal or a disk-log fallback
  yielding `harness_launch`), the executor's category is authoritative.
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
  `TryResult`; unified disk-log fallback for all four harnesses (codex
  reads its session log on silent exit; claude reads project session
  JSONL; antigravity reads glog; opencode extends existing disk-log
  fallback to also cover WARN/ERROR/structural lines on budget-killed
  tries).
- `relay-runner`: `ClassifyError` populates `Evidence` on every
  classification priority (Priority 5 defaults to `unidentified_issue`);
  runner-side try-budget and run-budget classification uses
  `unidentified_issue` (never empty); runner uses
  `result.ResolvedModel` for the `runner` telemetry tag.
- `telemetry`: `failure_evidence` context block populated for 100% of
  categorised failures (not just the executor-evidence path); add
  `RallyRoute` custom event for routing decisions; every `RallyTry`
  event carries a non-empty `outcome`.
- `cli-config`: drop the `gemini` model default, the `ge`/`gemini` aliases,
  and `[harness.ge.models]` from the configuration surface; emit a
  one-time warning when an operator route resolves to the removed alias.

## Impact

- **Code**:
  - `internal/agent/{gemini.go}` deleted; `internal/agent/{codex,claude,opencode,antigravity}.go`
    extended with session/disk-log fallbacks and `ResolvedModel` population.
  - `internal/reliability/{patterns.go, antigravity.go (renamed parser), category.go (new CategoryUnidentifiedIssue)}`:
    `StrategyDecision.Evidence`, removed gemini-only patterns, parser rename,
    `unidentified_issue` category, stricter `agent_error` semantics.
  - `internal/relay/{runner.go (multiple sites), route_runtime.go, mix.go}`:
    alias removal, classification plumbing, runner-tag resolution,
    try-budget/run-budget classification with `unidentified_issue`,
    routing-event split.
  - `internal/telemetry/{sink.go, newrelic.go, noop.go, attributes.go}`:
    `EmitRouteEvent` + `RallyRoute` event; `failure_evidence` plumbing.
  - `internal/config/{config_v2.go, providers.go}` and
    `internal/cli/{config.go, routes_check.go}`: gemini removal.
  - `cmd/rally/{main.go, init_roles.go}`: gemini removal.
- **Behaviour**: silent harness failures (codex exit-1, claude/opencode/antigravity
  budget exhaustion) get a structured category and a bounded diagnostic signal
  from the harness's own disk log; every failure carries a non-empty
  `failure_category` (the floor is `unidentified_issue`); `agent_error` is
  now reserved for specific extracted errors; routing decisions stop inflating
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
