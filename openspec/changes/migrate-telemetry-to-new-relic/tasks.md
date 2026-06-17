## 1. Telemetry boundary and scrubbed payload builder

- [ ] 1.1 Keep `github.com/getsentry/sentry-go` only for the one-release legacy fallback; do not add the New Relic Go APM agent dependency for 0.9.1.
- [ ] 1.2 Update `internal/telemetry/sink.go` comments so the public sink contract is backend-neutral (`operator-worthy failure`, `custom event`, `flush`) rather than Sentry-specific.
- [ ] 1.3 Extract Sentry-specific scrubbing into backend-neutral helpers that sanitize scalar attributes and context maps before either backend emits data.
- [ ] 1.4 Add tests proving New Relic-bound payloads drop prompt/transcript/log/current_task and host/user/network identity keys entirely, collapse home paths, truncate long strings, and do not emit `[scrubbed]` placeholder attributes for removed sensitive keys.
- [ ] 1.5 Add tests proving lap mismatch telemetry uses `RallyDiagnostic` with `level=warning`, `event_kind=lap_pin_mismatch`, and `mismatch_reason`, not `RallyFailure`.
- [ ] 1.6 Add `telemetry.LevelWarning`; map it to New Relic `level=warning` attributes and Sentry warning severity for the one-release fallback.

## 2. New Relic Event API sink

- [ ] 2.1 Implement `internal/telemetry/newrelic.go` as `NewRelicEventSink` satisfying `Sink` with a bounded in-memory queue and standard-library HTTPS client.
- [ ] 2.2 Add New Relic Event API config fields to `telemetry.Config`: license key, account id, app name, region, endpoint override, and baked defaults as needed.
- [ ] 2.3 Map `StartSpan` to a local span object with generated span id, parent span id, operation, description, start time, tags, and data.
- [ ] 2.4 Map `Span.Finish` to a queued `RallySpan` event containing duration, operation, description, span id, parent span id, and scrubbed attributes.
- [ ] 2.5 Map `EmitTryLog` to one queued `RallyTry` event per persisted try.
- [ ] 2.6 Map `CaptureEvent` to queued `RallyDiagnostic` events with scrubbed attributes and a scalar `level`.
- [ ] 2.7 Map `CaptureFailure` to queued `RallyFailure` events with `operator_worthy=true`, stable fingerprint/grouping attributes, and scrubbed failure contexts.
- [ ] 2.8 Implement `Flush` with bounded POSTs to the account-scoped New Relic Event API endpoint, using the license key only as an HTTP header and never logging it.
- [ ] 2.9 Add unit tests with an `httptest.Server` for request method/path, license-key header presence without logging, payload shape, batching, timeout behavior, and no network calls when disabled.

## 3. Attribute limits and cost guardrails

- [ ] 3.1 Add a deterministic attribute builder that merges tags and contexts into Event API-compatible scalar attributes.
- [ ] 3.2 Enforce Event API compatibility plus Rally's stricter local budget: fixed event type names (`RallySpan`, `RallyTry`, `RallyDiagnostic`, `RallyFailure`), bounded keys, string/number values only, max 64 attributes per event, and bounded request payload size.
- [ ] 3.3 Prioritize correlation and failure fields (`relay_id`, `run_id`, `try_id`, `repo`, `lap_id`, `runner`, `role`, `outcome`, `failure_category`, `recovery_classification`, `agent_state`) before lower-priority context fields when the attribute budget is exceeded.
- [ ] 3.4 Add regression tests that oversized context payloads are dropped deterministically rather than JSON-encoded into a large attribute.
- [ ] 3.5 Ensure the New Relic attribute builder omits full `rally.machine_id`, while retaining `machine_id_prefix` and `relay_guid` for correlation.
- [ ] 3.6 Confirm event volume stays bounded: one `RallyTry` per persisted try, one `RallySpan` per finished relay/run/try span, no prompt-line/agent-output/log forwarding events.

## 4. Activation, config, and legacy behavior

- [ ] 4.1 Extend `internal/config.TelemetryConfig` with non-secret New Relic metadata only: `new_relic_app_name`, `new_relic_region`, and optional `new_relic_event_endpoint`; retain/deprecate `sentry_dsn`.
- [ ] 4.2 Update `cmd/rally/main.go` telemetry globals and `telemetryConfigForRelay`: add `DefaultNewRelicLicenseKey`, `DefaultNewRelicAccountID`, app-name/region/endpoint fields, and keep `DefaultSentryDSN` only for legacy fallback if needed.
- [ ] 4.3 Update `internal/telemetry/init.go` precedence: `RALLY_TELEMETRY=0`, complete env New Relic pair, complete baked New Relic pair, legacy Sentry fallback, no-op.
- [ ] 4.4 Treat partial New Relic credentials as non-activating: one missing value SHALL fall through to legacy fallback or no-op without network calls.
- [ ] 4.5 Emit a deprecation warning when legacy Sentry-only telemetry is used, and ensure New Relic always wins when both providers are configured.
- [ ] 4.6 Preserve no-side-effect mechanical commands: help/version/update must not initialize telemetry or create `machine-id` because baked New Relic credentials exist.
- [ ] 4.7 Add activation tests for every precedence branch, kill switch behavior, partial credential behavior, legacy fallback warning, source-build no-op behavior, and tracked config not accepting New Relic secrets.

## 5. Release wiring and documentation

- [ ] 5.1 Update `.goreleaser.yaml` ldflags from `main.DefaultSentryDSN={{ .Env.RALLY_SENTRY_DSN }}` to `main.DefaultNewRelicLicenseKey={{ .Env.RALLY_NEW_RELIC_LICENSE_KEY }}` and `main.DefaultNewRelicAccountID={{ .Env.RALLY_NEW_RELIC_ACCOUNT_ID }}`.
- [ ] 5.2 Update `.github/workflows/release.yml` to pass both New Relic secrets and fail before GoReleaser when a not-yet-existing release would build with either secret empty.
- [ ] 5.3 Update generated `.rally/config.toml` template and README telemetry docs for Event API env/baked credential precedence, non-secret config metadata, opt-out, privacy, data sent, and legacy Sentry deprecation.
- [ ] 5.4 Update `AGENTS.md` observability guidance from Sentry-specific investigation to New Relic/Event API guidance, while noting legacy Sentry only for pre-0.9.1 or explicit fallback runs.
- [ ] 5.5 Bump `internal/buildinfo/VERSION` to `0.9.1` only after the release secret gate is implemented; confirm `main.Version` remains `"dev"`.
- [ ] 5.6 Add release checklist note: configure `RALLY_NEW_RELIC_LICENSE_KEY` and `RALLY_NEW_RELIC_ACCOUNT_ID` before cutting 0.9.1; do not push tags manually.
- [ ] 5.7 Update active `release-0-10-0-reliability-and-model-routing` OpenSpec artifacts so telemetry language builds on backend-neutral/New Relic terminology instead of reintroducing Sentry-specific Issues.

## 6. Verification

- [ ] 6.1 Run targeted telemetry/config tests: `go test ./internal/telemetry ./internal/config ./cmd/rally`.
- [ ] 6.2 Run broader relay telemetry tests that exercise `SetTelemetry`, failure capture, limit diagnostics, recovery `needs_user`, lap mismatch diagnostics, and prompt-size fields.
- [ ] 6.3 Run `go test ./...` before finalizing the implementation.
- [ ] 6.4 Run `openspec validate migrate-telemetry-to-new-relic --strict`.
- [ ] 6.5 Manually inspect `go.mod`, `go.sum`, `.goreleaser.yaml`, `.github/workflows/release.yml`, README, and `AGENTS.md` diffs for leaked credentials, accidental New Relic APM dependency, or stale Sentry release wiring.
