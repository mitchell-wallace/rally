## ADDED Requirements

### Requirement: New Relic telemetry configuration
The system SHALL support New Relic telemetry configuration under `[telemetry]` using `new_relic_license_key` and `new_relic_app_name`. The environment variables `NEW_RELIC_LICENSE_KEY` and `NEW_RELIC_APP_NAME` SHALL override config values. `new_relic_license_key` SHALL be omitted from generated config by default or generated empty, because it is an ingest credential. `new_relic_app_name` SHALL default to a non-identifying Rally app name when unset.

#### Scenario: Config New Relic key enables telemetry
- **WHEN** `.rally/config.toml` contains `[telemetry] new_relic_license_key`
- **AND** `RALLY_TELEMETRY` is not `0`
- **THEN** relay commands SHALL initialize New Relic telemetry with that key

#### Scenario: Environment New Relic key wins
- **WHEN** `NEW_RELIC_LICENSE_KEY` is set and config also contains `new_relic_license_key`
- **THEN** the environment key SHALL be used

#### Scenario: App name can be configured
- **WHEN** `NEW_RELIC_APP_NAME` or `[telemetry] new_relic_app_name` is set
- **THEN** the New Relic application name SHALL use that value after scrubbing/validation

#### Scenario: Generated config does not include a secret
- **WHEN** rally initializes a workspace config
- **THEN** the generated `[telemetry]` section SHALL NOT contain a real license key value

### Requirement: Legacy Sentry telemetry deprecation
The system MAY continue to parse `[telemetry] sentry_dsn` and `SENTRY_DSN` as a legacy fallback for one compatibility window, but New Relic configuration and baked New Relic release credentials SHALL take precedence. When legacy Sentry configuration is used because no New Relic key exists, the system SHALL emit a deprecation warning that 0.9.1 moved release telemetry to New Relic.

#### Scenario: New Relic beats legacy Sentry
- **WHEN** both a New Relic key and a legacy Sentry DSN are configured
- **THEN** the system SHALL initialize New Relic telemetry

#### Scenario: Legacy Sentry fallback warns
- **WHEN** only `SENTRY_DSN` or `[telemetry] sentry_dsn` is configured
- **THEN** the system MAY initialize Sentry telemetry
- **AND** it SHALL warn that Sentry telemetry is deprecated in favor of New Relic

#### Scenario: Kill switch disables both providers
- **WHEN** `RALLY_TELEMETRY=0` is set
- **AND** New Relic and legacy Sentry telemetry credentials are configured
- **THEN** neither provider SHALL initialize
