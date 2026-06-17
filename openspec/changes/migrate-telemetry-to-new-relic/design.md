## Context

Rally 0.9.0 sends release telemetry to Sentry through `internal/telemetry.Sink`, with a baked `DefaultSentryDSN` injected by GoReleaser and a global `RALLY_TELEMETRY=0` kill switch. The sink boundary already has the right shape for a backend swap: relay/run/try spans, structured try logs, operator-worthy failures, low-severity diagnostic events, flush, tag/context separation, and PII scrubbing.

The problem is operational rather than conceptual: Sentry's free-tier capacity is no longer enough for Rally's field telemetry, and the upcoming 0.10.0 work needs more signal. New Relic's current public pricing says the free tier includes 100 GB/month of data ingest, and the Go agent supports short-lived CLI patterns: environment/config initialization, background transactions, custom events, noticed errors, `WaitForConnection`, and `Shutdown` flushing.

## Goals / Non-Goals

**Goals:**
- Ship the provider migration as 0.9.1 before 0.10.0.
- Keep relay code and telemetry call sites stable by implementing a New Relic-backed `telemetry.Sink`.
- Preserve the privacy contract: no prompts, transcripts, hostnames, usernames, IP addresses, raw paths with home prefixes, or unbounded strings.
- Preserve the activation contract: source builds remain silent unless configured, release binaries activate only for relay-running commands, and `RALLY_TELEMETRY=0` prevents network calls and machine-id writes.
- Move release secret wiring from `RALLY_SENTRY_DSN` to New Relic ingest credentials.
- Keep user-provided Sentry config from accidentally activating baked release telemetry after 0.9.1; decide and document the compatibility behavior explicitly.

**Non-Goals:**
- No dashboard/alert-as-code provisioning in this change.
- No attempt to migrate historical Sentry data.
- No telemetry volume autosampler driven by New Relic billing APIs.
- No 0.10.0 feature implementation; only update 0.10.0 plans so they build on New Relic telemetry.

## Decisions

### 1. Use the New Relic Go agent for 0.9.1, not raw OTLP

Implement `internal/telemetry/newrelic.go` using `github.com/newrelic/go-agent/v3/newrelic`.

Rationale:
- The current sink interface maps naturally to New Relic agent primitives:
  - relay root span -> `Application.StartTransaction("relay-<id>")`
  - run/try child spans -> `Transaction.StartSegment(...)`
  - span tags/data -> transaction/segment attributes
  - `CaptureFailure` -> `Transaction.NoticeError(newrelic.Error{...})` plus a custom failure event
  - `CaptureEvent`/`EmitTryLog` -> bounded `RecordCustomEvent(...)`
- The Go agent has explicit `WaitForConnection` and `Shutdown` support for short-lived processes.
- It avoids adding an OpenTelemetry collector requirement or requiring users to configure OTLP headers/endpoints for source builds.

Alternatives considered:
- **OTLP exporter directly to New Relic**: vendor-neutral and uses New Relic's `api-key` header plus `https://otlp.nr-data.net` / EU endpoints, but it adds more SDK/provider setup and does not map Rally's existing failure/custom-event semantics as directly.
- **Keep Sentry and lower event volume**: avoids dependency churn but does not solve the free-tier exhaustion enough for the 0.10.0 observability needs.

### 2. Keep `Sink` as the stable boundary and rename Sentry-specific comments/types opportunistically

Do not push New Relic concepts into `internal/relay`. Keep `Sink`, `Span`, `FailureEvent`, and `Event` names, but update comments that currently say "Sentry Issue" to backend-neutral wording ("operator-worthy failure"). Add `NewRelicSink` alongside or replacing `SentrySink` inside `internal/telemetry`.

Implementation guidance:
- If Sentry compatibility is retained temporarily, keep it behind explicit user configuration only; release defaults should prefer New Relic.
- If Sentry support is removed, remove `internal/telemetry/sentry.go`, scrubber's direct `sentry.Event` dependency, `github.com/getsentry/sentry-go`, and Sentry tests or rewrite them against backend-neutral scrub helpers.
- Extract scrubbing into backend-neutral helpers that sanitize maps/attributes before either New Relic events or errors are emitted.

### 3. New activation precedence

Telemetry activation SHALL resolve in this order:
1. `RALLY_TELEMETRY=0` disables all telemetry.
2. `NEW_RELIC_LICENSE_KEY` enables New Relic and overrides config/baked keys.
3. `[telemetry] new_relic_license_key` enables New Relic from config.
4. baked `DefaultNewRelicLicenseKey` enables release telemetry.
5. optional legacy Sentry config/env is honored only when no New Relic key exists and no baked New Relic key exists.
6. no key disables telemetry.

Additional New Relic metadata:
- app name: `NEW_RELIC_APP_NAME`, `[telemetry] new_relic_app_name`, baked/default `"Rally CLI"`
- region/endpoint option: no custom endpoint for Go agent unless the agent supports it cleanly; use standard license-key account routing. If OTLP is introduced later, use the New Relic OTLP endpoint docs.
- environment attribute: `release`, `source`, or config-provided environment where useful, but avoid user-identifying values.

Rationale:
- New Relic must win for 0.9.1 release binaries so accidental stale Sentry config does not keep burning the Sentry tier.
- Legacy Sentry escape hatch can help local debugging, but it should be explicit and de-emphasized.

### 4. Event and attribute volume guardrails

New Relic's larger allowance is not a license to emit unbounded data.

Rules:
- Keep `EmitTryLog` as one custom event per persisted try, not per transcript line.
- Keep `CaptureEvent` for low-severity limit/fallback diagnostics only.
- Keep `CaptureFailure` only for operator-worthy failures and `needs_user`.
- Convert nested contexts into bounded flattened attributes with stable prefixes such as `rally.version`, `failure_evidence.message`, and `failure_evidence.raw_signal`.
- Drop or hash attributes that cannot be represented as string/number/bool after scrubbing; do not JSON-encode large nested maps into a single attribute.
- Enforce New Relic custom event constraints in code: event type made of allowed characters and under 255 bytes; attributes capped below 64 keys; keys under 255 bytes; values are simple scalar types.

### 5. Privacy and identity semantics are unchanged

Keep `machine-id` behavior: generate only after telemetry is active, store under `data_dir`, use prefix as a tag/attribute, full anonymous ID only in the backend-neutral `rally` context/attributes. Continue to collapse home paths and scrub prompt/transcript-looking keys before backend-specific conversion.

New Relic-specific note: do not enable automatic log forwarding for Rally CLI. Rally should send only its intentional structured telemetry, not stdout/stderr or agent logs.

### 6. Release wiring and docs

Replace release-time Sentry injection:
- `main.DefaultSentryDSN` -> `main.DefaultNewRelicLicenseKey` (and optionally `DefaultNewRelicAppName`)
- `.goreleaser.yaml` ldflags use `RALLY_NEW_RELIC_LICENSE_KEY`
- `.github/workflows/release.yml` passes `secrets.RALLY_NEW_RELIC_LICENSE_KEY`
- README telemetry section describes New Relic, opt-out, config/env precedence, data sent, privacy guarantees, and the reason 0.9.1 switched providers.

Versioning:
- Bump `internal/buildinfo/VERSION` to `0.9.1`.
- Leave `main.Version = "dev"` for GoReleaser injection.
- Do not create tags manually.

## Risks / Trade-offs

- **Risk: short-lived CLI exits before New Relic connects** -> call `WaitForConnection` with a small bounded timeout on initialization and `Shutdown` with the existing bounded flush timeout on cleanup.
- **Risk: New Relic event attribute limits drop important context** -> define deterministic flattening/attribute budget order, prioritize correlation tags, outcome/category, recovery classification, and bounded failure evidence.
- **Risk: stale Sentry config keeps sending to Sentry** -> make New Relic release default higher precedence than legacy Sentry config, and print/document a deprecation note when Sentry-only config is used.
- **Risk: privacy regression during backend conversion** -> keep scrubbing backend-neutral and test it against New Relic attributes/custom-event payloads, not only old Sentry events.
- **Risk: ingest volume still grows too quickly** -> keep one try log per try, no automatic logs, no transcript/log forwarding, and tests that `EmitTryLog` does not include prompt/output/log fields.
- **Risk: New Relic license key is more secret-like than a Sentry DSN** -> do not print it, do not write it into generated config by default, and treat release secret injection as a GitHub secret.

## Migration Plan

1. Implement `NewRelicSink` and backend-neutral scrubbing/attribute flattening while keeping `NoopSink` behavior unchanged.
2. Update config/env/ldflag resolution to prefer New Relic and deprecate Sentry.
3. Update tests for activation precedence, no-side-effect mechanical commands, privacy scrubbing, flush, failure/event mapping, and release wiring.
4. Update README and sample config.
5. Bump version to `0.9.1`.
6. Configure GitHub secret `RALLY_NEW_RELIC_LICENSE_KEY` before merging/releasing.

Rollback:
- Set `RALLY_TELEMETRY=0` to disable all telemetry immediately.
- If release wiring fails, remove the New Relic secret or ship a patch with an empty baked key; source builds remain opt-in.
- If the New Relic dependency causes build issues, revert the sink implementation behind the unchanged `Sink` interface.

## Research Notes

- New Relic pricing page currently states the free tier includes 100 GB/month of data ingest and $0.40/GB beyond the free 100 GB.
- New Relic Go agent docs show `ConfigFromEnvironment`, `ConfigLicense`, `ConfigAppName`, background transactions, custom events via `RecordCustomEvent`, error reporting via `NoticeError`, `WaitForConnection`, and `Shutdown`.
- New Relic OTLP docs remain a viable future path, with `api-key` header and US/EU endpoints, but are not the recommended 0.9.1 implementation path.
