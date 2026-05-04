## Why

`.rally/config.toml` already exists (`internal/config/config_v2.go`) but its schema is narrow: per-harness model strings, data dir, autocommit-hooks bool. Three gaps block richer agent mixes and downstream releases:

1. **No way to give a model a memorable name.** To rotate between two opencode-routed providers (`zai-coding-plan/glm-5.1` and `opencode-go/kimi-k2.6`) the user types the full model string in every mix, and rally has no stable handle for "the GLM model under opencode." This blocks v0.6.0 role routing (which needs route entries) and v0.7.0 provider rotation (which needs to refer to alternatives).
2. **No place for repo-defaults beyond per-harness models.** Default iteration count, default mix, microbeads-instruction content path, and fallback prompts all live in CLI flags or hard-coded constants today. Every relay invocation re-types them.
3. **No way to add a harness without recompiling rally.** Today the four built-in harnesses (`cc`/`cx`/`ge`/`op`) are the only options. New CLI agents (`droid`, etc.) require a code change.

This change extends the existing TOML schema with named per-harness models, repo-level defaults, fallback prompts, and a place to declare user-added harnesses with a generic output parser. Built-in harnesses keep their hard-coded behaviour; the schema is purely additive.

## What Changes

### Harnesses and named models

- New `[harness.<name>]` tables in `.rally/config.toml`. Built-in harnesses (`cc`, `cx`, `ge`, `op`) work without any entry; entries are needed only to add named models or to register a new harness.
- Each harness can have a `[harness.<name>.models]` sub-table mapping a model name (alphanumeric identifier, non-numeric) to a model string:
  ```toml
  [harness.op.models]
  z  = "zai-coding-plan/glm-5.1"
  gk = "opencode-go/kimi-k2.6"

  [harness.cc.models]
  opus   = "claude-opus-4-7"
  sonnet = "claude-sonnet-4-6"
  ```
- Mix syntax accepts three forms in any combination: weighted alias (`cc:2 cx:1` — unchanged), bare alias (`claude`), and `harness:model-name` (`op:z`, `cc:opus`).
- Right-of-colon disambiguation: **all-digits → weight on bare harness; identifier → model name under that harness.** Model names must be non-numeric.
- Weights do not apply to model-named entries in v0.5.0. To use a named model multiple times in a cycle, list it multiple times. Quota-on-models is a v0.6.0 concern.
- Resolution happens at config load. Unresolved model names produce a `did-you-mean` error citing the closest-matching defined names under the same harness.
- `harness` and `model` surface as separate fields throughout the executor layer — `AgentMix.Cycle` becomes a slice of typed `(harness, model)` records rather than a flat slice of strings.

### User-defined harnesses

- A `[harness.<name>]` entry that declares a `command` field registers a new harness:
  ```toml
  [harness.droid]
  command         = ["droid", "run"]
  model_flag      = "--model"               # rally appends [model_flag, model] when a model resolves
  output_strategy = "tail"
  output_lines    = 40
  tail_stream     = "combined"              # "stdout" | "stderr" | "combined" (default)

  [harness.droid.models]
  default = "droid-v1"
  glm     = "glm-5.1"
  ```
- The `command` array is the base invocation. `$PROMPT` (if present in `command`) is replaced with the prompt body at run time; if absent, the prompt is piped on stdin. There is no `$MODEL` placeholder — the model is injected declaratively via `model_flag` instead, which is more predictable and removes the need for fragile heuristics.
- `model_flag` controls how the resolved model is appended to `command`:
  - `model_flag = "--model"` (or any non-empty string) → rally appends `[model_flag, resolved_model]` to the command when a model is resolved.
  - `model_flag = ""` (explicit empty string) → rally appends `[resolved_model]` (positional, no flag).
  - `model_flag` omitted → rally never appends a model; the harness uses its own default. This is the path for "bare alias with no flat-field default."
- `output_strategy = "tail"` is the only strategy in v0.5.0; `output_lines` defaults to 40 if omitted. The tail parser captures the last N lines of the configured stream and surfaces them as the run's output.
- `tail_stream` selects which stream the tail parser captures: `"stdout"`, `"stderr"`, or `"combined"` (default). Useful when a CLI spams progress on one stream and emits the answer on the other.
- Built-in harnesses (`cc`/`cx`/`ge`/`op`) reject `command`, `model_flag`, `output_strategy`, and `tail_stream` fields at config load — their behaviour stays hard-coded. They may still declare `[harness.X.models]`.
- The generic executor lives next to the built-in executors in `internal/agent/` for proximity and shared utilities.

### Repo-local config (`.rally/config.toml`)

- `[defaults]`: `iterations` (int), `mix` (string), `claude_model` / `codex_model` / `gemini_model` / `opencode_model` (string) — these four are the unnamed default model for each built-in harness, referenced by a bare alias in a mix.
- `[microbeads]`: `instructions_file = ".rally/microbeads_instructions.md"` — sources microbeads-instruction content rally injects when in microbeads-backed mode. Per v0.4.0, injection is unconditional in microbeads-backed mode and absent in no-backend mode; there is no toggle.
- `[fallback]`: `instructions_file = ".rally/fallback.md"` — used in no-backend mode when no ready bead exists.
- `[harness.*]`: per-harness configuration (named models, optionally `command`/`model_flag`/output strategy/tail stream for user harnesses).
- Top-level `schema_version` int (`2` in v0.5.0); absent treated as version 1, mismatch warns.
- Top-level `data_dir` and `run_hooks_on_autocommit` (and `laps_instructions`) remain at the root — these are workspace runtime knobs, not per-harness model defaults, so the move does not apply to them.
- Backwards-compat: v0.5.0 also reads root-level `claude_model` / `codex_model` / `gemini_model` / `opencode_model` if present (the v0.2.x location). When a value comes from a root-level field, rally logs a one-line deprecation note pointing to `[defaults]`. `[defaults]` takes precedence on conflict. Every config write emits the new shape with `schema_version = 2`.
- CLI flags continue to override config values.

### Example config on `rally init`

`rally init` writes `.rally/config.toml` when none exists. The template is updated to use the new shape so first-time users land on the canonical layout:

```toml
schema_version = 2

[defaults]
iterations     = 25
# mix          = "claude,codex"
claude_model   = ""
codex_model    = ""
gemini_model   = ""
opencode_model = ""

# Workspace runtime
data_dir                = ""
run_hooks_on_autocommit = false
laps_instructions       = ""
```

### Fallback instructions

- When no ready bead exists in no-backend mode, rally injects the contents of `[fallback].instructions_file` instead of the bead body.
- Built-in default fallback retained if no file is configured or the configured path is unreadable.
- Replaces today's hard-coded fallback prompt.

## Capabilities

### New Capabilities

- `harness-models`: per-harness named model bindings under `[harness.<name>.models]`, plus user-defined harnesses via `[harness.<name>]` with a templated `command`, a tail-N output parser, and configurable stream capture.
- `repo-config`: `.rally/config.toml` with `[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]` sections plus `schema_version`, with v0.2.x flat fields preserved at the root.

### Modified Capabilities

- `relay-runner`: Mix parsing extended to resolve `harness:model-name` entries via the config layer; `AgentMix.Cycle` re-typed to carry resolved `(harness, model)` records; defaults sourced from `[defaults]`; fallback prompt content sourced from `[fallback].instructions_file` in no-backend mode; user-defined harnesses dispatched through the templated-command + tail-parser path.

## Impact

- Extends `internal/config/config_v2.go` schema (or splits into `v3.go` if the diff warrants).
- Adds a generic harness executor in `internal/agent/` that runs a templated command and applies the tail-N output parser with configurable stream selection.
- `AgentMix.Cycle []string` is replaced with a typed slice of resolved-agent records. Every caller of the cycle is updated.
- Updates `runInit` ([cmd/rally/main.go:236](cmd/rally/main.go#L236)) so the example config it writes uses the new `[defaults]`-shaped layout including the four model fields.
- Existing `.rally/config.toml` files continue to load; root-level `claude_model`/`codex_model`/`gemini_model`/`opencode_model` still resolve (with a deprecation note); new sections default to zero/sensible defaults.
- v0.4.0 alignment: no microbeads-instruction toggle (injection is unconditional in microbeads-backed mode); legacy `Beads` flat field already removed in v0.4.0.
- **Depends on v0.4.0 (microbeads-first-class) landing first** — no-backend / microbeads-backed mode detection and the `Beads`-field removal originate there.
- v0.6.0 dependency: role routing references `harness:model-name` in route entries (and rolls `routes.yml` into the same TOML).
- v0.7.0 dependency: provider rotation refers to model names when in-route advancing.
- Risk: TOML schema drift across releases — pinned `schema_version = 2`, warn-then-load on mismatch in v0.5.0; v0.6.0+ may tighten.
- Note: the legacy env-style `.rally/config` file has no loader in tree (verified); no work needed there.
