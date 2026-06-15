## ADDED Requirements

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
