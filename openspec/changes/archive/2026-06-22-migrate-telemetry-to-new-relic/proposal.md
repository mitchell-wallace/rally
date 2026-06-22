## Why

Rally's baked Sentry telemetry has exhausted the useful free-tier budget, reducing the value of release telemetry just as 0.9.x reliability work needs more field data. New Relic's free ingest allowance is a better fit for the current single-operator product stage, and the 0.9.1 patch release should move telemetry there before the broader 0.10.0 feature release.

## What Changes

- Hard-cut Rally release telemetry from Sentry to New Relic for 0.9.1; remove Sentry release wiring, fallback behavior, docs, and SDK usage.
- Use the New Relic Go APM agent (`github.com/newrelic/go-agent/v3/newrelic`) behind Rally's existing `internal/telemetry.Sink` boundary.
- Preserve the activation contract: source builds remain silent unless configured, release binaries activate only for relay-running commands, `RALLY_TELEMETRY=0` force-disables telemetry, and `[telemetry] enabled = false` provides a config-level opt-out without rebuilding.
- Keep Rally's current best-effort PII protections for Rally-supplied telemetry: no prompts/transcripts, home paths collapsed, no custom username/hostname attributes, and no obvious sensitive values in tags or contexts.
- Allow New Relic agent-native runtime/APM metadata and application log forwarding where useful, while keeping Rally from deliberately writing prompts, transcripts, or raw command output into New Relic log records.
- Update docs and release configuration so 0.9.1 is the telemetry-provider migration release.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `telemetry`: activation, provider, event taxonomy wording, flush behavior, privacy guarantees, and backend mapping move from Sentry-specific DSN/Issues to New Relic Go APM transactions, segments, custom events, and noticed errors.
- `cli-config`: `[telemetry]` gains an explicit `enabled` opt-out and New Relic app metadata while credentials stay in environment variables or baked release ldflags.
- `tooling-distribution`: GoReleaser and release workflow secrets change from `RALLY_SENTRY_DSN`/`DefaultSentryDSN` to New Relic release telemetry injection for 0.9.1.

## Impact

- Code: `internal/telemetry`, `internal/config`, `cmd/rally`, `.goreleaser.yaml`, `.github/workflows/release.yml`, README telemetry docs, `AGENTS.md` observability guidance, and telemetry-focused tests.
- Dependencies: remove `github.com/getsentry/sentry-go`; add `github.com/newrelic/go-agent/v3`.
- Operations: the New Relic ingest license key has already been configured as the GitHub Actions secret `RALLY_NEW_RELIC_LICENSE_KEY`, and the app name has already been configured as GitHub variable `RALLY_NEW_RELIC_APP_NAME`; keep `RALLY_TELEMETRY=0` and add `[telemetry] enabled = false` as user opt-outs.
- Privacy/cost: preserve Rally-supplied payload scrubbing and no-transcript guarantees, enable New Relic application logs intentionally, and add custom-event/attribute/log-volume limits so larger ingest allowance does not become unbounded telemetry volume.
