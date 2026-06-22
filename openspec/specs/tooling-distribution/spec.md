# tooling-distribution Specification

## Purpose
TBD - created by archiving change tidy-rally-runtime-data-storage. Update Purpose after archive.
## Requirements
### Requirement: Laps bundled alongside rally
The installer SHALL install the `laps` binary alongside `rally` so that laps is available as a first-class companion. Laps SHALL remain independently usable, and `.laps/` SHALL stay a separate top-level directory; rally SHALL NOT relocate `.laps/` into `.rally/`, and laps SHALL NOT read rally state from `.rally/`.

#### Scenario: Installer provisions laps
- **WHEN** a user runs the rally installer
- **THEN** the installer SHALL place a compatible `laps` binary next to the `rally` binary

#### Scenario: Laps stays decoupled
- **WHEN** laps is invoked standalone
- **THEN** it SHALL operate against `.laps/` without requiring or reading `.rally/`

### Requirement: rally update command
The system SHALL provide a `rally update` command that upgrades both the `rally` and `laps` binaries.

#### Scenario: Update upgrades both binaries
- **WHEN** a user runs `rally update`
- **THEN** the command SHALL upgrade both `rally` and `laps` to their latest compatible versions

### Requirement: Minimum laps version check
On startup the system SHALL check the installed laps version against the minimum required by Rally's companion contract, including installed hooks and checked-in agent workflows, and SHALL warn (without hard-failing) when laps is too old. The minimum supported release for current Rally source SHALL be laps v0.8.1.

#### Scenario: Outdated laps warns
- **WHEN** rally starts and detects a laps version below the minimum
- **THEN** rally SHALL print a warning advising `rally update` and SHALL continue running

#### Scenario: Compatible laps is silent
- **WHEN** the installed laps version meets the minimum
- **THEN** rally SHALL not emit a version warning

### Requirement: Track laps.json for handoff
The repository SHALL track `.laps/laps.json` so the work queue travels between containers via git. The stray manually-committed `.laps/.gitignore` that excluded laps files SHALL be removed, and rally SHALL NOT generate a `.gitignore` under `.laps/`.

#### Scenario: laps.json is committed
- **WHEN** the `.laps/` directory is committed to git
- **THEN** `laps.json` SHALL be included so the queue is shareable across containers

#### Scenario: rally never writes .laps/.gitignore
- **WHEN** rally initializes or installs laps hooks
- **THEN** it SHALL NOT create a `.gitignore` inside `.laps/`

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
