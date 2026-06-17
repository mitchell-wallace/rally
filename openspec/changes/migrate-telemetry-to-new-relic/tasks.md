## 1. Dependencies and telemetry boundary

- [ ] 1.1 Add the New Relic Go agent dependency (`github.com/newrelic/go-agent/v3/newrelic`) and remove `github.com/getsentry/sentry-go` if Sentry fallback is not retained.
- [ ] 1.2 Update `internal/telemetry/sink.go` comments and names so the public sink contract is backend-neutral (`operator-worthy failure`, `custom event`, `flush`) rather than Sentry-specific.
- [ ] 1.3 Extract Sentry-specific scrubbing into backend-neutral helpers that sanitize scalar attributes and context maps before New Relic conversion.
- [ ] 1.4 Add tests proving scrubbed New Relic-bound payloads remove prompt/transcript/log/current_task fields, collapse home paths, truncate long strings, and never include host/user/network identity.

## 2. New Relic sink implementation

- [ ] 2.1 Implement `internal/telemetry/newrelic.go` with `NewRelicSink` satisfying `Sink`.
- [ ] 2.2 Initialize the New Relic application with license key, app name, disabled automatic log forwarding, custom events enabled, distributed tracing enabled where appropriate, and best-effort `WaitForConnection` bounded by startup timeout.
- [ ] 2.3 Map `StartSpan` so the relay starts a background transaction and run/try spans become child timing segments; preserve context propagation for nested spans.
- [ ] 2.4 Map `Span.SetTag`/`SetData` into New Relic-compatible attributes after scrubbing and scalar conversion.
- [ ] 2.5 Map `EmitTryLog` and `CaptureEvent` into bounded custom events with stable event types such as `RallyTry` and `RallyDiagnostic`.
- [ ] 2.6 Map `CaptureFailure` into a noticed New Relic error plus a bounded custom failure event so operator-worthy failures remain queryable by Rally tags.
- [ ] 2.7 Implement `Flush` via New Relic shutdown with Rally's bounded timeout; ensure unreachable network cannot hang process exit.
- [ ] 2.8 Add unit tests for transaction/segment/event/error mapping using a test seam or fake New Relic app wrapper so tests do not make network calls.

## 3. Attribute limits and cost guardrails

- [ ] 3.1 Add a deterministic attribute builder that merges tags and contexts into New Relic-compatible scalar attributes.
- [ ] 3.2 Enforce New Relic custom event constraints: event type allowed characters and `<255` bytes, attribute keys `<255` bytes, scalar value types only, and no more than 64 attributes.
- [ ] 3.3 Prioritize correlation and failure fields (`relay_id`, `run_id`, `try_id`, `repo`, `lap_id`, `runner`, `role`, `outcome`, `failure_category`, `recovery_classification`, `agent_state`) before lower-priority context fields when the attribute budget is exceeded.
- [ ] 3.4 Add regression tests that oversized context payloads are dropped deterministically rather than JSON-encoded into a large attribute.
- [ ] 3.5 Confirm `EmitTryLog` remains one event per persisted try and no automatic stdout/stderr/agent log forwarding is enabled.

## 4. Activation, config, and legacy behavior

- [ ] 4.1 Extend `internal/config.TelemetryConfig` and TOML loading with `NewRelicLicenseKey`, `NewRelicAppName`, and retained/deprecated `SentryDSN` fields as decided in design.
- [ ] 4.2 Update `cmd/rally/main.go` telemetry globals and `telemetryConfigForRelay`: `DefaultNewRelicLicenseKey`, optional default app name, and New Relic config fields.
- [ ] 4.3 Update `internal/telemetry/init.go` precedence: `RALLY_TELEMETRY=0`, `NEW_RELIC_LICENSE_KEY`, config New Relic key, baked New Relic key, optional legacy Sentry fallback, no-op.
- [ ] 4.4 Emit a deprecation warning when legacy Sentry-only telemetry is used, and ensure New Relic always wins when both providers are configured.
- [ ] 4.5 Preserve no-side-effect mechanical commands: help/version/update must not initialize telemetry or create `machine-id` because a baked New Relic key exists.
- [ ] 4.6 Add activation tests for every precedence branch, kill switch behavior, legacy fallback warning, and source-build no-op behavior.

## 5. Release wiring and documentation

- [ ] 5.1 Update `.goreleaser.yaml` ldflags from `main.DefaultSentryDSN={{ .Env.RALLY_SENTRY_DSN }}` to `main.DefaultNewRelicLicenseKey={{ .Env.RALLY_NEW_RELIC_LICENSE_KEY }}`.
- [ ] 5.2 Update `.github/workflows/release.yml` to pass `RALLY_NEW_RELIC_LICENSE_KEY` from the matching GitHub secret.
- [ ] 5.3 Update generated `.rally/config.toml` template and README telemetry docs for New Relic env/config precedence, opt-out, privacy, data sent, and legacy Sentry deprecation.
- [ ] 5.4 Bump `internal/buildinfo/VERSION` to `0.9.1` and confirm `main.Version` remains `"dev"`.
- [ ] 5.5 Add release checklist note: configure `RALLY_NEW_RELIC_LICENSE_KEY` before cutting 0.9.1; do not push tags manually.

## 6. Verification

- [ ] 6.1 Run targeted telemetry/config tests: `go test ./internal/telemetry ./internal/config ./cmd/rally`.
- [ ] 6.2 Run broader relay telemetry tests that exercise `SetTelemetry`, failure capture, limit diagnostics, recovery `needs_user`, and prompt-size fields.
- [ ] 6.3 Run `go test ./...` before finalizing the implementation.
- [ ] 6.4 Run `openspec validate migrate-telemetry-to-new-relic --strict`.
- [ ] 6.5 Manually inspect `go.mod`, `go.sum`, `.goreleaser.yaml`, `.github/workflows/release.yml`, and README diffs for leaked credentials or stale Sentry release wiring.
