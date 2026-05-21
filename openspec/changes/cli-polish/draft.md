# CLI Polish — Display Fixes and Config UX

## Keyboard Shortcut Line Wrapping

**Bug**: When terminal is narrow, the shortcut hint line wraps to two lines.
Any 1-second timer redraws (e.g. countdown "waiting 50m 39s") don't account
for the extra line, so each tick appends a new line instead of overwriting,
creating an accumulating mess:

```
waiting 50m 39s
waiting 50m 38s
waiting 50m 37s
```

**Fix**: Detect terminal width and auto-truncate the shortcut hint to fit in
one line. Strategies (pick based on available width):
- Full:     `[Ctrl+S skip] [Ctrl+P pause] [Ctrl+X graceful stop] [Ctrl+C quit now]`
- Medium:   `[^S skip] [^P pause] [^X stop] [^C quit]`
- Narrow:   `^S skip · ^P pause · ^X stop · ^C quit`
- Minimal:  `^S·^P·^X·^C`

Use `term.GetSize(fd)` or equivalent to get width at render time.

## Left-Align Shortcut Hints

**Bug**: Shortcut hints appear centre-aligned (indented from left edge).

**Fix**: Remove any centering/padding from `style.ShortcutHint()`. Render
flush-left.

---

## Full-Width CLI Headers

**Change**: Make header/footer/summary lines in CLI output span the full
terminal width, capped at 80 characters max. Use `─` or similar box-drawing
characters to fill remaining width.

Example:
```
── Run 3 / Lap: fix-auth-bug ── claude (sonnet-4) ─────────────────────────────
```

Affects `internal/style/style.go` header rendering functions.

---

## Rally Config UX

The current `rally init` creates a config file that works but is awkward to
navigate and doesn't demonstrate key features well.

### Model Shorthands

Important for UX, especially for opencode where model names are long. Add
default shorthands in populated config:

```toml
[models]
s4 = "claude-sonnet-4-20250514"
o4 = "openai/o4-mini"
g25 = "gemini-2.5-pro"
```

### `rally init` Subcommands

- `rally init` — workspace init (existing behavior)
- `rally init models` — add/update model shorthands section in config
- `rally init roles` — existing role init
- `rally init all` — runs init + models + roles in sequence

### Config Navigation (stretch)

Consider tabs or sections in the config TUI to make it easier to browse.
Current flat TOML is hard to scan when it gets large. This might be better
addressed in the `build-new-tui` change — note here and defer if so.
