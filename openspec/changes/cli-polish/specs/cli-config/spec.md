## ADDED Requirements

### Requirement: Model shorthands
The generated `config.toml` SHALL include a `[models]` block mapping short aliases to
full model names, and the system SHALL resolve a configured shorthand to its full model
name wherever a model is referenced. Full model names SHALL continue to be accepted
directly.

#### Scenario: Shorthand resolves to full model name
- **WHEN** a model is referenced by a configured shorthand
- **THEN** the system SHALL resolve it to the corresponding full model name

#### Scenario: Full model name still accepted
- **WHEN** a model is referenced by its full name
- **THEN** the system SHALL use it directly without requiring a shorthand

### Requirement: Init subcommands
The system SHALL provide `rally init` (workspace init), `rally init models` (add/update
the model-shorthand block), `rally init roles` (role init), and `rally init all` (run the
three in sequence). Each subcommand SHALL be independently re-runnable, merging into an
existing config without discarding unrelated sections.

#### Scenario: init all runs the sequence
- **WHEN** `rally init all` is invoked
- **THEN** the system SHALL run workspace init, model-shorthand init, and role init in sequence

#### Scenario: init models is scoped
- **WHEN** `rally init models` is invoked on an existing config
- **THEN** the system SHALL add or update only the `[models]` block and leave other sections intact

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
