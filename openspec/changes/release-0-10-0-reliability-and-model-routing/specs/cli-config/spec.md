## ADDED Requirements

### Requirement: Role reasoning configuration
The system SHALL support a top-level `[reasoning]` configuration table that maps role names to model alias or reasoning-effort preferences. Role keys SHALL be case-insensitive. Explicit model selections in routes SHALL take precedence over role reasoning defaults.

#### Scenario: Role reasoning alias resolves
- **WHEN** `[reasoning]` maps a role such as `verify` to a configured model alias
- **THEN** route resolution for that role SHALL apply the alias when the selected route entry has no explicit model token

#### Scenario: Explicit route model wins
- **WHEN** a route entry includes an explicit model token and `[reasoning]` also has a value for that role
- **THEN** the explicit route model SHALL be used instead of the role reasoning default

#### Scenario: Unknown reasoning value warns
- **WHEN** a role reasoning value is unknown or unsupported for the resolved harness
- **THEN** the system SHALL emit a warning or advisory diagnostic rather than failing config loading before the harness runs

#### Scenario: Unsupported harness effort is skipped
- **WHEN** the resolved harness has no Rally-usable reasoning flag for the configured value
- **THEN** the system SHALL skip effort injection for that harness and warn the operator
