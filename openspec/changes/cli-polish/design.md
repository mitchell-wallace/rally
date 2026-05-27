## Context

Two independent CLI rough edges, grouped as low-risk polish.

The shortcut hint (`style.ShortcutHint()`) renders a fixed-width string. On a narrow
terminal it wraps to two lines; the relay's 1-second countdown redraw assumes a
single line, so each `waiting Xm Ys` tick appends below the wrapped hint instead of
overwriting it — the screen accumulates stale countdown lines. The hint is also
centre-aligned, and headers/footers are short, so output reads ragged against the
left edge.

Separately, `rally init`'s generated config works but under-sells the tool: no model
shorthands (painful for opencode's long model names) and a flat layout that's hard to
scan. And `FallbackConfig` is a misnomer — `FallbackConfig.InstructionsFile`,
`loadFallbackInstructions()`, and `builtInDefaultFallback` only set the task prompt
for a laps-less, promptless ("free") run (`runner.go:1054`). "Fallback" suggests
runner failover, which is a different mechanism entirely (the routing Scheduler's lane
rotation, owned by `agent-lifecycle`'s R9 notes).

## Goals / Non-Goals

**Goals:**
- Shortcut hint and countdown render cleanly on any terminal width.
- Flush-left hints; full-width headers.
- Generated config demonstrates model shorthands and is easier to navigate.
- Config naming reflects what the field actually does, without breaking existing
  configs for at least one release.

**Non-Goals:**
- A new TUI / tabbed config browser (deferred to `build-new-tui`).
- Any change to runner failover, free-run behavior, or prompt assembly (the rename is
  name-only).
- Prompt-context pruning — that lives in `harden-relay-run-lifecycle` (Bounded prompt
  context), not here.

## Decisions

**1. Width-aware shortcut hint with tiered fallbacks.**
Read terminal width at render time (`term.GetSize(fd)` or equivalent) and pick the
widest tier that fits on one line:
- Full: `[Ctrl+S skip] [Ctrl+P pause] [Ctrl+X graceful stop] [Ctrl+C quit now]`
- Medium: `[^S skip] [^P pause] [^X stop] [^C quit]`
- Narrow: `^S skip · ^P pause · ^X stop · ^C quit`
- Minimal: `^S·^P·^X·^C`

Guaranteeing a single line is what fixes the countdown-redraw accumulation; the redraw
logic itself is unchanged once the hint never wraps. The full/medium labels assume the
`agent-lifecycle` renames ("graceful stop" / "quit now").

**2. Left-align and full-width headers in `style`.**
Remove centering/padding from `ShortcutHint()`. Make header/footer/summary lines fill
the terminal width (capped at 80) with box-drawing characters, e.g.
`── Run 3 / Lap: fix-auth-bug ── claude (sonnet-4) ───────────────`. Both are confined
to `internal/style/style.go`.

**3. Model shorthands + `init` subcommands.**
Add a `[models]` shorthand block to the generated config and resolve shorthands when a
model is referenced. Split `rally init` into `init` (workspace), `init models`,
`init roles`, and `init all` so each piece is independently re-runnable. The config-TUI
tabs idea is noted as a stretch and deferred to `build-new-tui`.

**4. `FallbackConfig` → `FreeRunPrompt`, with a back-compat alias.**
Rename the type/methods/defaults and the config key to `[free_run] prompt_file`. Accept
the old `[fallback] instructions_file` as a deprecated alias for one release (warn on
use), then drop it. Name-only; the free-run behavior at `runner.go:1054` is untouched.

## Risks / Trade-offs

- **`term.GetSize` fails (not a TTY / piped output)** → fall back to a safe default
  width (e.g. 80) and the corresponding tier; never wrap.
- **Back-compat alias forgotten / dropped too early** → document the one-release window
  and emit a deprecation warning when the old key is read, so removal is signposted.
- **Header width cap interacts with very narrow terminals** → clamp to the available
  width when it is below the content length; truncate the label, not the structure.
- **`ShortcutHint()` double-edited with `agent-lifecycle`** → sequence/co-implement; the
  label text here is owned by `agent-lifecycle`, the layout by this change.
