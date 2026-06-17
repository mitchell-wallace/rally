## Context

Rally 0.9.0 sends release telemetry to Sentry through `internal/telemetry.Sink`, with a baked `DefaultSentryDSN` injected by GoReleaser and a global `RALLY_TELEMETRY=0` kill switch. The sink boundary already has the right shape for a backend swap: relay/run/try spans, structured try logs, operator-worthy failures, low-severity diagnostic events, flush, tag/context separation, and PII scrubbing.

The operational problem is Sentry capacity: Rally has exhausted the useful free-tier budget just before the 0.10.0 reliability/model-routing work needs more field data. New Relic's public pricing currently advertises 100 GB/month free ingest, and its Go APM agent gives Rally richer transactions, segments, errors, runtime data, and custom events than a minimal custom HTTP sink.

The product posture is intentionally pragmatic. Rally should avoid shipping obvious personal information from Rally-controlled payloads, but this is not a high-privacy product. The preferred 0.9.1 outcome is useful New Relic APM data with a clear config/env opt-out, not a severely minimized telemetry feed.

## Goals / Non-Goals

**Goals:**
- Ship the provider migration as 0.9.1 before 0.10.0.
- Hard-cut Sentry: no release fallback, no Sentry SDK dependency, no Sentry DSN config path, and no Sentry release secret.
- Keep relay code and telemetry call sites stable by implementing a New Relic-backed `telemetry.Sink`.
- Use the New Relic Go APM agent as the backend implementation.
- Preserve the activation contract: source builds remain silent unless configured, release binaries activate only for relay-running commands, `RALLY_TELEMETRY=0` prevents telemetry and machine-id writes, and `[telemetry] enabled = false` disables telemetry without rebuilding.
- Keep best-effort Rally-supplied PII protections: no prompts, transcripts, raw command output, usernames in home paths, or Rally-added hostname/username tags in Rally-controlled telemetry payloads or New Relic log records.
- Move release secret wiring from `RALLY_SENTRY_DSN` to a New Relic ingest license key.
- Update 0.10.0 plans so they build on New Relic telemetry and do not reintroduce Sentry semantics.

**Non-Goals:**
- No dashboard/alert-as-code provisioning in this change.
- No attempt to migrate historical Sentry data.
- No telemetry volume autosampler driven by New Relic billing APIs.
- No attempt to suppress every New Relic agent-native host/runtime/APM metadata field. Rally will avoid adding obvious personal identifiers itself and will set safer agent options where available.
- No 0.10.0 feature implementation; only update 0.10.0 plans so they build on New Relic telemetry.

## Decisions

### 1. Use the New Relic Go APM agent behind `telemetry.Sink`

Implement `internal/telemetry/newrelic.go` with `github.com/newrelic/go-agent/v3/newrelic`, not a hand-built Event API-only sink.

Rationale:
- Rally needs more useful data now, and the Go APM agent provides transactions, segments, error collection, runtime sampling, and custom events with less custom transport code.
- The existing `telemetry.Sink` boundary still prevents New Relic concepts from leaking into relay orchestration.
- The user-facing privacy control should be opt-out at runtime/config time, not a rebuild requirement.

Mapping:
- `StartSpan` creates or attaches to a New Relic transaction for root relay spans and creates `newrelic.Segment` values for child run/try spans.
- `Span.Finish` ends the associated transaction or segment and attaches bounded, scrubbed Rally attributes such as relay/run/try ids, role, runner, outcome, duration, and lifecycle classification.
- `EmitTryLog` records one `RallyTry` custom event per persisted try via `Application.RecordCustomEvent`; existing relay call sites that emit before `store.AppendTry` must move emission after successful persistence.
- `CaptureEvent` records a `RallyDiagnostic` custom event with a scalar `level`; warning diagnostics use first-class `telemetry.LevelWarning`.
- `CaptureFailure` calls `Transaction.NoticeError(newrelic.Error{...})` on the active transaction with bounded attributes, including an error `Class` derived from stable failure categories, and MAY also record a `RallyFailure` custom event for queryability and continuity with existing reporting.
- Panic visibility from the obsolete `enrich-sentry-coverage` draft moves into New Relic: configure `cfg.ErrorCollector.RecordPanics = true`, but do not rely on that setting alone. Because Rally hides transaction ending behind the `Span.Finish` abstraction, implement a panic-aware New Relic transaction finish/recovery path that calls `recover()`, records `newrelic.Error` with `newrelic.NewStackTrace()` and bounded attributes, ends the transaction, and re-panics so existing process semantics are preserved. The existing string-based panic classification remains only a fallback for agent-output text.
- `Flush` calls bounded New Relic shutdown/wait APIs so exit is not held hostage by network failure.

Alternatives considered:
- **Direct New Relic Event API**: gives tighter control over payload shape, but throws away the richer APM/runtime data the migration is meant to unlock.
- **OTLP exporter directly to New Relic**: a possible future path for traces/metrics, but the Go APM agent is the documented, lowest-friction 0.9.1 implementation.
- **Keep Sentry and lower event volume**: avoids dependency churn but does not solve the free-tier exhaustion enough for 0.10.0 observability.

### 2. Remove Sentry, keep `Sink` as the stable boundary

Do not push New Relic concepts into `internal/relay`. Keep `Sink`, `Span`, `FailureEvent`, and `Event` as the relay-facing API, but update comments that currently say "Sentry Issue" to backend-neutral wording ("operator-worthy failure").

Implementation guidance:
- Delete `internal/telemetry/sentry.go`, Sentry-specific tests, and the `github.com/getsentry/sentry-go` dependency.
- Remove `DefaultSentryDSN`, `SENTRY_DSN`, `[telemetry] sentry_dsn`, Sentry fallback/deprecation branches, and Sentry release wiring.
- Extract or preserve scrubbing as backend-neutral helpers that sanitize Rally-supplied maps/attributes before New Relic receives them.
- Tests should fail if a Sentry SDK import, DSN config path, GoReleaser Sentry ldflag, release workflow `RALLY_SENTRY_DSN`, or normative Sentry fallback docs remain in the 0.9.1 path.

### 3. Activation precedence, config opt-out, and secret handling

Telemetry activation SHALL resolve in this order:
1. `RALLY_TELEMETRY=0` disables all telemetry.
2. `[telemetry] enabled = false` disables all telemetry.
3. `NEW_RELIC_LICENSE_KEY` enables New Relic for source builds or local override.
4. baked `DefaultNewRelicLicenseKey` enables New Relic release telemetry.
5. no license key disables telemetry.

Tracked config SHALL NOT contain New Relic license keys. `.rally/config.toml` may contain non-secret New Relic metadata and the opt-out flag:
- `enabled = false` disables telemetry without a rebuild.
- `new_relic_app_name` overrides the default app name.
- `new_relic_host_display_name` MAY override the non-identifying display host name if needed, but generated config should default to a generic value or omit the key.

Environment/config metadata:
- app name: `NEW_RELIC_APP_NAME`, `[telemetry] new_relic_app_name`, or default `"Rally CLI"`
- optional host display name: New Relic's standard `NEW_RELIC_PROCESS_HOST_DISPLAY_NAME`, `[telemetry] new_relic_host_display_name`, or default `"rally-cli"`; do not introduce a Rally-specific host-display env alias unless a later product decision asks for one.
- optional New Relic host/collector override for tests/advanced use: standard New Relic agent environment/config where supported, not generated into config by default

### 4. Agent configuration and volume guardrails

New Relic's larger allowance is not a license to emit unbounded Rally data.

Rules:
- Keep `EmitTryLog` as one custom event per persisted try, not per transcript line.
- Keep relay/run/try timing as one logical transaction/segment hierarchy per relay execution.
- Keep `CaptureEvent` for low-severity limit/fallback/mismatch diagnostics only.
- Keep `CaptureFailure` only for operator-worthy failures and `needs_user`.
- Convert Rally-supplied nested contexts into bounded scalar attributes with stable prefixes such as `rally.version`, `failure_evidence.message`, and `failure_evidence.raw_signal`.
- Drop sensitive Rally-supplied keys entirely rather than shipping placeholder values such as `[scrubbed]`; do not emit custom attributes named `prompt`, `transcript`, `log`, `hostname`, or equivalent even with redacted values.
- Drop or hash attributes that cannot be represented as string/number after scrubbing; do not JSON-encode large nested maps into a single custom attribute.
- Enforce a Rally-local custom attribute/event budget in code: event type is fixed, attributes are strings/numbers/bools only, attribute counts are capped, keys are bounded, and custom event payloads are size bounded.
- Keep New Relic application logging enabled intentionally. Apply Rally's explicit logging choices after `ConfigFromEnvironment`: `ConfigAppLogEnabled(true)`, `ConfigAppLogForwardingEnabled(true)`, `ConfigAppLogMetricsEnabled(true)`, and a bounded `ConfigAppLogForwardingMaxSamplesStored(...)`; keep local decorating off unless it proves useful. For 0.9.1, enable the agent app-log capability but do not add new `Application.RecordLog` calls or logger integrations; `RallyTry` custom events remain the per-try observability stream. If a later change adds a sanitized lifecycle log stream, it must define a schema and tests before emitting logs.
- Keep agent runtime sampling, distributed tracing, custom insights events, and error collection enabled unless tests show they create unacceptable overhead.

### 5. Privacy and identity semantics

Keep `machine-id` behavior: generate only after telemetry is active and store under `data_dir`. Rally MAY attach the full anonymous machine id or a stable prefix as a custom attribute when useful for correlation; it MUST NOT attach the OS username, home directory username, raw hostname, prompt text, transcript text, or command log text as Rally-supplied attributes.

Best-effort agent configuration:
- set a generic `HostDisplayName` such as `"rally-cli"` where the New Relic agent supports it;
- avoid adding custom host/user/network attributes;
- collapse home paths in Rally-supplied working-directory fields and free-text provider signals before attaching them to transactions, events, or errors;
- enable New Relic application log forwarding intentionally, but avoid adding Rally log records for prompts, transcripts, raw agent output, or raw command output;
- accept that New Relic's Go agent may still send native runtime/APM/connect metadata as part of normal operation.

### 6. Cross-change alignment with 0.10.0

The active 0.10.0 plan changes lap-pin mismatches from operator errors to warning diagnostics. This 0.9.1 migration SHALL not cement the old Sentry behavior. Represent `wrong_lap_consumed` and `multi_lap_consumed` as `RallyDiagnostic` custom events or New Relic diagnostic attributes with `level=warning`, `event_kind=lap_pin_mismatch`, and `mismatch_reason`, not as `RallyFailure` / operator-worthy errors by default. Add `telemetry.LevelWarning` so New Relic receives warning diagnostics without ad hoc strings.

### 7. Release wiring and docs

Replace release-time Sentry injection:
- remove `main.DefaultSentryDSN`
- add `main.DefaultNewRelicLicenseKey`
- `.goreleaser.yaml` ldflags use `RALLY_NEW_RELIC_LICENSE_KEY`
- `.github/workflows/release.yml` fails before GoReleaser if the New Relic release secret is empty for a non-existing release
- README and `AGENTS.md` observability guidance describe New Relic, opt-out, config/env precedence, data sent, and privacy posture.

Versioning:
- Bump `internal/buildinfo/VERSION` to `0.9.1` only after the release workflow secret gate is in place.
- Leave `main.Version = "dev"` for GoReleaser injection.
- Do not create tags manually.

## Risks / Trade-offs

- **Risk: New Relic agent sends more native metadata than the old hand-curated Sentry payloads** -> document this as an intentional product tradeoff, enable application logging knowingly, set a generic host display name where possible, and keep Rally-supplied attributes scrubbed.
- **Risk: app log forwarding captures sensitive local output** -> keep application logging enabled by product decision, but in 0.9.1 do not add New Relic `RecordLog` calls or logger integrations; custom events carry Rally's structured lifecycle data until a later sanitized log schema is specified.
- **Risk: environment configuration changes New Relic application logging shape** -> apply Rally's explicit logging options after `ConfigFromEnvironment` so release telemetry has predictable forwarding, metrics, decorating, and sample-limit behavior.
- **Risk: Go panics remain string-only telemetry after the Sentry enrichment draft is abandoned** -> carry the draft's native exception-capture intent into New Relic using `newrelic.Error`, `newrelic.NewStackTrace()`, transaction-scoped `NoticeError`, and a panic-aware finish/recover path rather than relying only on `ErrorCollector.RecordPanics`.
- **Risk: version bump ships before secrets exist** -> add a release workflow check for `RALLY_NEW_RELIC_LICENSE_KEY`, and keep version bump as the final implementation step.
- **Risk: New Relic attribute limits drop important context** -> define deterministic flattening/attribute budget order, prioritizing correlation tags, outcome/category, recovery classification, and bounded failure evidence.
- **Risk: stale Sentry config silently keeps spending Sentry quota** -> remove Sentry fallback and ignore/remove Sentry config paths for 0.9.1.
- **Risk: ingest volume still grows too quickly** -> keep one try event per persisted try, bounded logical spans/segments, bounded app-log samples, and tests that no prompt/transcript/raw-output fields appear in Rally-supplied attributes or log records.

## Migration Plan

1. Remove Sentry implementation, dependency, config, release wiring, and docs.
2. Add the New Relic Go APM agent dependency and implement `NewRelicSink` behind the existing `Sink` interface.
3. Update config/env/ldflag resolution for `RALLY_TELEMETRY=0`, `[telemetry] enabled = false`, env license key, baked license key, and no-op fallback.
4. Configure the agent with app name, generic host display name, explicit application logging choices after environment config, bounded shutdown, custom events, native panic/error capture, and runtime/APM defaults.
5. Update tests for activation precedence, config opt-out, no-side-effect mechanical commands, privacy scrubbing, flush, failure/event mapping, and release secret gating.
6. Update README and `AGENTS.md`.
7. Add release workflow secret gate.
8. Bump version to `0.9.1`.
9. Configure GitHub secret `RALLY_NEW_RELIC_LICENSE_KEY` before merging/releasing.

Rollback:
- Set `RALLY_TELEMETRY=0` or `[telemetry] enabled = false` to disable all telemetry immediately.
- If release wiring fails, remove the New Relic secret or ship a patch with empty baked credentials; source builds remain opt-in.
- If New Relic agent ingestion fails, the unchanged `Sink` boundary allows reverting to no-op for a patch release without restoring Sentry.

## Research Notes

- New Relic pricing page currently states the free tier includes 100 GB/month of data ingest and $0.40/GB beyond the free 100 GB.
- New Relic Go APM agent docs and code show the APIs Rally needs: `ConfigFromEnvironment`, `ConfigLicense`, `ConfigAppName`, `Application.StartTransaction`, `Transaction.StartSegment`, `Transaction.AddAttribute`, `Application.RecordCustomEvent`, `Transaction.NoticeError`, `newrelic.Error`, `newrelic.NewStackTrace`, `Application.WaitForConnection`, and `Application.Shutdown`.
- Local inspection of `github.com/newrelic/go-agent/v3@v3.43.3` shows application log forwarding defaults to enabled and can be influenced by `NEW_RELIC_APPLICATION_LOGGING_*` environment variables, so Rally should apply explicit logging options after environment config rather than relying on defaults.
