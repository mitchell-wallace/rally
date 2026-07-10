## Why

Rally v0.2.0 (consolidate-rally-gry) ships as CLI-only to focus on getting the core architecture right: Executor interface, JSONL store, relay runner, and per-agent inline output parsing. Once that foundation is stable, rally needs a rich terminal UI to make relay management, try monitoring, and inbox messaging feel natural without leaving the terminal.

The v0.1.x TUI was a simple line-based Bubble Tea app tightly coupled to the old runner/state/messages internals. It was removed during the v0.2.0 consolidation rather than ported, since it would need a complete rewrite to work with the new architecture.

## Prerequisites

- `consolidate-rally-gry` change must be complete (v0.2.0 core architecture in place)
- Store layer, relay runner, and executor interfaces must be stable

## What Changes

- Add full-screen gitui-style terminal UI using Bubble Tea and bubbles component library
- Add dashboard panel: relay progress (completed/total runs), agent mix, recent try history with outcome/agent/runtime/git stats
- Add live try status panel: elapsed runtime counter, git lines +/-, files changed (no agent stdout streaming)
- Add inbox panel: message list (pending above addressed), compose mode, reorder, mark addressed
- Add relay start configuration overlay: editable fields for iteration count and agent mix, defaults from `.rally/config.toml`
- Add relay resume modal: shown on startup when incomplete relay exists, displays relay state, resume/discard options
- Add relay stop via keyboard shortcut (graceful stop — complete current try then halt)
- Add view navigation: keyboard shortcuts for dashboard/inbox switching
- Make default (no subcommand) launch the full-screen TUI
- Add `github.com/charmbracelet/bubbles` dependency
- Create `internal/tui/` package tree: `dashboard/`, `inbox/`, `runstatus/`

## Design Notes

These are carried forward from the original consolidate-rally-gry design and should be validated against the actual v0.2.0 architecture when this change is picked up:

- **Layout**: gitui-style bordered panels using bubbles, responsive to terminal size
- **Runtime boundary**: The TUI consumes the presentation-neutral contract in `internal/relay/runner/runtimeevent` that `separate-runtime-presentation-boundary` introduced — runtime events in (via `runtimeevent.Sink`), `runtimeevent.Press` controls out (via `runtimeevent.ControlSource`). The runner emits data; the TUI renders. The TUI is a concrete presentation adapter: it may import only `relay/runner/runtimeevent`, `style`, and `keyboard`, and must not import runner internals, harness, config, or store (enforced by archguard's presentation boundary).
- **Monitor status line**: Replace, do not adapt. The live status-line subsystem (`mon.Start(os.Stdout)` with cursor-reservation) is a documented runner-driven residual that the TUI supersedes wholesale — see Decision 8 of `separate-runtime-presentation-boundary`.
- **Data source**: In-memory cache loaded from JSONL — the TUI reads from the store, not from files directly
- **No stdout streaming**: Show stats only (runtime, git diff summary), not raw agent output
- **Relay resume**: Modal prompt on startup if incomplete relay exists — this replaces the CLI prompt added in v0.2.0

## Capabilities

### New Capabilities
- `tui-dashboard`: Full-screen gitui-style terminal UI — bordered panels via bubbles, dashboard view (relay progress + try history), inbox view (message CRUD + FIFO ordering), live try status (runtime, git stats), relay start configuration overlay, responsive layout
- `tui-config-management`: In-TUI machine-scope management for ordered role routes, per-role reasoning effort, and provider enable/disable state, persisted without reformatting unrelated TOML.

## Impact

- New packages: `internal/tui/dashboard/`, `internal/tui/inbox/`, `internal/tui/runstatus/`
- Go dependencies added: `github.com/charmbracelet/bubbles`
- CLI interface change: default (no subcommand) launches TUI instead of showing help
- Consumes the existing `internal/relay/runner/runtimeevent` contract (events in via `Sink`, `Press` controls out via `ControlSource`) — no new runner callback interface; the runner already emits, the TUI renders

## Update Notes

### 2026-06-08T20:53:42+10:00

- Directional update: this change may no longer want to be a full takeover TUI
  runtime in the style of lazygit or neovim. A lighter path may be more useful:
  start-of-run interactive configuration for the same knobs currently exposed by
  CLI args, plus inflight steering enhancements.
- Candidate scope to revisit: per-relay route/agent-mix selection, iteration
  count, temporary harness/model enablement or disablement, and later inflight
  updates to agent mix, role config, or target iterations.
- The existing `rally config` command can be a baseline, but its linear prompt
  flow is not ideal. A menu-like config surface, where the operator can navigate
  to one option and change it, may be the better shared model for startup config
  and any future inflight config update command.

### 2026-07-02

- Re-grounded against `separate-runtime-presentation-boundary` (Decisions 5/7/8).
  The runner↔presentation boundary the TUI consumes now exists as
  `internal/relay/runner/runtimeevent`: the runner emits typed events through a
  `Sink` and reads `Press` controls through a `ControlSource`; a concrete
  presentation package (the CLI's `internal/presentation/terminal` adapter today,
  a TUI later) renders them. Dropped the stale "OnStatus callback" framing — no
  new runner callback interface is needed. Noted that the monitor status line is
  replaced wholesale, not adapted (Decision 8 residual).

### 2026-07-10

- The accepted tabbed TUI is now the implementation base. The next operator
  surface is a lightweight Config tab rather than a full runtime takeover.
- This increment covers ordered routes for built-in and custom roles,
  per-role reasoning effort, and provider enable/disable switches.
- These shared routing tables are edited in the machine config
  (`~/.config/rally/config.toml`). Repo config remains an override layer and is
  not rewritten by this surface.
- Persistence must retain unrelated TOML and comments. The existing whole-file
  `SaveV2File` writer is therefore unsuitable for this surface; narrowly scoped
  mutations are required.
