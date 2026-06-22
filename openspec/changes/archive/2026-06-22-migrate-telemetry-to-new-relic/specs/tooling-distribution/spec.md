## ADDED Requirements

### Requirement: 0.9.1 New Relic release telemetry wiring
The release tooling SHALL inject a New Relic release telemetry license key for 0.9.1 instead of Sentry credentials. GoReleaser SHALL set `main.DefaultNewRelicLicenseKey` from `RALLY_NEW_RELIC_LICENSE_KEY`, and the GitHub release workflow SHALL source that value from GitHub secrets. The release workflow SHALL fail before GoReleaser when building a not-yet-existing release and the New Relic secret is empty, so a version bump cannot silently ship a release without baked telemetry. `main.Version` SHALL remain `"dev"` in source, and the release version SHALL be controlled by `internal/buildinfo/VERSION`.

#### Scenario: Release ldflags inject New Relic license
- **WHEN** GoReleaser builds a release binary
- **THEN** its ldflags SHALL inject `main.DefaultNewRelicLicenseKey` from `RALLY_NEW_RELIC_LICENSE_KEY`
- **AND** it SHALL NOT inject `main.DefaultSentryDSN` for the 0.9.1 release path

#### Scenario: Release workflow uses New Relic secret
- **WHEN** the GitHub release workflow runs for a not-yet-existing release
- **THEN** it SHALL verify `RALLY_NEW_RELIC_LICENSE_KEY` is non-empty before GoReleaser starts

#### Scenario: Missing New Relic secret fails release
- **WHEN** the GitHub release workflow is about to build a not-yet-existing release
- **AND** the New Relic release telemetry secret is empty
- **THEN** the workflow SHALL fail before GoReleaser creates release artifacts

#### Scenario: Version file bumps to 0.9.1
- **WHEN** the New Relic telemetry migration is implemented and release secret gating is in place
- **THEN** `internal/buildinfo/VERSION` SHALL be set to `0.9.1`
- **AND** `main.Version` SHALL remain `"dev"` for ldflag injection

#### Scenario: Tags are still created by auto-tag workflow
- **WHEN** the 0.9.1 version bump reaches `main`
- **THEN** release tagging SHALL be handled by the existing auto-tag workflow rather than by manually pushing a tag
