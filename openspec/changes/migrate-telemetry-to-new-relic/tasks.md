## 1. Telemetry boundary and Sentry removal

- [ ] 1.1 Remove `github.com/getsentry/sentry-go` from `go.mod`/`go.sum`, delete `internal/telemetry/sentry.go`, and remove Sentry-specific tests.
- [x] 1.2 Update `internal/telemetry/sink.go` comments so the public sink contract is backend-neutral (`operator-worthy failure`, `custom event`, `flush`) rather than Sentry-specific.
- [ ] 1.3 Remove `DefaultSentryDSN`, `SENTRY_DSN`, `[telemetry] sentry_dsn`, Sentry fallback/deprecation branches, Sentry release ldflags, and docs that describe current Sentry telemetry.
- [x] 1.4 Extract or retain backend-neutral scrubbing helpers that sanitize Rally-supplied scalar attributes and context maps before New Relic receives them.
- [ ] 1.5 Add tests proving Rally-supplied New Relic attributes drop prompt/transcript/log/current_task and host/user identity keys, collapse home paths, truncate long strings, and do not emit `[scrubbed]` placeholder attributes for removed sensitive keys.
- [x] 1.6 Add tests proving lap mismatch telemetry uses `RallyDiagnostic` with `level=warning`, `event_kind=lap_pin_mismatch`, and `mismatch_reason`, not `RallyFailure`.
- [x] 1.7 Add `telemetry.LevelWarning`; map it to New Relic diagnostic attributes without any Sentry severity mapping.

## 2. New Relic Go APM sink

- [x] 2.1 Add `github.com/newrelic/go-agent/v3` and implement `internal/telemetry/newrelic.go` as `NewRelicSink` satisfying `Sink`.
- [x] 2.2 Add New Relic config fields to telemetry initialization: license key, app name, generic host display name, explicit app-log-forwarding/log-metrics settings, startup wait timeout, shutdown timeout, and baked defaults as needed.
- [x] 2.3 Map root relay `StartSpan` calls to New Relic transactions and child run/try spans to New Relic segments while preserving Rally span ids/parent ids as custom attributes.
- [x] 2.4 Attach bounded, scrubbed Rally attributes to transactions/segments for operation, description, duration, relay/run/try ids, role, runner, outcome, failure classification, and recovery classification.
- [x] 2.5 Map `EmitTryLog` to one `Application.RecordCustomEvent("RallyTry", attrs)` call per persisted try, moving the existing `internal/relay/runner.go` normal and handoff-only `EmitTryLog` calls after successful `store.AppendTry` so failed persistence cannot emit `RallyTry`.
- [x] 2.6 Map `CaptureEvent` to `Application.RecordCustomEvent("RallyDiagnostic", attrs)` with a scalar `level`.
- [x] 2.7 Map `CaptureFailure` to New Relic error reporting using transaction-scoped `Transaction.NoticeError(newrelic.Error{Message, Class, Attributes})` with bounded attributes, and record a `RallyFailure` custom event when useful for NRQL continuity.
- [x] 2.8 Implement `Flush` with bounded `Application.Shutdown`/connection waiting so unreachable New Relic endpoints do not hang CLI exit.
- [x] 2.9 Configure native panic capture with `cfg.ErrorCollector.RecordPanics = true`, and implement a panic-aware transaction finish/recovery path where the `Span.Finish` abstraction would otherwise hide deferred `Transaction.End`: recover, notice a `newrelic.Error` with `newrelic.NewStackTrace()` and bounded attributes, end the transaction, then re-panic. String-based panic classification remains only a fallback.
- [x] 2.10 Add unit tests with a fake/isolated New Relic app configuration where possible for transaction/segment creation, custom event payload shape, error attributes/classes, panic-aware transaction finish/recovery behavior, shutdown timeout behavior, and no network calls when disabled.

## 3. Attribute limits and cost guardrails

- [x] 3.1 Add a deterministic attribute builder that merges Rally tags and contexts into New Relic-compatible scalar attributes.
- [x] 3.2 Enforce Rally's local budget: fixed custom event names (`RallyTry`, `RallyDiagnostic`, `RallyFailure`), string/number/bool values only, at most 64 attributes per custom event/error, attribute keys under 255 bytes, and bounded string values.
- [x] 3.3 Prioritize correlation and failure fields (`relay_id`, `run_id`, `try_id`, `repo`, `lap_id`, `runner`, `role`, `outcome`, `failure_category`, `recovery_classification`, `agent_state`) before lower-priority context fields when the attribute budget is exceeded.
- [x] 3.4 Add regression tests that oversized context payloads are dropped deterministically rather than JSON-encoded into a large attribute.
- [x] 3.5 Configure the New Relic agent to keep application logging enabled after any `ConfigFromEnvironment` application, using `ConfigAppLogEnabled(true)`, `ConfigAppLogForwardingEnabled(true)`, `ConfigAppLogMetricsEnabled(true)`, bounded `ConfigAppLogForwardingMaxSamplesStored(...)`, and `ConfigAppLogDecoratingEnabled(false)` or direct equivalent fields.
- [x] 3.6 Set a generic New Relic host display name where supported, and avoid adding Rally custom attributes for raw hostname, username, IP, or home-directory username.
- [x] 3.7 Add regression tests proving Rally's post-environment New Relic config leaves application logging and forwarding enabled, keeps local decorating off, applies the sample limit, and does not add `Application.RecordLog` calls or logger integrations in 0.9.1.
- [x] 3.8 Confirm event volume stays bounded: one `RallyTry` per persisted try, bounded relay/run/try transactions/segments, bounded application-log forwarding samples, and no prompt-line/agent-output custom events.

## 4. Activation and config opt-out

- [ ] 4.1 Extend `internal/config.TelemetryConfig` with `enabled *bool`, `new_relic_app_name`, and optional `new_relic_host_display_name`; remove/deprecate `sentry_dsn`.
- [ ] 4.2 Update generated `.rally/config.toml` docs/template to make `[telemetry]` opt-out discoverable as a commented example (`# enabled = false`) or equivalent unset default, never as active `enabled = false`, and avoid any secret fields.
- [ ] 4.3 Update `cmd/rally/main.go` telemetry globals and `telemetryConfigForRelay`: remove `DefaultSentryDSN`; add `DefaultNewRelicLicenseKey`, app-name/host-display-name fields, and New Relic agent config options.
- [ ] 4.4 Update telemetry initialization precedence: `RALLY_TELEMETRY=0`, `[telemetry] enabled=false`, `NEW_RELIC_LICENSE_KEY`, baked `DefaultNewRelicLicenseKey`, no-op.
- [ ] 4.5 Treat missing New Relic license key as non-activating with no network calls; do not fall back to Sentry.
- [ ] 4.6 Preserve no-side-effect mechanical commands: help/version/update must not initialize telemetry or create `machine-id` because baked New Relic credentials exist.
- [ ] 4.7 Add activation tests for every precedence branch, kill switch behavior, config opt-out behavior, source-build no-op behavior, and tracked config not accepting New Relic secrets.

## 5. Release wiring and documentation

- [ ] 5.1 Update `.goreleaser.yaml` ldflags from `main.DefaultSentryDSN={{ .Env.RALLY_SENTRY_DSN }}` to `main.DefaultNewRelicLicenseKey={{ .Env.RALLY_NEW_RELIC_LICENSE_KEY }}`.
- [ ] 5.2 Update `.github/workflows/release.yml` to pass the New Relic license secret and fail before GoReleaser when a not-yet-existing release would build with `RALLY_NEW_RELIC_LICENSE_KEY` empty.
- [ ] 5.3 Update README telemetry docs for Go APM agent behavior, env/baked credential precedence, `[telemetry] enabled = false`, `RALLY_TELEMETRY=0`, data sent, data intentionally not sent, and the hard Sentry cutover.
- [ ] 5.4 Update `AGENTS.md` observability guidance from Sentry CLI investigation to New Relic guidance, while noting Sentry issue IDs only as historical pre-0.9.1 references.
- [ ] 5.5 Bump `internal/buildinfo/VERSION` to `0.9.1` only after the release secret gate is implemented; confirm `main.Version` remains `"dev"`.
- [ ] 5.6 Add release checklist note: `RALLY_NEW_RELIC_LICENSE_KEY` and `RALLY_NEW_RELIC_APP_NAME` are already configured in GitHub; verify they are still present before cutting 0.9.1, keep the license key secret, and do not push tags manually.
- [ ] 5.7 Update active `release-0-10-0-reliability-and-model-routing` OpenSpec artifacts so telemetry language builds on New Relic Go APM/backend-neutral terminology instead of reintroducing Sentry-specific Issues or fallback behavior.
- [ ] 5.8 Retire or rewrite obsolete planning references that would steer future work back to Sentry, including `openspec/changes/enrich-sentry-coverage/draft.md` and `openspec/next-up.md`; preserve only backend-neutral/New Relic concepts such as native panic/error capture.

## 6. Verification

- [ ] 6.1 Run targeted telemetry/config tests: `go test ./internal/telemetry ./internal/config ./cmd/rally`.
- [ ] 6.2 Run broader relay telemetry tests that exercise `SetTelemetry`, failure capture, limit diagnostics, recovery `needs_user`, lap mismatch diagnostics, and prompt-size fields.
- [ ] 6.3 Run `go test ./...` before finalizing the implementation.
- [ ] 6.4 Run `openspec validate migrate-telemetry-to-new-relic --strict`.
- [ ] 6.5 Manually inspect `go.mod`, `go.sum`, `.goreleaser.yaml`, `.github/workflows/release.yml`, README, and `AGENTS.md` diffs for leaked credentials, accidental Sentry release wiring, or log-forwarding exposure.
