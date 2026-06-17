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

