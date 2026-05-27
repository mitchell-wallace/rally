## Why

Two unrelated rough edges in rally's CLI surface, bundled because both are small,
low-risk polish:

1. **Display glitches.** The keyboard-shortcut hint line wraps to two lines on a
   narrow terminal, and the 1-second countdown redraw (`waiting 50m 39s`) doesn't
   account for the extra line, so each tick appends instead of overwriting and the
   screen fills with stale countdown lines. The hint is also centre-aligned
   (indented from the left edge) and headers/footers don't span the terminal width,
   so output reads ragged.
2. **Config UX friction.** `rally init` produces a config that works but is awkward
   to navigate and doesn't demonstrate key features — notably model shorthands
   (important for opencode, whose model names are long). And the config type
   `FallbackConfig` is misnamed: it only sets the task prompt for a laps-less,
   promptless ("free") run, but "fallback" reads like runner failover (which is
   actually the routing Scheduler's lane rotation, unrelated).

## What Changes

- **Width-aware shortcut hint.** Detect terminal width at render time and truncate
  the shortcut hint to a single line, picking a tier (full / medium / narrow /
  minimal) that fits, so countdown redraws always overwrite cleanly.
- **Left-align shortcut hints.** Remove centering/padding from
  `style.ShortcutHint()`; render flush-left.
- **Full-width headers.** Make header/footer/summary lines span the terminal width
  (capped at 80) using box-drawing fill.
- **Model shorthands in config.** Populate a `[models]` shorthand section in the
  generated config (e.g. `s4 = "claude-sonnet-4-20250514"`).
- **`rally init` subcommands.** `rally init` (workspace, existing), `rally init
  models` (add/update shorthands), `rally init roles` (existing role init), `rally
  init all` (all three in sequence).
- **Rename `FallbackConfig` → `FreeRunPrompt`.** Rename
  `FallbackConfig.InstructionsFile` / `loadFallbackInstructions()` /
  `builtInDefaultFallback` to `FreeRunPromptFile` / `loadFreeRunPrompt()` /
  `builtInDefaultFreeRunPrompt`, config key `[free_run] prompt_file`, with a
  back-compat alias accepting the old `[fallback] instructions_file` for one
  release. Pure naming/config clarity, no behavior change.

## Capabilities

### Added Capabilities
- `cli-display`: terminal-width-aware shortcut hint, left-aligned hints, and
  full-width headers/footers.
- `cli-config`: model-shorthand config section, `rally init` subcommands, and the
  `FreeRunPrompt` config rename with a back-compat alias.

## Impact

- **Code**: `internal/style/style.go` (`ShortcutHint`, header rendering), the
  countdown/redraw path, `cmd/rally/main.go` (`init` subcommands), the config struct
  and template (`internal/config/` and the `config.toml` template in
  `cmd/rally/main.go`), and the free-run prompt loader (`runner.go:1054`,
  `loadFallbackInstructions`).
- **Behavior**: clean single-line shortcut hint and countdown on any width;
  flush-left hints; full-width headers; richer generated config; clearer config
  naming with a one-release back-compat alias.
- **Out of scope / rejected**:
  - `rally reconcile` (QA R8) — rejected; fixing internal state via a CLI command is
    a code smell. Correctness is made intrinsic instead (lap pinning in
    `harden-relay-run-lifecycle`; VERIFY keeps `tasks.md` current via `prepare-laps`).
  - `rally resume` after manual stop (QA R14) — subsumed by `agent-lifecycle`
    (session resume) + `harden-relay-run-lifecycle` (freeze-decay / `--new` reset).
  - Config-TUI tabs/sections — deferred to a future `build-new-tui` change; noted, not
    built here.
- **Coordination**: `style.ShortcutHint()` is also edited by `agent-lifecycle` (label
  renames "graceful stop" / "quit now"). The narrow/medium tiers here assume those
  renamed labels — co-implement or sequence so the two changes don't clobber each
  other.
