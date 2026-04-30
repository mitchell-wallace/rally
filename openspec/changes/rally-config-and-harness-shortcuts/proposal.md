## Why

`.rally/config.toml` already exists (`internal/config/config_v2.go`) but its schema is narrow: per-harness model strings, beads toggle, data dir, autocommit-hooks bool. Two gaps block richer agent mixes and downstream releases:

1. **Mix syntax conflates harness and model.** To rotate between two opencode-routed providers (`zai-coding-plan/glm-5.1` and `opencode-go/kimi-k2.6`) the user has to type the full model strings in every mix, and rally has no way to refer to "the GLM provider via opencode" as a stable handle. This blocks v0.6.0 role routing (which needs route entries) and v0.7.0 provider rotation (which needs to refer to alternatives).
2. **No place for repo-defaults beyond per-harness models.** Default iteration count, default mix, beads-instructions toggle, and fallback prompts all live in CLI flags today. Every relay invocation re-types them.

This change extends the existing TOML schema with named harness:provider shortcuts, repo-level defaults, beads-instructions toggle, and fallback prompts. It also retires the legacy env-style `.rally/config` (which v0.2.x already superseded with `config.toml` for everything except `RALLY_DATA_DIR`).

## What Changes

### Harness:provider shortcuts
- New `[providers]` table in `.rally/config.toml`:
  ```toml
  [providers."op:z"]
  harness = "opencode"
  model   = "zai-coding-plan/glm-5.1"

  [providers."op:gk"]
  harness = "opencode"
  model   = "opencode-go/kimi-k2.6"
  ```
- Mix syntax accepts both raw `harness:model` and shorthand keys: `--mix "claude,op:z,op:gk,gemini"`
- Rally validates shortcut keys at config load; unresolved keys error early with a `did-you-mean` suggestion
- `harness` and `model` are surfaced as separate fields throughout the executor layer (today they're sometimes joined as a single string)

### Repo-local config (`.rally/config.toml`)
- `[defaults]`: `iterations`, `mix`, `verbose`
- `[microbeads]`: `instructions = "auto" | "include" | "skip"`, `instructions_file = ".rally/microbeads_instructions.md"` — replaces the misleadingly-named `beads = "auto"` flat field (renamed in v0.4.0). No backend selector — microbeads is the only first-class tracker.
- `[fallback]`: `instructions_file = ".rally/fallback.md"` — used in no-backend mode (when microbeads isn't active) so the agent still has a useful prompt
- `[providers]`: shortcut table (above)
- Existing flat fields (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`) are preserved at the file root; new sections are additive
- CLI flags continue to override config values
- Legacy env-style `.rally/config` is removed outright — it only ever stored `RALLY_DATA_DIR` defaults, never used in practice; no migration path, no warning

### Beads-instructions toggle
- `auto` (default): inject beads instructions if no `CLAUDE.md`/`AGENTS.md` mentioning `bd`/`br`/`bv` is detected in the workspace
- `include`: always inject
- `skip`: never inject
- The injected text comes from `instructions_file` if present, else a built-in default

### Fallback instructions
- When no ready bead exists, rally injects the contents of `[fallback].instructions_file` instead of the bead body
- Built-in default fallback retained if no file is configured
- Replaces today's hard-coded fallback prompt

## Capabilities

### New Capabilities
- `provider-shortcuts`: Named harness+model bindings declared in `.rally/config.toml` and usable in mix syntax
- `repo-config`: `.rally/config.toml` with `[defaults]`, `[beads]`, `[fallback]`, `[providers]` sections — overridable via CLI/env

### Modified Capabilities
- `executor`: Mix parsing accepts shorthand keys; harness and model surfaced as separate fields throughout
- `relay-runner`: Reads defaults from config; injects beads/fallback instructions per the configured policy

## Impact

- Extends `internal/config/config_v2.go` schema (or splits it into `internal/config/v3.go` if the change is large enough to warrant)
- Existing `.rally/config.toml` files continue to load; new sections default to zero/sensible defaults
- Legacy env-style `.rally/config` deleted from the codebase — no loader, no migration
- v0.6.0 dependency: role routing references shortcut keys in route entries (and rolls `routes.yml` into the same TOML)
- v0.7.0 dependency: provider rotation needs shortcut keys to refer to alternatives
- Risk: TOML schema drift across releases — pin a `schema_version` field at the file root, warn on mismatch
