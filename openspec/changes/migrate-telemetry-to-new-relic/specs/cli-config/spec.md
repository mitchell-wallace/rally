## ADDED Requirements

### Requirement: New Relic telemetry configuration
The system SHALL support non-secret New Relic telemetry configuration under `[telemetry]` using `new_relic_app_name`, `new_relic_region`, and optionally `new_relic_event_endpoint` for tests or advanced local overrides. New Relic ingest credentials SHALL NOT be read from `.rally/config.toml` because that file is tracked and may be committed. `NEW_RELIC_LICENSE_KEY` and `NEW_RELIC_ACCOUNT_ID` SHALL be the supported user-provided credential environment variables, and baked release credentials SHALL be injected only by release tooling. `new_relic_app_name` SHALL default to a non-identifying Rally app name when unset.

#### Scenario: Environment New Relic credentials enable telemetry
- **WHEN** `NEW_RELIC_LICENSE_KEY` and `NEW_RELIC_ACCOUNT_ID` are set
- **AND** `RALLY_TELEMETRY` is not `0`
- **THEN** relay commands SHALL initialize New Relic telemetry with those credentials

#### Scenario: Tracked config cannot hold New Relic secret
- **WHEN** `.rally/config.toml` contains `[telemetry]`
- **THEN** the system SHALL NOT read a New Relic license key or account id from that tracked config file

#### Scenario: App name can be configured
- **WHEN** `NEW_RELIC_APP_NAME` or `[telemetry] new_relic_app_name` is set
- **THEN** the New Relic event attributes SHALL use that app name after scrubbing/validation

#### Scenario: Generated config does not include a secret field
- **WHEN** rally initializes a workspace config
- **THEN** the generated `[telemetry]` section SHALL NOT include a `new_relic_license_key` or `new_relic_account_id` field

#### Scenario: Region or endpoint can be configured without secrets
- **WHEN** `NEW_RELIC_REGION`, `NEW_RELIC_EVENT_ENDPOINT`, `[telemetry] new_relic_region`, or `[telemetry] new_relic_event_endpoint` is set
- **THEN** the Event API sink SHALL use the configured non-secret routing metadata while still requiring credentials from environment or baked release defaults

### Requirement: Legacy Sentry telemetry deprecation
The system SHALL continue to parse `[telemetry] sentry_dsn` and `SENTRY_DSN` as a legacy fallback for one compatibility window, but complete New Relic credentials and baked New Relic release credentials SHALL take precedence. When legacy Sentry configuration is used because no complete New Relic credential pair exists, the system SHALL emit a deprecation warning that 0.9.1 moved release telemetry to New Relic.

#### Scenario: New Relic beats legacy Sentry
- **WHEN** complete New Relic credentials and a legacy Sentry DSN are configured
- **THEN** the system SHALL initialize New Relic telemetry

#### Scenario: Legacy Sentry fallback warns
- **WHEN** only `SENTRY_DSN` or `[telemetry] sentry_dsn` is configured
- **THEN** the system SHALL initialize Sentry telemetry for the compatibility window
- **AND** it SHALL warn that Sentry telemetry is deprecated in favor of New Relic

#### Scenario: Kill switch disables both providers
- **WHEN** `RALLY_TELEMETRY=0` is set
- **AND** New Relic and legacy Sentry telemetry credentials are configured
- **THEN** neither provider SHALL initialize
