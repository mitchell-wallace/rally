## ADDED Requirements

### Requirement: New Relic telemetry configuration
The system SHALL support New Relic telemetry configuration under `[telemetry]` using an explicit `enabled` opt-out and non-secret metadata such as `new_relic_app_name` and optional `new_relic_host_display_name`. New Relic ingest credentials SHALL NOT be read from `.rally/config.toml` because that file is tracked and may be committed. `NEW_RELIC_LICENSE_KEY` SHALL be the supported user-provided credential environment variable, and the baked release license key SHALL be injected only by release tooling. `new_relic_app_name` SHALL default to a non-identifying Rally app name when unset.

#### Scenario: Environment New Relic license enables telemetry
- **WHEN** `NEW_RELIC_LICENSE_KEY` is set
- **AND** `RALLY_TELEMETRY` is not `0`
- **AND** `[telemetry] enabled` is not `false`
- **THEN** relay commands SHALL initialize New Relic telemetry with that license

#### Scenario: Config can disable telemetry
- **WHEN** `[telemetry] enabled = false` is configured
- **THEN** relay commands SHALL NOT initialize New Relic telemetry
- **AND** no machine-local identifier file SHALL be created

#### Scenario: Tracked config cannot hold New Relic secret
- **WHEN** `.rally/config.toml` contains `[telemetry]`
- **THEN** the system SHALL NOT read a New Relic license key from that tracked config file

#### Scenario: App name can be configured
- **WHEN** `NEW_RELIC_APP_NAME` or `[telemetry] new_relic_app_name` is set
- **THEN** the New Relic agent SHALL use that app name after scrubbing/validation

#### Scenario: Host display name can be generic
- **WHEN** New Relic's standard `NEW_RELIC_PROCESS_HOST_DISPLAY_NAME` or `[telemetry] new_relic_host_display_name` is set
- **THEN** the New Relic agent SHALL use that display name after scrubbing/validation

#### Scenario: Generated config exposes opt-out but no secret field
- **WHEN** rally initializes a workspace config
- **THEN** the generated `[telemetry]` section SHALL make the `enabled = false` opt-out discoverable without making it active by default, for example as `# enabled = false`
- **AND** it SHALL NOT include a `new_relic_license_key` field

### Requirement: Sentry telemetry removal
The system SHALL remove Sentry from the active telemetry configuration path. Complete New Relic credentials and baked New Relic release credentials SHALL NOT be affected by `SENTRY_DSN` or `[telemetry] sentry_dsn`. Legacy Sentry-only configuration SHALL NOT initialize telemetry in 0.9.1.

#### Scenario: New Relic ignores legacy Sentry config
- **WHEN** a New Relic license and a legacy Sentry DSN are configured
- **THEN** the system SHALL initialize New Relic telemetry

#### Scenario: Legacy Sentry config alone is inert
- **WHEN** only `SENTRY_DSN` or `[telemetry] sentry_dsn` is configured
- **THEN** the system SHALL NOT initialize Sentry
- **AND** telemetry SHALL remain disabled

#### Scenario: Kill switch disables New Relic
- **WHEN** `RALLY_TELEMETRY=0` is set
- **AND** New Relic telemetry credentials are configured
- **THEN** New Relic SHALL NOT initialize
