## 1. Width-aware shortcut hint

- [ ] 1.1 Read terminal width at render time (`term.GetSize(fd)` or equivalent) in `internal/style/style.go`
- [ ] 1.2 Implement the four tiers (full / medium / narrow / minimal) and pick the widest that fits one line
- [ ] 1.3 Fall back to a safe default width + tier when stdout is not a TTY
- [ ] 1.4 Tests: each width band selects the expected tier; output never exceeds one line

## 2. Left-align hints and full-width headers

- [ ] 2.1 Remove centering/padding from `style.ShortcutHint()`; render flush-left
- [ ] 2.2 Make header/footer/summary lines fill terminal width (cap 80) with box-drawing fill
- [ ] 2.3 Clamp gracefully on very narrow terminals (truncate label, keep structure)
- [ ] 2.4 Tests: header fills to the cap; hint is flush-left; countdown redraw overwrites cleanly (no accumulation)

## 3. Model shorthands

- [ ] 3.1 Add a `[models]` shorthand block to the generated `config.toml` template (`cmd/rally/main.go`) and config struct
- [ ] 3.2 Resolve a shorthand to its full model name where models are referenced
- [ ] 3.3 Tests: shorthand resolves; unknown shorthand errors clearly; full model names still accepted

## 4. `rally init` subcommands

- [ ] 4.1 Add `rally init models`, `rally init roles`, `rally init all`; keep bare `rally init` as workspace init
- [ ] 4.2 Make each subcommand independently re-runnable (idempotent merge into existing config)
- [ ] 4.3 Tests: `init all` runs the three in sequence; `init models` only touches the shorthand block

## 5. Rename `FallbackConfig` → `FreeRunPrompt`

- [ ] 5.1 Rename `FallbackConfig.InstructionsFile`→`FreeRunPromptFile`, `loadFallbackInstructions()`→`loadFreeRunPrompt()`, `builtInDefaultFallback`→`builtInDefaultFreeRunPrompt`
- [ ] 5.2 Change the config key to `[free_run] prompt_file`; accept the old `[fallback] instructions_file` as a deprecated alias for one release, warning on use
- [ ] 5.3 Confirm the free-run behavior at `runner.go:1054` is unchanged (name-only refactor)
- [ ] 5.4 Tests: new key loads; old key still loads with a deprecation warning; both resolve to the same prompt

## 6. Docs & coordination

- [ ] 6.1 Document the model shorthands, `init` subcommands, and the `[free_run]` key (with the deprecation note) in `README.md`/config docs
- [ ] 6.2 Coordinate `style.ShortcutHint()` edits with `agent-lifecycle` (label renames) so the layout and label work don't clobber each other
- [ ] 6.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
