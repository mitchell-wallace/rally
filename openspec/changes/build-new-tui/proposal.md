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
- **Data source**: In-memory cache loaded from JSONL — the TUI reads from the store, not from files directly
- **Relay runner integration**: OnStatus callbacks for pushing live updates to the TUI
- **No stdout streaming**: Show stats only (runtime, git diff summary), not raw agent output
- **Relay resume**: Modal prompt on startup if incomplete relay exists — this replaces the CLI prompt added in v0.2.0

## Capabilities

### New Capabilities
- `tui-dashboard`: Full-screen gitui-style terminal UI — bordered panels via bubbles, dashboard view (relay progress + try history), inbox view (message CRUD + FIFO ordering), live try status (runtime, git stats), relay start configuration overlay, responsive layout

## Impact

- New packages: `internal/tui/dashboard/`, `internal/tui/inbox/`, `internal/tui/runstatus/`
- Go dependencies added: `github.com/charmbracelet/bubbles`
- CLI interface change: default (no subcommand) launches TUI instead of showing help
- Relay runner gains OnStatus callback interface for TUI integration
