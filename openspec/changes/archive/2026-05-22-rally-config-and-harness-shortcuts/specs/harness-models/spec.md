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

### Requirement: User-defined harnesses via `command` and declarative `model_flag`
The system SHALL accept a `[harness.<name>]` entry that registers a new harness when it declares a `command` field (an array of strings). The command SHALL be invoked with `$PROMPT` substituted to the prompt body if `$PROMPT` appears anywhere in `command`; otherwise the prompt SHALL be piped to the harness on stdin. Substitution is positional (no shell interpolation): each command element is replaced verbatim if it equals `$PROMPT`, and partial matches (`prefix-$PROMPT`) are also substituted. The model is injected declaratively via `model_flag`, not via a placeholder in `command`. Built-in harnesses SHALL NOT declare `command`, `model_flag`, `output_strategy`, or `tail_stream` — the loader SHALL reject any of those fields on a built-in entry.

#### Scenario: User harness registered and invoked
- **GIVEN** `.rally/config.toml` contains:
  ```
  [harness.droid]
  command         = ["droid", "run"]
  model_flag      = "--model"
  output_strategy = "tail"
  output_lines    = 40

  [harness.droid.models]
  default = "droid-v1"
  ```
- **WHEN** a relay run is dispatched with `(harness=droid, model=droid-v1)`
- **THEN** the system SHALL exec `droid run --model droid-v1` with the prompt piped on stdin, and surface the last 40 lines of combined stdout+stderr as the run output

#### Scenario: `$PROMPT` substituted as argument
- **GIVEN** `[harness.droid] command = ["droid", "ask", "--prompt", "$PROMPT"]` and `model_flag = "--model"`
- **WHEN** a relay run is dispatched with `model=droid-v1`
- **THEN** the system SHALL exec `droid ask --prompt <prompt-body> --model droid-v1` and SHALL NOT pipe to stdin

#### Scenario: `$MODEL` placeholder rejected at config load
- **WHEN** `.rally/config.toml` contains a `command` array with any element containing the literal `$MODEL`
- **THEN** config load SHALL exit non-zero with an error explaining that the model is injected via `model_flag`, not a placeholder

#### Scenario: Built-in rejects `command` field
- **WHEN** `.rally/config.toml` contains `[harness.cc] command = [...]`
- **THEN** config load SHALL exit non-zero with an error explaining that built-in harnesses do not accept `command`, `model_flag`, `output_strategy`, or `tail_stream`

### Requirement: `model_flag` controls model injection
The system SHALL append the resolved model to a user-defined harness's command according to the harness's `model_flag` setting:
- If `model_flag` is set to a non-empty string AND a model is resolved, the system SHALL append `[model_flag, resolved_model]` to the command.
- If `model_flag` is set to an empty string AND a model is resolved, the system SHALL append `[resolved_model]` (positional, no flag).
- If `model_flag` is omitted from the harness configuration, the system SHALL NOT append the model under any circumstance, even if a model is resolved.
- If no model is resolved (bare alias with no flat-field default), the system SHALL NOT append the model regardless of `model_flag`.

When a run has a non-empty resolved model AND the dispatched harness has `model_flag` unset, the system SHALL log a one-line informational note that the model could not be passed and the harness's own default will be used.

#### Scenario: Flag-and-value appended
- **GIVEN** `[harness.droid] command = ["droid", "run"]` and `model_flag = "--model"`
- **WHEN** a run dispatches with `model=droid-v1`
- **THEN** the system SHALL exec `droid run --model droid-v1`

#### Scenario: Positional model appended
- **GIVEN** `[harness.droid] command = ["droid", "run"]` and `model_flag = ""`
- **WHEN** a run dispatches with `model=droid-v1`
- **THEN** the system SHALL exec `droid run droid-v1`

#### Scenario: Model-flag omitted with model resolved
- **GIVEN** `[harness.droid] command = ["droid", "run"]` with no `model_flag` field
- **WHEN** a run dispatches with `model=droid-v1`
- **THEN** the system SHALL exec `droid run` and SHALL log a one-line note that the model could not be passed

#### Scenario: No model resolved
- **GIVEN** `[harness.droid] command = ["droid", "run"]` and `model_flag = "--model"`
- **WHEN** a run dispatches with no resolved model (bare alias against an unset flat-field default)
- **THEN** the system SHALL exec `droid run` (model not appended, harness applies its own default)

### Requirement: Tail output strategy with configurable line count and stream
The system SHALL implement an output strategy named `"tail"` for user-defined harnesses that captures the last N lines of the configured stream, where N is `output_lines` (default 40) and the stream is selected by `tail_stream`: `"stdout"`, `"stderr"`, or `"combined"` (default). Other `output_strategy` values SHALL be rejected at config load in v0.5.0. Other `tail_stream` values SHALL also be rejected at config load.

#### Scenario: Default line count and stream
- **WHEN** a user-defined harness declares `output_strategy = "tail"` and omits `output_lines` and `tail_stream`
- **THEN** the parser SHALL default to 40 lines of combined stdout+stderr

#### Scenario: Custom line count
- **WHEN** a user-defined harness declares `output_strategy = "tail"` and `output_lines = 100`
- **THEN** the parser SHALL surface the last 100 lines of combined stdout+stderr

#### Scenario: Stderr-only capture
- **WHEN** a user-defined harness declares `tail_stream = "stderr"`
- **THEN** the parser SHALL surface the last `output_lines` lines of stderr only, ignoring stdout

#### Scenario: Unknown strategy rejected
- **WHEN** a user-defined harness declares `output_strategy = "json"` (or any value other than `"tail"`)
- **THEN** config load SHALL exit non-zero with an error listing the supported strategies

#### Scenario: Unknown stream rejected
- **WHEN** a user-defined harness declares `tail_stream = "both"` (or any value other than `stdout`/`stderr`/`combined`)
- **THEN** config load SHALL exit non-zero with an error listing the supported streams
