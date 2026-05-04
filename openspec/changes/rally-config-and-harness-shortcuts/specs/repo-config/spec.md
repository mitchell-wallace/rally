## ADDED Requirements

### Requirement: `.rally/config.toml` schema with `[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]`
The system SHALL accept the following sections in `.rally/config.toml`, in addition to the existing v0.2.x flat fields at the file root:

- `[defaults]` — `iterations` (int), `mix` (string); each optional, used when the corresponding CLI flag is not supplied
- `[microbeads]` — `instructions_file` (path); optional, used to source microbeads-instruction content when in microbeads-backed mode
- `[fallback]` — `instructions_file` (path); optional, used in no-backend mode when no ready bead exists
- `[harness.<name>]` — per-harness configuration: `models` sub-table for named models; `command` (array of strings), `model_flag` (string, optional — controls how the resolved model is appended), `output_strategy` (string), `output_lines` (int), and `tail_stream` (string, one of `stdout`/`stderr`/`combined`, default `combined`) for user-defined harnesses (see `harness-models` capability)

CLI flags SHALL continue to override config values. Existing flat fields (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`) SHALL remain at the file root and SHALL be honoured as the unnamed default model for each built-in harness.

#### Scenario: Defaults sourced from config
- **WHEN** `.rally/config.toml` contains `[defaults]\niterations = 25\nmix = "claude,codex"` and `rally relay` is invoked without `--iterations` or `--mix` flags
- **THEN** the relay SHALL use 25 iterations and the configured mix

#### Scenario: CLI flag overrides config default
- **WHEN** `[defaults].iterations = 25` is set and `rally relay --iterations 5` is invoked
- **THEN** the relay SHALL use 5 iterations

#### Scenario: Existing flat fields still loaded
- **WHEN** `.rally/config.toml` contains a top-level `claude_model = "claude-opus-4-7"` field with no other sections
- **THEN** the loader SHALL accept it and apply the model to the claude executor as in v0.2.x; a bare `claude` alias in any mix SHALL resolve to `(claude, claude-opus-4-7)`

### Requirement: `schema_version` field with warn-on-mismatch
The system SHALL recognise a top-level `schema_version` integer field in `.rally/config.toml`. v0.5.0 expects `schema_version = 2`. If absent, the loader SHALL treat the file as version 1 and load it without warning. If present but mismatched, the loader SHALL log a one-line warning naming the expected version and proceed with load. Every config write SHALL emit `schema_version = 2`.

#### Scenario: Version absent (legacy file)
- **WHEN** `.rally/config.toml` has no `schema_version` field
- **THEN** the loader SHALL accept it without warning (treated as version 1) and load successfully

#### Scenario: Version mismatch
- **WHEN** `.rally/config.toml` declares `schema_version = 99`
- **THEN** the loader SHALL log a warning naming the expected version (`2`) and proceed to load the file

#### Scenario: Version emitted on write
- **WHEN** rally writes `.rally/config.toml` (e.g. via a future `rally config set` or any internal write path)
- **THEN** the written file SHALL include `schema_version = 2` at the root

### Requirement: Microbeads instruction content source
The system SHALL source the content of microbeads-instruction injection from `[microbeads].instructions_file` when defined and readable. When absent, missing, or unreadable, the system SHALL fall back to a built-in default. The decision of *whether* to inject lives in mode detection (per `microbeads-only-integration` capability, v0.4.0), not in this config section. A warning for a missing/unreadable path SHALL be emitted at first use, not at config load.

#### Scenario: Configured instructions file used
- **WHEN** `[microbeads].instructions_file = ".rally/microbeads_instructions.md"` is set, the file exists and is readable, and rally is in microbeads-backed mode
- **THEN** the prompt SHALL include the file's contents as the microbeads instructions

#### Scenario: Configured file missing
- **WHEN** `[microbeads].instructions_file` references a path that does not exist or cannot be read, AND rally enters microbeads-backed mode
- **THEN** rally SHALL log a warning naming the missing path on first use and SHALL fall back to its built-in default microbeads-instruction content

### Requirement: Fallback prompt content source for no-backend mode
The system SHALL substitute `[fallback].instructions_file` content for the bead body when (a) rally is in no-backend mode AND (b) no ready bead exists for this iteration. When the configured file is absent, missing, or unreadable, rally SHALL use a built-in default fallback prompt. In microbeads-backed mode, this section SHALL have no effect.

#### Scenario: No-backend mode with no ready bead
- **WHEN** rally starts an iteration in no-backend mode and no ready bead exists
- **THEN** the prompt SHALL include the contents of `[fallback].instructions_file` (or the built-in default) in place of the bead body

#### Scenario: Microbeads-backed mode ignores fallback
- **WHEN** rally is in microbeads-backed mode and a ready bead exists
- **THEN** the prompt SHALL include the bead body and SHALL NOT inject the fallback file content
