## ADDED Requirements

### Requirement: `.rally/config.toml` schema with `[defaults]`, `[laps]`, `[fallback]`, `[harness.*]`
The system SHALL accept the following sections in `.rally/config.toml`:

- `[defaults]` — `iterations` (int), `mix` (string), `claude_model` / `codex_model` / `gemini_model` / `opencode_model` (string); each optional. The four model fields are the unnamed default model for each built-in harness, referenced by a bare alias in a mix. `iterations` and `mix` are used when the corresponding CLI flag is not supplied.
- `[laps]` — `instructions_file` (path); optional, used to source laps-instruction content when in laps-backed mode
- `[fallback]` — `instructions_file` (path); optional, used in no-backend mode when no ready lap exists
- `[harness.<name>]` — per-harness configuration: `models` sub-table for named models; `command` (array of strings), `model_flag` (string, optional — controls how the resolved model is appended), `output_strategy` (string), `output_lines` (int), and `tail_stream` (string, one of `stdout`/`stderr`/`combined`, default `combined`) for user-defined harnesses (see `harness-models` capability)

The following root-level fields SHALL remain at the file root: `data_dir`, `run_hooks_on_autocommit`, `laps_instructions`, and `schema_version`. These are workspace runtime knobs, not per-harness defaults.

CLI flags SHALL continue to override config values.

#### Scenario: Defaults sourced from config
- **WHEN** `.rally/config.toml` contains `[defaults]\niterations = 25\nmix = "claude,codex"` and `rally relay` is invoked without `--iterations` or `--mix` flags
- **THEN** the relay SHALL use 25 iterations and the configured mix

#### Scenario: CLI flag overrides config default
- **WHEN** `[defaults].iterations = 25` is set and `rally relay --iterations 5` is invoked
- **THEN** the relay SHALL use 5 iterations

#### Scenario: Bare alias resolves through `[defaults]`
- **WHEN** `.rally/config.toml` contains `[defaults]\nclaude_model = "claude-opus-4-7"` and a mix entry `cc` is supplied
- **THEN** the parser SHALL resolve `cc` to `(claude, claude-opus-4-7)`

### Requirement: Backwards-compatible read of v0.2.x root-level model fields
The system SHALL continue to read root-level `claude_model`, `codex_model`, `gemini_model`, and `opencode_model` fields if present (the v0.2.x location), so that existing in-the-wild configs continue to work without manual migration. When a model value comes from a root-level field rather than `[defaults]`, the system SHALL log a one-line deprecation note pointing the operator at `[defaults]`. When both a root-level field and the `[defaults]` equivalent are set, `[defaults]` SHALL take precedence. Every config write SHALL emit the new shape (model fields under `[defaults]`); rally SHALL NOT round-trip a root-level model field on write.

#### Scenario: Root-level field still honoured
- **WHEN** `.rally/config.toml` contains a top-level `claude_model = "claude-opus-4-7"` field with no `[defaults].claude_model`
- **THEN** the loader SHALL accept the value, log a one-line deprecation note, and a bare `cc` alias SHALL resolve to `(claude, claude-opus-4-7)`

#### Scenario: `[defaults]` wins on conflict
- **WHEN** `.rally/config.toml` contains both root-level `claude_model = "X"` and `[defaults].claude_model = "Y"`
- **THEN** the loader SHALL use `Y` and SHALL log the deprecation note for the now-shadowed root-level field

#### Scenario: New shape on write
- **WHEN** rally writes `.rally/config.toml` (e.g. via a future `rally config set` or any internal write path) and the in-memory config has `claude_model = "Z"`
- **THEN** the written file SHALL contain `[defaults].claude_model = "Z"` and SHALL NOT contain a root-level `claude_model` field

### Requirement: `rally init` writes a `[defaults]`-shaped example config
The system SHALL write an example `.rally/config.toml` in the new `[defaults]`-shaped layout when `rally init` runs in a workspace that does not yet have one. The example SHALL include a populated `[defaults]` section (with the four model fields and an `iterations` placeholder), the runtime fields at the root, and `schema_version = 2`.

#### Scenario: Init writes new-shape template
- **WHEN** `rally init` runs in a workspace with no existing `.rally/config.toml`
- **THEN** the written file SHALL contain `schema_version = 2`, a `[defaults]` section with `iterations`, `claude_model`, `codex_model`, `gemini_model`, and `opencode_model` keys, and root-level `data_dir`, `run_hooks_on_autocommit`, and `laps_instructions` keys

#### Scenario: Init does not overwrite existing config
- **WHEN** `rally init` runs in a workspace with an existing `.rally/config.toml`
- **THEN** the existing file SHALL NOT be overwritten or modified

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

### Requirement: Laps instruction content source
The system SHALL source the content of laps-instruction injection from `[laps].instructions_file` when defined and readable. When absent, missing, or unreadable, the system SHALL fall back to a built-in default. The decision of *whether* to inject lives in mode detection (per `laps-only-integration` capability, v0.4.0), not in this config section. A warning for a missing/unreadable path SHALL be emitted at first use, not at config load.

#### Scenario: Configured instructions file used
- **WHEN** `[laps].instructions_file = ".rally/laps_instructions.md"` is set, the file exists and is readable, and rally is in laps-backed mode
- **THEN** the prompt SHALL include the file's contents as the laps instructions

#### Scenario: Configured file missing
- **WHEN** `[laps].instructions_file` references a path that does not exist or cannot be read, AND rally enters laps-backed mode
- **THEN** rally SHALL log a warning naming the missing path on first use and SHALL fall back to its built-in default laps-instruction content

### Requirement: Fallback prompt content source for no-backend mode
The system SHALL substitute `[fallback].instructions_file` content for the lap body when (a) rally is in no-backend mode AND (b) no ready lap exists for this iteration. When the configured file is absent, missing, or unreadable, rally SHALL use a built-in default fallback prompt. In laps-backed mode, this section SHALL have no effect.

#### Scenario: No-backend mode with no ready lap
- **WHEN** rally starts an iteration in no-backend mode and no ready lap exists
- **THEN** the prompt SHALL include the contents of `[fallback].instructions_file` (or the built-in default) in place of the lap body

#### Scenario: Laps-backed mode ignores fallback
- **WHEN** rally is in laps-backed mode and a ready lap exists
- **THEN** the prompt SHALL include the lap body and SHALL NOT inject the fallback file content
