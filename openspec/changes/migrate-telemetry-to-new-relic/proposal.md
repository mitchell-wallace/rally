## Why

Rally's baked Sentry telemetry has exhausted the available free-tier budget, reducing the usefulness of release telemetry just as 0.9.x reliability work needs more field data. New Relic currently offers a larger free monthly ingest allowance, so 0.9.1 should move product telemetry there before the broader 0.10.0 feature release.

## What Changes

- Replace Rally's baked Sentry release telemetry path with New Relic-backed telemetry for release binaries.
- Preserve Rally's current no-surprise telemetry contract: disabled by default for source builds, active only for relay-running commands, force-disabled by `RALLY_TELEMETRY=0`, and no machine-id side effects while disabled.
- Add New Relic configuration using ingest license key/app metadata, with environment variables taking precedence over `.rally/config.toml` and baked release defaults.
- Keep the existing `internal/telemetry.Sink` boundary and runner call sites; implement a New Relic sink behind that boundary rather than reworking relay telemetry emission.
- Retire Sentry-specific release wiring and docs for 0.9.1 while leaving a short compatibility/deprecation path for user-provided Sentry config where needed.
- Update docs and release configuration so 0.9.1 is the telemetry-provider migration release.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `telemetry`: activation, provider, event taxonomy wording, flush behavior, privacy guarantees, and backend mapping move from Sentry-specific DSN/Issues to New Relic ingest/APM-compatible events and errors.
- `cli-config`: `[telemetry]` config keys and release defaults change from Sentry DSN to New Relic license key/app metadata while preserving the global kill switch.
- `tooling-distribution`: GoReleaser and release workflow secrets change from `RALLY_SENTRY_DSN`/`DefaultSentryDSN` to New Relic release telemetry injection for 0.9.1.

## Impact

- Code: `internal/telemetry`, `internal/config`, `cmd/rally`, `.goreleaser.yaml`, `.github/workflows/release.yml`, README telemetry docs, and telemetry-focused tests.
- Dependencies: replace or deprecate the Sentry SDK dependency for release telemetry; add either the New Relic Go agent or OpenTelemetry OTLP dependencies based on design.
- Operations: configure New Relic ingest credentials as GitHub Actions secrets before cutting 0.9.1; keep `RALLY_TELEMETRY=0` as the user opt-out.
- Privacy/cost: preserve existing scrubbing and no-transcript guarantees, and add event/attribute limits so larger ingest allowance does not become unbounded telemetry volume.
