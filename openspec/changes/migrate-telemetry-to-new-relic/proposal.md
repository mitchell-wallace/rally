## Why

Rally's baked Sentry telemetry has exhausted the available free-tier budget, reducing the usefulness of release telemetry just as 0.9.x reliability work needs more field data. New Relic currently offers a larger free monthly ingest allowance, so 0.9.1 should move product telemetry there before the broader 0.10.0 feature release.

## What Changes

- Replace Rally's baked Sentry release telemetry path with New Relic-backed telemetry for release binaries.
- Preserve Rally's current no-surprise telemetry contract: disabled by default for source builds, active only for relay-running commands, force-disabled by `RALLY_TELEMETRY=0`, and no machine-id side effects while disabled.
- Add New Relic activation using environment or baked release ingest credentials, while keeping tracked `.rally/config.toml` limited to non-secret metadata such as app name/region.
- Keep the existing `internal/telemetry.Sink` boundary and runner call sites; implement a New Relic sink behind that boundary rather than reworking relay telemetry emission.
- Retire Sentry-specific release wiring and docs for 0.9.1 while leaving a one-release compatibility/deprecation fallback for user-provided Sentry config when no New Relic credentials exist.
- Update docs and release configuration so 0.9.1 is the telemetry-provider migration release.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `telemetry`: activation, provider, event taxonomy wording, flush behavior, privacy guarantees, and backend mapping move from Sentry-specific DSN/Issues to New Relic Event API custom events.
- `cli-config`: `[telemetry]` config keys and release defaults change from Sentry DSN to non-secret New Relic metadata plus env/baked credential activation while preserving the global kill switch.
- `tooling-distribution`: GoReleaser and release workflow secrets change from `RALLY_SENTRY_DSN`/`DefaultSentryDSN` to New Relic release telemetry injection for 0.9.1.

## Impact

- Code: `internal/telemetry`, `internal/config`, `cmd/rally`, `.goreleaser.yaml`, `.github/workflows/release.yml`, README telemetry docs, `AGENTS.md` observability guidance, and telemetry-focused tests.
- Dependencies: no New Relic APM agent dependency for 0.9.1; implement the Event API sink with bounded standard-library HTTPS requests and retain Sentry SDK only for the one-release fallback if kept.
- Operations: configure New Relic account id and ingest license key as GitHub Actions secrets before cutting 0.9.1; keep `RALLY_TELEMETRY=0` as the user opt-out.
- Privacy/cost: preserve existing scrubbing and no-transcript guarantees, and add event/attribute limits so larger ingest allowance does not become unbounded telemetry volume.
