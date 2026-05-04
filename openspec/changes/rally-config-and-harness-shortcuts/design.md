## Context

`.rally/config.toml` exists today (`internal/config/config_v2.go`) but its schema is narrow: per-harness model strings, the now-removed `Beads` field, `data_dir`, `run_hooks_on_autocommit`. Every other knob the operator might want to set — default iteration count, default mix, fallback prompt content — lives only as a CLI flag and gets re-typed every relay invocation. The harness:model couple is also opaque: to use `opencode` routed through `zai-coding-plan/glm-5.1`, the operator types the full string in every mix, and rally has no stable handle for "the GLM provider via opencode."

This blocks two downstream releases:
- v0.6.0 role routing needs route entries that reference shortcut keys instead of full harness:model strings.
- v0.7.0 provider rotation needs to refer to alternatives by name when in-route advancing.

A separate concern: `.rally/config` (no extension, env-style `KEY=VALUE` lines) is a v0.1-era loader that v0.2.x already obsoleted by introducing `config.toml`. The only thing it ever stored in practice was `RALLY_DATA_DIR`. It still has a parser in the codebase but has zero documented use; carrying it forward muddles the "TOML is the config format" story.

## Goals / Non-Goals

**Goals:**
- A clean place to declare `harness:model` shortcuts that mix syntax, route entries (v0.6.0), and rotation configs (v0.7.0) can all reference
- A repo-local home for defaults that today only live as CLI flags
- A configured source for fallback-prompt content (used in no-backend mode)
- Drop the legacy `.rally/config` env-style file outright — no migration path, since it had no real users
- Preserve all existing v0.2.x flat fields at the file root for backwards-compat of in-the-wild configs

**Non-Goals:**
- Migrating the progress log to TOML — that file stays YAML per the v0.4.0 deferred decision
- Reintroducing a microbeads-instruction toggle — v0.4.0 decided injection is unconditional in microbeads-backed mode
- A full schema-version migration framework — `schema_version` is recorded for future use but v0.5.0 only emits a warning on mismatch
- Exposing every internal knob as config — only the four sections below; everything else stays code-default

## Decisions

### Shortcut keys live in `[providers]` and are referenced verbatim
**Chosen**: Each shortcut entry is a TOML table under `[providers."<key>"]` with `harness` and `model` fields. Mix syntax accepts the key as a single token (e.g. `--mix "claude,op:z,op:gk"`), and rally resolves it at config load.

**Alternative considered (a)**: Inline shortcuts as a flat map (`[providers] op_z = "opencode/zai-coding-plan/glm-5.1"`).
**Alternative considered (b)**: Define shortcuts in CLI flags only.

**Why**: A nested table makes harness and model addressable as separate fields throughout the codebase (today they're sometimes joined as a single string and re-split downstream — fragile). Inline strings encode the same information but force every consumer to re-parse. CLI-only shortcuts don't survive across invocations; the whole point is to set the binding once and stop typing it.

### Numeric-only shortcut keys are forbidden
**Chosen**: Shortcut keys SHALL match `^[A-Za-z][A-Za-z0-9_-]*` or contain at least one non-digit; pure-digit keys (`"4"`, `"42"`) are rejected at config load. Keys may include `:` (e.g. `"op:z"`) for mnemonic readability.

**Alternative considered**: Allow any string.

**Why**: v0.6.0 introduces a quota suffix `:N` on agent entries. With pure-digit shortcut keys, an entry like `claude:4` is ambiguous — is `4` the GLM-via-opencode shortcut or a quota of 4? Forbidding numeric-only keys makes positional parsing of `:`-segments unambiguous. Models with embedded digits (`gpt-4`, `claude-4.5-sonnet`) are not numeric-only and parse as normal.

### Resolve shortcuts at config load, not at use
**Chosen**: When `config.toml` parses, every `[providers]` entry is validated against the harness whitelist, and every later reference (in a mix, route, or rotation) is resolved against the resolved table at that moment. Unresolved keys produce a `did-you-mean` error referencing the closest-matching defined keys.

**Alternative considered**: Lazy resolution — resolve when the agent is first selected.

**Why**: Lazy resolution means a typo in a route entry doesn't surface until run N when that entry is reached, which can be hours into a relay. Up-front validation moves the failure to startup where the operator can fix it without losing run state. The `did-you-mean` hint reduces frustration on plausible typos.

### Drop the microbeads-instruction toggle (alignment with v0.4.0)
**Chosen**: `[microbeads]` contains only `instructions_file = "..."` (a path to the content rally injects when in microbeads-backed mode). There is no `instructions = "auto"|"include"|"skip"` toggle — injection is unconditional in microbeads-backed mode, omitted in no-backend mode. The legacy `Beads` flat field is removed outright.

**Alternative considered**: Keep the `instructions` toggle from the original v0.5.0 draft.

**Why**: v0.4.0 already decided injection is unconditional in microbeads-backed mode (`docs/...microbeads-first-class/design.md`). Carrying a toggle in v0.5.0 would re-introduce the very surface v0.4.0 just removed. The only configurable piece is *what content* gets injected when mode-detection says yes — that's `instructions_file`.

### `[fallback].instructions_file` only used in no-backend mode
**Chosen**: When `.beads/mb.json` is absent (no-backend mode) AND no ready bead exists, rally injects the contents of `[fallback].instructions_file` as the prompt. A built-in default fallback content ships with rally for workspaces that don't configure one.

**Alternative considered**: Fallback file used always, with bead body appended when present.

**Why**: The fallback exists specifically because there's no bead to drive the prompt. Injecting it alongside a real bead body would dilute the bead's instructions and confuse the agent. Scoping fallback to "no bead" preserves the bead's primacy when one exists.

### Preserve flat fields at root; new sections are purely additive
**Chosen**: Existing fields (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`) stay at the file root. New sections (`[defaults]`, `[microbeads]`, `[fallback]`, `[providers]`) live alongside them. CLI flags continue to override config values.

**Alternative considered**: Break the schema cleanly: move all flat fields under `[harness]`/`[runtime]` sections.

**Why**: Existing `.rally/config.toml` files in the wild would all break, costing every user a manual migration for a purely cosmetic gain. The flat fields work fine; new sections expand the surface without disturbing the established part of it.

### Drop legacy `.rally/config` env-style file with no migration
**Chosen**: Delete the `.rally/config` loader (`internal/config/`) outright. No fallback read, no warning if the file exists.

**Alternative considered**: Keep the loader, log a deprecation warning.

**Why**: The file's only documented use case (`RALLY_DATA_DIR`) is also settable via `data_dir` in `config.toml` and via the `RALLY_DATA_DIR` environment variable. The deprecation-warning path is dead weight on the loader for a feature with no users; pruning it now keeps the v0.5.0 loader lean. Operators with a stale `.rally/config` see no behaviour change since it had no behaviour anyone relied on.

### Add a `schema_version` field but only warn on mismatch in v0.5.0
**Chosen**: The TOML root gains a `schema_version = 2` field. v0.5.0 reads it; if absent, treat as version 1 and accept the file (compatibility path). On mismatch with what rally expects, log a warning but proceed. v0.6.0+ may use this to block load.

**Alternative considered (a)**: No version field, evolve schema implicitly.
**Alternative considered (b)**: Hard error on mismatch from v0.5.0.

**Why**: Implicit evolution becomes painful by v0.6.0 (route entries are conditional on shortcut resolution working correctly; we want a clean handshake). Hard error from v0.5.0 surprises users who didn't write the field. Warn-then-load gives us a soft migration runway: existing configs load, get auto-bumped on next write, and v0.6.0+ can tighten if needed.

## Risks / Trade-offs

- **Two harness:model representations coexist (raw string and shortcut key)** → Mitigation: the parser is the single resolution point; downstream code only sees the resolved `(harness, model)` tuple. Tests cover both forms producing identical resolved values.
- **`did-you-mean` suggestions are noisy if shortcuts are numerous** → Mitigation: cap at 3 suggestions ranked by Levenshtein distance; if none are within a small threshold, just list valid keys.
- **Removing `.rally/config` could surprise users who set `RALLY_DATA_DIR` there years ago** → Mitigation: the env variable and the `data_dir` field in `config.toml` both still work. Release notes document the removal. The risk is bounded: if any user does have a `.rally/config` file with that variable, rally simply ignores it; the env-variable read still happens via the OS environment.
- **`schema_version` warn-only is easy to ignore** → Mitigation: the warning prints a one-line "schema mismatch — please update or run `rally config check`" message; it's not a slap, but it's visible. v0.6.0+ tightens behaviour as the format stabilises.
- **Fallback file path could resolve to a missing file** → Mitigation: at config load, the path is checked; missing files emit a warning and rally falls back to the built-in default content. No hard error — a missing fallback is weak signal for a no-bead session, not a startup blocker.

## Migration Plan

1. **Schema additions**: extend `internal/config/config_v2.go` (or split as `v3.go` if the diff is sizeable) with the four new sections. Existing fields untouched. New fields default to zero values when absent.
2. **Provider resolution**: add `ResolveAgent(spec string) (harness, model string, err error)` to the config layer. Mix parsing, route parsing (v0.6.0), and rotation parsing (v0.7.0) all funnel through this single resolver.
3. **Mix parsing extension**: update the relay-runner's mix parser to call the new resolver. Existing `harness:model` strings continue to work; shortcut keys now resolve through `[providers]`.
4. **Fallback wiring**: extend the prompt-building path so that no-backend mode + no-ready-bead substitutes `[fallback].instructions_file` content (or built-in default) for the bead body.
5. **Defaults wiring**: read `[defaults].iterations`, `.mix`, `.verbose` at relay startup; CLI flags continue to override.
6. **Legacy loader removal**: delete `internal/config/` env-style loader and any callers; CI confirms no remaining references.
7. **Schema version handshake**: emit `schema_version = 2` on every write; warn on read-time mismatch.

Rollback: revert v0.5.0. Existing `.rally/config.toml` files keep working since the new sections were additive. Workspaces that adopted shortcut keys would need to expand them back to raw strings — the release notes call this out.

## Open Questions

- Whether the `[providers]` table should support per-shortcut env-var injection (e.g. `OPENCODE_API_KEY` overrides). For v0.5.0, env handling stays exactly as it is today (set in the shell or via systemd). Revisit if multi-key workflows surface.
- Whether `[defaults].mix` should accept a "named mix" (e.g. `mix = "balanced"` looking up a named list elsewhere). Out of scope for v0.5.0; named mixes can land in v0.6.0 alongside roles or later.
