# cli-config Specification

## Purpose
TBD - created by archiving change cli-polish. Update Purpose after archive.
## Requirements
### Requirement: Init subcommands
The system SHALL provide `rally init` (workspace init), `rally init roles` (role init),
and `rally init all` (workspace + roles in sequence). Each subcommand SHALL be
independently re-runnable, merging into an existing config without discarding unrelated
sections.

#### Scenario: init all runs the sequence
- **WHEN** `rally init all` is invoked
- **THEN** the system SHALL run workspace init and role init in sequence

#### Scenario: init roles is scoped
- **WHEN** `rally init roles` is invoked on an existing config
- **THEN** the system SHALL add or update only role configuration and leave other sections intact

### Requirement: Free-run prompt config naming
The system SHALL name the free-run task-prompt configuration `[free_run] prompt_file`
(with corresponding `FreeRunPromptFile` field and `loadFreeRunPrompt` /
`builtInDefaultFreeRunPrompt` symbols). For one release the system SHALL also accept the
deprecated `[fallback] instructions_file` key as an alias, emitting a deprecation warning
when it is used. The rename SHALL NOT change free-run behavior.

#### Scenario: New key loads
- **WHEN** a config sets `[free_run] prompt_file`
- **THEN** the system SHALL load it as the free-run task prompt

#### Scenario: Deprecated key still loads with a warning
- **WHEN** a config sets the old `[fallback] instructions_file` key
- **THEN** the system SHALL load it as the free-run task prompt and emit a deprecation warning

#### Scenario: Behavior unchanged by rename
- **WHEN** a free run (laps-less and promptless) executes after the rename
- **THEN** the system SHALL use the free-run prompt exactly as before the rename

### Requirement: Run/try timeout configuration
The system SHALL accept three `[reliability]` configuration keys governing try/run duration: `run_timeout_secs` (the per-run wall-clock budget measured across all retry attempts, default 4500), `try_timeout_secs` (a secondary per-attempt cap, default 3600), and `handoff_timeout_secs` (the hard limit on the bounded handoff-only resume, default 300, not counted against the run budget). An unset or `0` value SHALL yield the default for each. The system SHALL ensure the handoff window never reaches or exceeds the effective per-try cap or run budget, clamping or rejecting a configuration that would let the handoff phase outlast them. When `try_timeout_secs` is greater than or equal to `run_timeout_secs` the per-try cap is subsumed by the run budget (the run budget always fires first); the system SHALL treat that configuration as valid and apply only the run budget rather than failing. The interactive config form SHALL expose all three fields alongside the existing reliability fields (`stall_threshold_secs`, `retry_budget`, `liveness_probe`).

#### Scenario: Defaults apply when unset
- **WHEN** `[reliability]` omits `run_timeout_secs`, `try_timeout_secs`, and `handoff_timeout_secs` (or sets them to 0)
- **THEN** the system SHALL use 4500, 3600, and 300 seconds respectively

#### Scenario: Configured timeouts are honored
- **WHEN** `[reliability]` sets `run_timeout_secs`, `try_timeout_secs`, and `handoff_timeout_secs`
- **THEN** the runner SHALL bound each run (across retries), each attempt, and each bounded handoff-only resume by the configured values

#### Scenario: Handoff window cannot outlast the run/try bounds
- **WHEN** a configuration sets `handoff_timeout_secs` greater than or equal to the effective `try_timeout_secs` or `run_timeout_secs`
- **THEN** the system SHALL clamp the handoff window below those bounds (or reject the configuration with a clear error) rather than allowing the handoff phase to outlast them

#### Scenario: Per-try cap at or above the run budget is subsumed
- **WHEN** a configuration sets `try_timeout_secs` greater than or equal to `run_timeout_secs`
- **THEN** the system SHALL accept the configuration and apply only the run budget (the per-try cap never fires before it) rather than failing

### Requirement: Recovery route in role configuration
The system SHALL include `recovery` among the roles listed in the interactive route-configuration form, so an operator can map the `recovery` route to one or more runners. The default-seeded `recovery` route SHALL prefer a senior-class runner. A missing `recovery` route SHALL NOT block configuration; recovery routing falls back to the lap's normal route with a warning at relay time.

#### Scenario: Recovery role appears in the config form
- **WHEN** the route-configuration form is shown
- **THEN** `recovery` SHALL appear in the role list alongside `default`, `junior`, `senior`, `ui`, and `verify`

#### Scenario: Recovery route maps to runners
- **WHEN** an operator assigns runners to the `recovery` route and saves
- **THEN** the system SHALL persist the `recovery` route in `.rally/config.toml`

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

### Requirement: Role reasoning configuration
The system SHALL support a top-level `[reasoning]` configuration table that maps role names to model alias or reasoning-effort preferences. Role keys SHALL be case-insensitive. Values SHALL be resolved only after the selected route entry's harness is known: harness-scoped aliases such as `op:g55-xh` or `cc:opus-high` resolve through that harness's model alias table, while bare effort tokens apply only to harnesses that support effort injection. Explicit model selections in routes SHALL take precedence over role reasoning defaults.

#### Scenario: Harness-scoped role reasoning alias resolves
- **WHEN** `[reasoning]` maps a role such as `verify` to a harness-scoped configured model alias
- **THEN** route resolution for that role SHALL apply the alias after the selected route entry's harness is known and when the selected route entry has no explicit model token

#### Scenario: Explicit route model wins
- **WHEN** a route entry includes an explicit model token and `[reasoning]` also has a value for that role
- **THEN** the explicit route model SHALL be used instead of the role reasoning default

#### Scenario: Missing harness-scoped alias fails route check
- **WHEN** a role reasoning value references a harness-scoped model alias that is not configured
- **THEN** `rally routes check` SHALL report a clear error including the role, harness, and alias

#### Scenario: Unknown effort value warns
- **WHEN** a role reasoning value is an unknown effort token for the resolved harness
- **THEN** the system SHALL emit a warning or advisory diagnostic rather than failing config loading before the harness runs

#### Scenario: Unsupported harness effort is skipped
- **WHEN** the resolved harness has no Rally-usable reasoning flag for the configured value
- **THEN** the system SHALL skip effort injection for that harness and warn the operator

