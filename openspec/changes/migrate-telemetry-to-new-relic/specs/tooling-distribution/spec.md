## ADDED Requirements

### Requirement: 0.9.1 New Relic release telemetry wiring
The release tooling SHALL inject New Relic release telemetry credentials for 0.9.1 instead of Sentry credentials. GoReleaser SHALL set `main.DefaultNewRelicLicenseKey` from the `RALLY_NEW_RELIC_LICENSE_KEY` environment variable, and the GitHub release workflow SHALL source that environment variable from a GitHub secret of the same purpose. `main.Version` SHALL remain `"dev"` in source, and the release version SHALL be controlled by `internal/buildinfo/VERSION`.

#### Scenario: Release ldflags inject New Relic key
- **WHEN** GoReleaser builds a release binary
- **THEN** its ldflags SHALL inject `main.DefaultNewRelicLicenseKey` from `RALLY_NEW_RELIC_LICENSE_KEY`
- **AND** it SHALL NOT inject `main.DefaultSentryDSN` for the 0.9.1 release path

#### Scenario: Release workflow uses New Relic secret
- **WHEN** the GitHub release workflow runs
- **THEN** it SHALL pass the New Relic license key secret to GoReleaser as `RALLY_NEW_RELIC_LICENSE_KEY`

#### Scenario: Version file bumps to 0.9.1
- **WHEN** the New Relic telemetry migration is implemented
- **THEN** `internal/buildinfo/VERSION` SHALL be set to `0.9.1`
- **AND** `main.Version` SHALL remain `"dev"` for ldflag injection

#### Scenario: Tags are still created by auto-tag workflow
- **WHEN** the 0.9.1 version bump reaches `main`
- **THEN** release tagging SHALL be handled by the existing auto-tag workflow rather than by manually pushing a tag
