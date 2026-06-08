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

