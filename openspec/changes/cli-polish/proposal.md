## Why

A cluster of 5 main and 5 smaller rough edges in rally's CLI surface, bundled
because each is polish:

1. **Display glitches.** The keyboard-shortcut hint line wraps to two lines on a
   narrow terminal, and the 1-second status redraw doesn't account for the extra
   line, so each tick appends instead of overwriting and the screen fills with stale
   status lines. Headers/footers don't span the terminal width (fixed-width 40-char
   separators), so output reads ragged.
2. **Status-line inaccuracy.** `last activity` is computed as
   `time.Since(logMtime)` with no relation to when the current try started, so at the
   start of a retry (`⏱ 0s`) it reports the stale log mtime — e.g. `20h 50m ago` —
   and the derived `⚠ slowing` indicator (≥0.6× the stall threshold) fires
   immediately and falsely.
3. **Noisy, mis-coloured retries.** Each retry attempt prints its own red
   `✗ failed` footer, so a run that retries five times shows five near-identical red
   lines even though it is still in flight. Retry attempts that are not the terminal
   outcome should not be coloured as failures, and the repetition is unnecessary.
4. **Leftover changes misclassified as "incomplete".** The "incomplete: file
   changes without finalization" class is computed from the whole dirty working tree
   (`dirtyBeforeAutoCommit` + a `git status --porcelain` fallback), so uncommitted
   leftovers from a *previous* failed try make a later no-op try look incomplete.
5. **Config UX friction.** The config type `FallbackConfig` is misnamed: it only sets
   the task prompt for a laps-less, promptless ("free") run, but "fallback" reads like
   runner failover (which is actually the routing Scheduler's lane rotation, unrelated).

## What Changes

- **Width-aware shortcut hint.** Detect terminal width at render time and truncate
  the shortcut hint to a single line, picking a tier (full / medium / narrow /
  minimal) that fits, so countdown redraws always overwrite cleanly.
- **Left-align shortcut hints.** The hint is already flush-left in the current
  code; verify the width-aware tier changes preserve that.
- **Full-width headers.** Make header/footer/summary lines span the terminal width
  (capped at 80) using box-drawing fill.
- **Activity age bounded by try runtime.** Clamp `last activity` so it can never
  exceed the current try's elapsed time. A try that just started reads `< 1m ago`
  regardless of the log file's pre-existing mtime, and `⚠ slowing` cannot fire until
  the try itself has been silent long enough.
- **Collapse retries into one updating line.** While a run is retrying, render a
  single in-place neutral line (`↻ retrying N/M · last: <reason> (<dur>, <files>)`)
  instead of one footer per attempt. Print exactly one coloured outcome footer when
  the run reaches its terminal result.
- **Colour only the terminal outcome.** Render the `✗ failed` footer in the failure
  colour only when the failure is terminal (retry budget exhausted, or a
  single-attempt run). Non-terminal retry states render neutral/dim.
- **Leftover-aware "incomplete" detection.** Snapshot the set of already-dirty
  paths at try start and classify a try as "incomplete" only when *this* try
  produced uncommitted changes — leftovers inherited from a prior failed try no
  longer trigger the class.
- **`rally init` subcommands.** `rally init` (workspace, existing), `rally init
  roles` (existing role init), `rally init all` (workspace + roles in sequence).
- **Rename `FallbackConfig` → `FreeRunPrompt`.** Rename
  `FallbackConfig.InstructionsFile` / `loadFallbackInstructions()` /
  `builtInDefaultFallback` to `FreeRunPromptFile` / `loadFreeRunPrompt()` /
  `builtInDefaultFreeRunPrompt`, config key `[free_run] prompt_file`, with a
  back-compat alias accepting the old `[fallback] instructions_file` for one
  release. Pure naming/config clarity, no behavior change.

## Capabilities

### Added Capabilities
- `cli-display`: terminal-width-aware shortcut hint, left-aligned hints, full-width
  headers/footers, try-runtime-bounded activity age, the collapsed single-line retry
  pattern, and terminal-outcome-only failure colouring.
- `cli-config`: `rally init` subcommands and the `FreeRunPrompt` config rename
  with a back-compat alias.

### Modified Capabilities
- `relay-runner`: the "Incomplete failure class" requirement is tightened so the
  class is computed from changes produced *during the current try*, not the whole
  dirty working tree, so leftovers from a prior failed try no longer trigger it.

## Impact

- **Code**: `internal/style/style.go` (`ShortcutHint`, `RenderHeader`/`RenderFooter`,
  terminal-outcome colouring), `internal/monitor/monitor.go` (status redraw path +
  `Tick`'s last-activity computation / `formatLastActivity`), `internal/relay/runner.go`
  (per-attempt footer orchestration around `runner.go:1123`; the `incomplete`
  computation at `runner.go:986` + `filesChangedList` porcelain fallback at
  `runner.go:1490`), `cmd/rally/main.go` (`init` subcommands), the config struct and
  template (`internal/config/config_v2.go` and the `config.toml` template in
  `cmd/rally/main.go`), and the free-run prompt loader (`loadFallbackInstructions` /
  `builtInDefaultFallback` in `runner.go`).
- **Behavior**: clean single-line shortcut hint and status redraw on any width;
  flush-left hints; full-width headers; accurate `last activity` (bounded by try
  runtime) with no false instant `slowing`; one updating retry line plus a single
  coloured outcome footer instead of repeated red failures; "incomplete" no longer
  fires on inherited leftover changes; clearer config naming
  with a one-release back-compat alias.
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
