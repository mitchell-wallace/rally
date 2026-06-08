## Context

A cluster of small CLI rough edges, grouped as low-risk polish.

The shortcut hint (`style.ShortcutHint()`) renders a fixed-width string. On a narrow
terminal it wraps to two lines. The live status line redraws every second
(`monitor.TickInterval = 1s`) via a cursor-up sequence whose line count is fixed at
print time (`SetCursorUpLines`, `monitor.go:568`: `\x1b[<n>A … \x1b[2K`). When the
hint wraps, the terminal scrolls and the cursor-up count no longer points at the
status line, so each tick appends below instead of overwriting and the screen
accumulates stale status lines. Guaranteeing a single-line hint restores the
cursor-up math. The hint is already flush-left in the current code; the width-aware tier
changes must verify and preserve that. Headers/footers use short fixed-width
separators (40 chars) that don't span the terminal, so output reads ragged.

Two further status-line issues are independent of width. `last activity`
(`monitor.go:425`) is `time.Since(logMtime)` with no relation to the current try, so
at a retry's start it surfaces the stale previous-try mtime (`20h 50m ago`) and the
derived `⚠ slowing` indicator fires instantly. Retries also print one red `✗ failed`
footer per attempt (`RenderFooter`, `runner.go:1123`), which is both noisy and
miscolours non-terminal attempts as failures.

Separately, the "incomplete" failure class (`runner.go:986`) is derived from the
whole dirty working tree (`dirtyBeforeAutoCommit` plus `filesChangedList`'s
`git status --porcelain` fallback at `runner.go:1490`), so uncommitted leftovers from
a prior failed try make a later no-op try look incomplete.

Separately, `FallbackConfig` is a misnomer — `FallbackConfig.InstructionsFile`
(in `config_v2.go`), `loadFallbackInstructions()` (in `runner.go`), and
`builtInDefaultFallback` (in `runner.go`) only set the task prompt for a
laps-less, promptless ("free") run. "Fallback" suggests runner failover, which
is a different mechanism entirely (the routing Scheduler's lane rotation, owned
by `agent-lifecycle`'s R9 notes).

## Goals / Non-Goals

**Goals:**
- Shortcut hint and countdown render cleanly on any terminal width.
- Full-width headers (hints are already flush-left; verify preserved).
- Config naming reflects what the field actually does, without breaking existing
  configs for at least one release.

**Non-Goals:**
- A new TUI / tabbed config browser (deferred to `build-new-tui`).
- Any change to runner failover, free-run behavior, or prompt assembly (the rename is
  name-only).
- Prompt-context pruning — that lives in `harden-relay-run-lifecycle` (Bounded prompt
  context), not here.
- Back-compat alias in the generated config template — the template never had a
  `[fallback]` section, so only config *deserialization* (`LoadV2`) needs the alias.

## Decisions

**1. Width-aware shortcut hint with tiered fallbacks.**
Detect terminal width and pick the widest tier that fits on one line. Use
`term.GetSize(fd)` on initial render and on `SIGWINCH` (avoid polling every
`Tick()`); if `SIGWINCH` proves impractical to test, fall back to one-shot
detection on first render (the hint tier never changes mid-run, so one-shot is
sufficient for the redraw fix). SIGWINCH can be tested via `tmux` (resize a
pane and verify the hint tier updates). Tiers:
- Full: `[Ctrl+S skip] [Ctrl+P pause] [Ctrl+X stop] [Ctrl+C quit]`
- Medium: `[^S skip] [^P pause] [^X stop] [^C quit]`
- Narrow: `^S skip · ^P pause · ^X stop · ^C quit`
- Minimal: `^S·^P·^X·^C`

Guaranteeing a single line is what fixes the countdown-redraw accumulation; the redraw
logic itself is unchanged once the hint never wraps. The relabel to "graceful stop" /
"quit now" remains owned by `agent-lifecycle`; this change preserves the current
"stop" / "quit" labels while making the layout width-aware.

**2. Full-width headers in `style`.**
Make header/footer/summary lines fill the terminal width (capped at 80) with
box-drawing characters, e.g.
`── Run 3 / Lap: fix-auth-bug ── claude (sonnet-4) ───────────────`. Confined
to `internal/style/style.go`. The shortcut hint is already flush-left in the
current code; verify the width-aware tier changes preserve that.

**3. `init` subcommands.**
Split `rally init` into `init` (workspace), `init roles` (existing), and
`init all` (workspace + roles) so each piece is independently re-runnable. The
config-TUI tabs idea is noted as a stretch and deferred to `build-new-tui`.

**4. Activity age bounded by try runtime.**
In `monitor.Tick()`, clamp the computed last-activity so it never exceeds the try's
own elapsed time: `if lastActivity > elapsed { lastActivity = elapsed }` (the monitor
already knows `m.startTime`). A try that just started therefore reads `< 1m ago`
regardless of the log file's pre-existing mtime, and the `slowing` derivation
(≥0.6× threshold, `monitor.go:463`) cannot fire until the try itself has been silent
that long. This is mechanism-independent: it fixes the symptom whether the stale
mtime comes from a reused log path, a not-yet-written fresh file, or a resumed relay.
The `—` (no activity) case for `lastActivity < 0` is unchanged.

**5. Collapse retries into one updating line; colour only the terminal outcome.**
While a run is retrying within its budget, suppress the per-attempt footer and render
a single neutral line that updates in place each attempt:
`↻ retrying N/M · last: <reason> (<dur>, <files>)`. When the run reaches its terminal
result, print exactly one outcome footer — green `✓ passed on try N/M …` on recovery,
or red `✗ failed after K tries · <reason>` when the budget is exhausted. A
single-attempt run (`maxAttempts == 1`) is terminal on its first failure, so its
footer is coloured immediately. `RenderFooter` gains a notion of terminal-vs-interim
so the failure colour (`FailureStyle`) is applied only to terminal failures; interim
states use the neutral/dim style. This complements the existing "Live retry
indicator" (`retry N/M` stays on the live status line); it only changes the footer
cadence and colouring, not the inline indicator.

**6. Leftover-aware "incomplete" detection.**
Snapshot the set of already-dirty paths at try start (alongside the existing
`headBefore` capture at `runner.go:816`) and define "changes produced by this try" as
the working-tree delta against that snapshot, not the absolute dirty set. The
"incomplete" class then requires that *this* try produced uncommitted, unfinalized
changes; leftovers inherited from a prior failed try (already present at try start and
untouched) do not trigger it. A try that both inherits leftovers and adds its own
unfinalized changes is still incomplete. This keeps the incomplete-retry guidance flow
intact for genuine cases while ending the false-positive cascade seen across retries.

**7. `FallbackConfig` → `FreeRunPrompt`, with a back-compat alias.**
Rename the type/methods/defaults and the config key to `[free_run] prompt_file`. Accept
the old `[fallback] instructions_file` as a deprecated alias for one release (warn on
use), then drop it. Name-only; the `loadFallbackInstructions()` body in `runner.go` and the
`resolveRunTask()` call site are untouched.

## Risks / Trade-offs

- **Width detection fails (not a TTY / piped output)** → fall back to a safe default
  width (e.g. 80) and the corresponding tier; never wrap. One-shot detection at
  first render is sufficient since the hint tier never changes mid-run.
  `SIGWINCH` responsiveness is a nice-to-have.
- **Back-compat alias forgotten / dropped too early** → document the one-release window
  and emit a deprecation warning when the old key is read, so removal is signposted.
- **Header width cap interacts with very narrow terminals** → clamp to the available
  width when it is below the content length; truncate the label, not the structure.
- **`ShortcutHint()` double-edited with `agent-lifecycle`** → sequence/co-implement; the
  label text here is owned by `agent-lifecycle`, the layout by this change.
- **Clamping last-activity hides a genuinely stale log** → only the *display* age is
  clamped; the stall detector (`reliability`, separate from the monitor) keeps using
  real mtime, so liveness/kill behavior is unchanged. The clamp only prevents a
  cosmetic over-report and a false `slowing` badge in the try's first seconds.
- **Leftover snapshot vs. agent reverting a leftover** → define the delta as "paths
  whose working-tree state differs from the start-of-try snapshot". A try that reverts
  or commits an inherited leftover changed it, so that is correctly attributed to this
  try; only untouched leftovers are excluded. This change is behavioral (failure
  classification), so it carries explicit regression tests, unlike the display-only
  items.
- **Suppressing interim footers loses per-attempt durations** → accepted: the
  per-try durations remain in `tries.jsonl`/`summary.jsonl` and the relay log; the
  collapsed line trades on-screen history for clarity. The terminal footer still
  reports the final attempt's duration and the try count.
- **Cursor position for retry line** → Rally does not currently print agent
  output inline, so the cursor-up redraw mechanism works reliably for the retry
  line. A future change that adds inline agent output would need to account for
  cursor position at retry-print time.
