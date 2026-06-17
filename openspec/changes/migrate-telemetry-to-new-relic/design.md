## Context

Rally 0.9.0 sends release telemetry to Sentry through `internal/telemetry.Sink`, with a baked `DefaultSentryDSN` injected by GoReleaser and a global `RALLY_TELEMETRY=0` kill switch. The sink boundary already has the right shape for a backend swap: relay/run/try spans, structured try logs, operator-worthy failures, low-severity diagnostic events, flush, tag/context separation, and PII scrubbing.

The operational problem is Sentry capacity: Rally has exhausted the useful free-tier budget just before the 0.10.0 reliability/model-routing work needs more field data. New Relic's public pricing currently advertises 100 GB/month free ingest. New Relic's Event API accepts custom events over HTTPS with an account id and ingest license key, which lets Rally send only explicitly constructed telemetry payloads.

## Goals / Non-Goals

**Goals:**
- Ship the provider migration as 0.9.1 before 0.10.0.
- Keep relay code and telemetry call sites stable by implementing a New Relic-backed `telemetry.Sink`.
- Preserve the privacy contract: no prompts, transcripts, hostnames, usernames, IP addresses, raw paths with home prefixes, or unbounded strings.
- Preserve the activation contract: source builds remain silent unless configured, release binaries activate only for relay-running commands, and `RALLY_TELEMETRY=0` prevents network calls and machine-id writes.
- Move release secret wiring from `RALLY_SENTRY_DSN` to New Relic ingest credentials.
- Keep user-provided Sentry config as a one-release explicit fallback only when no New Relic credentials exist, with a deprecation warning.

**Non-Goals:**
- No dashboard/alert-as-code provisioning in this change.
- No attempt to migrate historical Sentry data.
- No telemetry volume autosampler driven by New Relic billing APIs.
- No New Relic APM agent instrumentation in 0.9.1.
- No 0.10.0 feature implementation; only update 0.10.0 plans so they build on New Relic telemetry.

## Decisions

### 1. Use New Relic Event API, not the Go APM agent

Implement `internal/telemetry/newrelic.go` as a direct Event API sink using the standard library HTTP client (or a very small local client wrapper), not `github.com/newrelic/go-agent/v3/newrelic`.

Rationale:
- The New Relic Go APM agent has default collection surfaces outside Rally's scrubber: application log forwarding, runtime sampling, code-level metrics, span events, and a connect payload containing host identity. That conflicts with Rally's established "only intentional structured telemetry" and "no host identity" guarantees.
- The Event API lets Rally send only the payloads built by `internal/telemetry`, after existing scrubbing/attribute limiting has run.
- Rally does not need APM auto-instrumentation for 0.9.1; its existing sink already knows the relay/run/try lifecycle and can emit queryable custom events.

Mapping:
- `StartSpan` returns a local in-memory span with start time, generated span id, optional parent id, operation, description, tags, and data.
- `Span.Finish` emits a `RallySpan` custom event with duration, operation, description, span id, parent span id, and scrubbed attributes.
- `EmitTryLog` emits a `RallyTry` custom event.
- `CaptureEvent` emits a `RallyDiagnostic` custom event, using the event level as an attribute.
- `CaptureFailure` emits a `RallyFailure` custom event with `operator_worthy=true`, stable fingerprint/grouping attributes, and the same scrubbed tags/context fields that Sentry received.
- The sink batches events in memory and flushes them with bounded HTTPS POSTs to the Event API.

Alternatives considered:
- **New Relic Go APM agent**: easier transactions/custom events/errors, but it transmits agent-managed metadata that is hard to prove compatible with Rally's privacy contract.
- **OTLP exporter directly to New Relic**: good future path for traces/metrics, but the Event API is simpler for 0.9.1 and maps better to Rally's custom-event-shaped telemetry.
- **Keep Sentry and lower event volume**: avoids dependency churn but does not solve the free-tier exhaustion enough for 0.10.0 observability.

### 2. Keep `Sink` as the stable boundary and neutralize Sentry-specific naming

Do not push New Relic concepts into `internal/relay`. Keep `Sink`, `Span`, `FailureEvent`, and `Event` as the relay-facing API, but update comments that currently say "Sentry Issue" to backend-neutral wording ("operator-worthy failure").

Implementation guidance:
- Retain `SentrySink` for exactly one compatibility window as an explicit fallback when only `SENTRY_DSN` or `[telemetry] sentry_dsn` is configured.
- New Relic credentials always take precedence over legacy Sentry config, including baked release credentials.
- Add a deprecation warning for Sentry fallback usage.
- Extract scrubbing into backend-neutral helpers that sanitize maps/attributes before either backend emits data.

### 3. New activation precedence and secret handling

Telemetry activation SHALL resolve in this order:
1. `RALLY_TELEMETRY=0` disables all telemetry.
2. `NEW_RELIC_LICENSE_KEY` plus `NEW_RELIC_ACCOUNT_ID` enable New Relic and override baked/default values.
3. baked `DefaultNewRelicLicenseKey` plus `DefaultNewRelicAccountID` enable release telemetry.
4. legacy `SENTRY_DSN` / `[telemetry] sentry_dsn` fallback is honored only when no New Relic credential pair exists.
5. no complete credential pair disables telemetry.

Tracked config SHALL NOT contain New Relic license keys or account ids. `.rally/config.toml` may contain only non-secret New Relic metadata such as app name, region/endpoint selection, and environment label. This avoids committing ingest credentials through Rally's normal path-scoped setup commits and user-authored config commits.

Environment/config metadata:
- app name: `NEW_RELIC_APP_NAME`, `[telemetry] new_relic_app_name`, or default `"Rally CLI"`
- region: `NEW_RELIC_REGION`, `[telemetry] new_relic_region`, default `us`
- event endpoint override for tests/advanced use: `NEW_RELIC_EVENT_ENDPOINT`, not generated into config by default

### 4. Event API batching, limits, and cost guardrails

New Relic's larger allowance is not a license to emit unbounded data.

Rules:
- Keep `EmitTryLog` as one custom event per persisted try, not per transcript line.
- Keep `Span.Finish` as one custom event per relay/run/try span; no child events for prompt lines or agent output.
- Keep `CaptureEvent` for low-severity limit/fallback/mismatch diagnostics only.
- Keep `CaptureFailure` only for operator-worthy failures and `needs_user`.
- Convert nested contexts into bounded flattened attributes with stable prefixes such as `rally.version`, `failure_evidence.message`, and `failure_evidence.raw_signal`.
- Drop sensitive keys entirely for New Relic-bound payloads rather than shipping placeholder values such as `[scrubbed]`; do not emit attributes named `prompt`, `transcript`, `log`, `hostname`, or equivalent even with redacted values.
- Drop or hash attributes that cannot be represented as string/number after scrubbing; do not JSON-encode large nested maps into a single attribute.
- Enforce Event API constraints plus a stricter Rally-local budget in code: event type is present and valid, attributes are strings/numbers only, attributes are capped to 64 per event, keys are bounded, and request payloads are size bounded.

### 5. Privacy and identity semantics are unchanged

Keep `machine-id` behavior: generate only after telemetry is active, store under `data_dir`, and use `machine_id_prefix` plus `relay_guid` for New Relic correlation. Unlike the old Sentry context path, the New Relic Event API sink SHALL NOT emit the full anonymous `machine_id`, because flattened Event API attributes are queryable and higher-cardinality than the previous context-only placement. Continue to collapse home paths and scrub prompt/transcript-looking keys before backend-specific conversion.

Because the Event API sink is direct HTTP, Rally SHALL NOT collect or transmit host/runtime/log metadata unless Rally itself explicitly adds a scrubbed field. Do not use New Relic APM/logging agents in this change.

### 6. Cross-change alignment with 0.10.0

The active 0.10.0 plan changes lap-pin mismatches from operator errors to warning diagnostics. This 0.9.1 migration SHALL not cement the old Sentry behavior. Represent `wrong_lap_consumed` and `multi_lap_consumed` as `RallyDiagnostic` events with `level=warning`, `event_kind=lap_pin_mismatch`, and `mismatch_reason`, not as `RallyFailure` / operator-worthy errors by default. Add `telemetry.LevelWarning` so both New Relic and the one-release Sentry fallback can represent warning severity without ad hoc strings.

### 7. Release wiring and docs

Replace release-time Sentry injection:
- `main.DefaultSentryDSN` remains only for legacy fallback if retained.
- add `main.DefaultNewRelicLicenseKey` and `main.DefaultNewRelicAccountID`
- `.goreleaser.yaml` ldflags use `RALLY_NEW_RELIC_LICENSE_KEY` and `RALLY_NEW_RELIC_ACCOUNT_ID`
- `.github/workflows/release.yml` fails before GoReleaser if either New Relic release secret is empty for a non-existing release
- README and `AGENTS.md` observability guidance describe New Relic, opt-out, config/env precedence, data sent, privacy guarantees, and legacy Sentry fallback/deprecation.

Versioning:
- Bump `internal/buildinfo/VERSION` to `0.9.1` only after the release workflow secret gate is in place.
- Leave `main.Version = "dev"` for GoReleaser injection.
- Do not create tags manually.

## Risks / Trade-offs

- **Risk: Event API custom events are less APM-native than New Relic transactions/errors** -> model relay/run/try spans as `RallySpan` events with parent ids and durations; use NRQL dashboards/alerts rather than APM transaction views for 0.9.1.
- **Risk: Event API requires both account id and license key** -> require both via env or baked release ldflags; no partial config initializes telemetry.
- **Risk: version bump ships before secrets exist** -> add a release workflow check for `RALLY_NEW_RELIC_LICENSE_KEY` and `RALLY_NEW_RELIC_ACCOUNT_ID`, and keep version bump as the final implementation step.
- **Risk: New Relic event attribute limits drop important context** -> define deterministic flattening/attribute budget order, prioritizing correlation tags, outcome/category, recovery classification, and bounded failure evidence.
- **Risk: stale Sentry config keeps spending Sentry quota** -> New Relic release defaults have higher precedence than legacy Sentry config; Sentry-only fallback warns.
- **Risk: privacy regression during backend conversion** -> keep scrubbing backend-neutral and test the exact Event API request body, not only internal maps.
- **Risk: ingest volume still grows too quickly** -> keep one try event per try, one span event per logical span, no automatic logs, no transcript/log forwarding, and tests that no prompt/output/log fields appear in request bodies.

## Migration Plan

1. Implement `NewRelicEventSink` and backend-neutral scrubbing/attribute flattening while keeping `NoopSink` behavior unchanged.
2. Update config/env/ldflag resolution to prefer a complete New Relic credential pair and deprecate Sentry fallback.
3. Update tests for activation precedence, no-side-effect mechanical commands, privacy scrubbing, flush, failure/event mapping, and release secret gating.
4. Update README and `AGENTS.md`.
5. Add release workflow secret gate.
6. Bump version to `0.9.1`.
7. Configure GitHub secrets `RALLY_NEW_RELIC_LICENSE_KEY` and `RALLY_NEW_RELIC_ACCOUNT_ID` before merging/releasing.

Rollback:
- Set `RALLY_TELEMETRY=0` to disable all telemetry immediately.
- If release wiring fails, remove New Relic secrets or ship a patch with empty baked credentials; source builds remain opt-in.
- If New Relic event ingestion fails, the unchanged `Sink` boundary allows reverting to no-op or legacy Sentry fallback for a patch release.

## Research Notes

- New Relic pricing page currently states the free tier includes 100 GB/month of data ingest and $0.40/GB beyond the free 100 GB.
- New Relic Event API docs state custom events are sent over HTTPS to account-specific endpoints using an ingest license key and are queryable with NRQL.
- New Relic custom event docs require simple scalar attributes and warn about custom event limits/reserved terms.
- New Relic Go APM agent docs and code show useful APIs, but the agent default config includes collection surfaces and host metadata outside Rally's scrubber, so it is not the 0.9.1 implementation path.
