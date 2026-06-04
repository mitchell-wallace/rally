## 1. Width-aware shortcut hint

- [ ] 1.1 Detect terminal width on first render using `term.GetSize(fd)`, and optionally on `SIGWINCH` (if testable via `tmux`); fall back to one-shot detection if `SIGWINCH` is impractical
- [ ] 1.2 Implement the four tiers (full / medium / narrow / minimal) and pick the widest that fits one line
- [ ] 1.3 Fall back to a safe default width + tier when stdout is not a TTY
- [ ] 1.4 Tests: each width band selects the expected tier; output never exceeds one line

## 2. Full-width headers

- [ ] 2.1 Make header/footer/summary lines fill terminal width (cap 80) with box-drawing fill in `internal/style/style.go`
- [ ] 2.2 Clamp gracefully on very narrow terminals (truncate label, keep structure)
- [ ] 2.3 Verify shortcut hint stays flush-left through width-aware tier changes (it is already flush-left in current code)
- [ ] 2.4 Tests: header fills to the cap; countdown redraw overwrites cleanly (no accumulation)

## 3. `rally init` subcommands

- [ ] 3.1 Add `rally init roles` (existing role init) and `rally init all` (workspace + roles); keep bare `rally init` as workspace init
- [ ] 3.2 Make each subcommand independently re-runnable (idempotent merge into existing config)
- [ ] 3.3 Tests: `init all` runs the two in sequence; `init roles` only touches the roles configuration

## 4. Rename `FallbackConfig` → `FreeRunPrompt`

- [ ] 4.1 Rename `FallbackConfig.InstructionsFile`→`FreeRunPromptFile`, `loadFallbackInstructions()`→`loadFreeRunPrompt()`, `builtInDefaultFallback`→`builtInDefaultFreeRunPrompt`
- [ ] 4.2 Change the config key to `[free_run] prompt_file`; accept the old `[fallback] instructions_file` as a deprecated alias for one release, warning on use
- [ ] 4.3 Confirm the free-run behavior at `loadFallbackInstructions()` / `resolveRunTask()` in `runner.go` is unchanged (name-only refactor)
- [ ] 4.4 Tests: new key loads; old key still loads with a deprecation warning; both resolve to the same prompt

## 5. Activity age bounded by try runtime

- [ ] 5.1 In `internal/monitor/monitor.go` `Tick()`, clamp `lastActivity` to the try's elapsed time (`if lastActivity > elapsed { lastActivity = elapsed }`) before computing indicators; leave the `lastActivity < 0` ("—") path unchanged
- [ ] 5.2 Tests: at `elapsed == 0` with a stale log mtime, `last activity` reads `< 1m ago`; `⚠ slowing` does not appear until the try's own silence reaches 0.6× the threshold

## 6. Collapse retries into one updating line

- [ ] 6.1 In `internal/relay/runner.go`, suppress the per-attempt `RenderFooter` while a run is retrying within budget; render one in-place neutral line (`↻ retrying N/M · last: <reason> (<dur>, <files>)`) using the existing cursor-redraw mechanism (safe because Rally does not currently print agent output inline)
- [ ] 6.2 Print exactly one outcome footer at the terminal result (green `✓ passed on try N/M …` on recovery; red `✗ failed after K tries · <reason>` when exhausted)
- [ ] 6.3 Add a terminal-vs-interim notion to `style.RenderFooter` so `FailureStyle` (red) is applied only to terminal failures; single-attempt runs are terminal on first failure
- [ ] 6.4 Tests: a run that retries 5× prints one updating line + one final coloured footer (not 5 red footers); a single-attempt failure is coloured red; a run that recovers prints a green footer

## 7. Leftover-aware "incomplete" detection

- [ ] 7.1 Snapshot the already-dirty path set at try start (near the `headBefore` capture, `runner.go:816`) using `git status --porcelain`
- [ ] 7.2 Compute "changes produced by this try" as the working-tree delta vs. the snapshot (comparing `git status --porcelain` before/after), and base the `incomplete` classification (`runner.go:986`) on that delta rather than `dirtyBeforeAutoCommit` / the porcelain fallback
- [ ] 7.3 Tests: a no-op try that inherits leftover changes from a prior failed try is NOT classified incomplete; a try that adds its own unfinalized changes IS; a try that reverts/commits an inherited leftover is attributed to this try

## 8. Docs & coordination

- [ ] 8.1 Document the `init` subcommands and the `[free_run]` key (with the deprecation note) in `README.md`/config docs
- [ ] 8.2 Coordinate `style.ShortcutHint()` edits with `agent-lifecycle` (label renames) so the layout and label work don't clobber each other
- [ ] 8.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
