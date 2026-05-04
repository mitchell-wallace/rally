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
- `[microbeads]`: `instructions_file = ".rally/microbeads_instructions.md"` — sources the microbeads-instruction content rally injects when in microbeads-backed mode. There is no `auto`/`include`/`skip` toggle — per v0.4.0, injection is unconditional in microbeads-backed mode and absent in no-backend mode. The legacy `Beads` flat field is removed outright (not renamed).
- `[fallback]`: `instructions_file = ".rally/fallback.md"` — used in no-backend mode when no ready bead exists so the agent still has a useful prompt
- `[providers]`: shortcut table (above)
- Top-level `schema_version` int (`2` in v0.5.0); absent treated as version 1, mismatch warns
- Existing flat fields (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`) are preserved at the file root; new sections are additive
- CLI flags continue to override config values
- Legacy env-style `.rally/config` is removed outright — it only ever stored `RALLY_DATA_DIR` defaults, never used in practice; no migration path, no warning

### Fallback instructions
- When no ready bead exists in no-backend mode, rally injects the contents of `[fallback].instructions_file` instead of the bead body
- Built-in default fallback retained if no file is configured or the configured path is unreadable
- Replaces today's hard-coded fallback prompt

## Capabilities

### New Capabilities
- `provider-shortcuts`: Named `harness:model` bindings declared in `[providers]` and resolvable in mix syntax (and in v0.6.0 routes / v0.7.0 rotation)
- `repo-config`: `.rally/config.toml` with `[defaults]`, `[microbeads]`, `[fallback]`, `[providers]` sections plus `schema_version`, with v0.2.x flat fields preserved at the root

### Modified Capabilities
- `relay-runner`: Mix parsing extended to resolve shortcut keys via the config layer; defaults sourced from `[defaults]`; fallback prompt content sourced from `[fallback].instructions_file` in no-backend mode

## Impact

- Extends `internal/config/config_v2.go` schema (or splits it into `internal/config/v3.go` if the change is large enough to warrant)
- Existing `.rally/config.toml` files continue to load; new sections default to zero/sensible defaults
- Legacy env-style `.rally/config` deleted from the codebase — no loader, no migration. `RALLY_DATA_DIR` env var continues to work
- v0.4.0 alignment: no microbeads-instruction toggle (injection is unconditional in microbeads-backed mode); legacy `Beads` flat field removed outright
- v0.6.0 dependency: role routing references shortcut keys in route entries (and rolls `routes.yml` into the same TOML)
- v0.7.0 dependency: provider rotation needs shortcut keys to refer to alternatives
- Risk: TOML schema drift across releases — pinned `schema_version = 2`, warn-then-load on mismatch in v0.5.0; v0.6.0+ may tighten
