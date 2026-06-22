## ADDED Requirements

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
