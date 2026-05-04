## ADDED Requirements

### Requirement: `[providers]` shortcut table
The system SHALL accept a `[providers]` table in `.rally/config.toml` where each entry is a TOML sub-table keyed by an alphanumeric identifier (with optional `:`, `_`, `-`) and containing `harness` and `model` string fields. Numeric-only keys SHALL be rejected at config load.

#### Scenario: Valid shortcut entries
- **WHEN** `.rally/config.toml` contains `[providers."op:z"]\nharness = "opencode"\nmodel = "zai-coding-plan/glm-5.1"`
- **THEN** the loader SHALL register `op:z` as a shortcut resolving to `(harness=opencode, model=zai-coding-plan/glm-5.1)`

#### Scenario: Numeric-only key rejected
- **WHEN** `.rally/config.toml` contains `[providers."4"]` or any other purely-numeric key
- **THEN** config load SHALL exit non-zero with an error naming the offending key and explaining that numeric-only shortcut keys are reserved for quota syntax

#### Scenario: Unknown harness rejected
- **WHEN** a `[providers]` entry declares `harness = "unrecognised"` (not in the supported harness list)
- **THEN** config load SHALL exit non-zero with an error naming the offending entry and listing the supported harnesses

### Requirement: Shortcut resolution at parse time
The system SHALL resolve every shortcut reference in mix syntax (and in any downstream consumer such as routes or rotation lists) at config-load time, not at first-use. Unresolved keys SHALL produce a `did-you-mean` error referencing the closest-matching defined keys.

#### Scenario: Mix references a defined shortcut
- **WHEN** `--mix "claude,op:z,op:gk,gemini"` is parsed and `op:z` and `op:gk` are defined in `[providers]`
- **THEN** the parser SHALL expand each shortcut into its `(harness, model)` tuple and surface the four agents to the relay runner

#### Scenario: Mix references an undefined shortcut
- **WHEN** `--mix` references a shortcut key that is not defined in `[providers]`
- **THEN** parsing SHALL exit non-zero with a `did-you-mean` message listing up to three closest-matching defined keys

### Requirement: Harness and model surface as separate fields
The system SHALL surface `harness` and `model` as separate fields throughout the executor invocation path (rather than as a single joined string). Resolution from a shortcut or a raw `harness:model` string SHALL produce the same downstream representation.

#### Scenario: Raw and shortcut forms produce identical resolution
- **WHEN** one mix entry uses `op:z` (a shortcut) and another uses the raw form `opencode:zai-coding-plan/glm-5.1`
- **THEN** both SHALL resolve to the same `(harness="opencode", model="zai-coding-plan/glm-5.1")` tuple downstream
