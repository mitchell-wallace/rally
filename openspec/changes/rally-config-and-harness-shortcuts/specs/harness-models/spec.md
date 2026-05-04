## ADDED Requirements

### Requirement: `[harness.<name>.models]` named-model table
The system SHALL accept a `[harness.<name>.models]` sub-table in `.rally/config.toml` mapping model names (alphanumeric identifiers, non-numeric) to model strings. The harness `<name>` SHALL be a built-in harness alias (`cc`/`cx`/`ge`/`op` or their long forms) or a user-defined harness registered elsewhere in the same config. Numeric-only model names SHALL be rejected at config load.

#### Scenario: Valid named-model entries
- **WHEN** `.rally/config.toml` contains `[harness.op.models]\nz = "zai-coding-plan/glm-5.1"\ngk = "opencode-go/kimi-k2.6"`
- **THEN** the loader SHALL register `op:z` resolving to `(harness=opencode, model=zai-coding-plan/glm-5.1)` and `op:gk` resolving to `(harness=opencode, model=opencode-go/kimi-k2.6)`

#### Scenario: Numeric-only model name rejected
- **WHEN** `.rally/config.toml` contains a model name that is purely numeric (e.g. `[harness.op.models]\n4 = "..."`)
- **THEN** config load SHALL exit non-zero with an error naming the offending name and explaining that numeric-only model names conflict with weight syntax

#### Scenario: Unknown harness rejected
- **WHEN** a `[harness.<name>.models]` block declares a `<name>` that is neither a built-in alias nor a user-defined harness in the same config
- **THEN** config load SHALL exit non-zero with an error naming the offending entry and listing valid harness names

### Requirement: Resolution at parse time
The system SHALL resolve every `harness:model-name` reference in mix syntax (and in any downstream consumer such as routes or rotation lists) at config-load time, not at first-use. Unresolved names SHALL produce a `did-you-mean` error referencing the closest-matching defined names within the same harness.

#### Scenario: Mix references defined named models
- **WHEN** `--mix "claude,op:z,op:gk,gemini"` is parsed and `op:z` and `op:gk` are defined under `[harness.op.models]`
- **THEN** the parser SHALL expand each named entry into its `(harness, model)` tuple and surface the four agents to the relay runner

#### Scenario: Mix references an undefined named model
- **WHEN** `--mix` references a `harness:model-name` whose model name is not defined under that harness's `models` table
- **THEN** parsing SHALL exit non-zero with a `did-you-mean` message listing up to three closest-matching defined names *within the same harness*

### Requirement: Right-of-colon disambiguation
The system SHALL distinguish weight-on-bare-harness from named-model entries purely by the right-hand side of the colon: an all-digits right side denotes a weight; an alphanumeric identifier denotes a model name. A third colon-separated segment SHALL be rejected in v0.5.0.

#### Scenario: Weight syntax preserved
- **WHEN** a mix contains `cc:2`
- **THEN** the parser SHALL treat `2` as a weight on the `claude` harness with the unnamed default model (per the `claude_model` flat field), as in v0.2.x

#### Scenario: Named-model syntax
- **WHEN** a mix contains `cc:opus`
- **THEN** the parser SHALL resolve `opus` against `[harness.cc.models]` and produce `(harness=claude, model=<resolved>)`

#### Scenario: Third segment rejected
- **WHEN** a mix contains `cc:opus:2`
- **THEN** parsing SHALL exit non-zero with an error explaining that weight-on-named-model is not supported in v0.5.0

### Requirement: Harness and model surface as separate fields
The system SHALL surface `harness` and `model` as separate fields throughout the executor invocation path (rather than as a single joined string). Resolution from a named entry, a raw `harness:model-string`, or a bare alias plus flat-field default SHALL all produce the same downstream representation.

#### Scenario: Named and raw forms produce identical resolution
- **WHEN** one mix entry uses `op:z` (named model) and another uses the raw form `opencode:zai-coding-plan/glm-5.1`
- **THEN** both SHALL resolve to the same `(harness="opencode", model="zai-coding-plan/glm-5.1")` tuple downstream

#### Scenario: Bare alias uses flat-field default
- **WHEN** a mix contains `cc` and `claude_model = "claude-opus-4-7"` is set at the config root
- **THEN** the parser SHALL produce `(harness="claude", model="claude-opus-4-7")`

### Requirement: User-defined harnesses via templated `command`
The system SHALL accept a `[harness.<name>]` entry that registers a new harness when it declares a `command` field (an array of strings). The command SHALL be invoked with `$MODEL` substituted to the resolved model string and `$PROMPT` substituted to the prompt body; if `$PROMPT` does not appear in `command`, the prompt SHALL be piped to the harness on stdin. Built-in harnesses SHALL NOT declare `command` or `output_strategy` — the loader SHALL reject either field on a built-in entry.

#### Scenario: User harness registered and invoked
- **GIVEN** `.rally/config.toml` contains:
  ```
  [harness.droid]
  command         = ["droid", "run", "--model", "$MODEL"]
  output_strategy = "tail"
  output_lines    = 40

  [harness.droid.models]
  default = "droid-v1"
  ```
- **WHEN** a relay run is dispatched with `(harness=droid, model=droid-v1)`
- **THEN** the system SHALL exec `droid run --model droid-v1` with the prompt piped on stdin, and surface the last 40 lines of combined stdout+stderr as the run output

#### Scenario: `$PROMPT` substituted as argument
- **GIVEN** `[harness.droid] command = ["droid", "ask", "--model", "$MODEL", "--prompt", "$PROMPT"]`
- **WHEN** a relay run is dispatched
- **THEN** the system SHALL exec the command with `$PROMPT` replaced by the prompt body and SHALL NOT pipe to stdin

#### Scenario: Built-in rejects `command` field
- **WHEN** `.rally/config.toml` contains `[harness.cc] command = [...]`
- **THEN** config load SHALL exit non-zero with an error explaining that built-in harnesses do not accept `command` or `output_strategy`

### Requirement: Tail output strategy with configurable line count
The system SHALL implement an output strategy named `"tail"` for user-defined harnesses that captures the last N lines of combined stdout+stderr, where N is `output_lines` (default 40). Other `output_strategy` values SHALL be rejected at config load in v0.5.0.

#### Scenario: Default line count
- **WHEN** a user-defined harness declares `output_strategy = "tail"` and omits `output_lines`
- **THEN** the parser SHALL default to 40 lines

#### Scenario: Custom line count
- **WHEN** a user-defined harness declares `output_strategy = "tail"` and `output_lines = 100`
- **THEN** the parser SHALL surface the last 100 lines

#### Scenario: Unknown strategy rejected
- **WHEN** a user-defined harness declares `output_strategy = "json"` (or any value other than `"tail"`)
- **THEN** config load SHALL exit non-zero with an error listing the supported strategies
