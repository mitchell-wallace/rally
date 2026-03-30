## Why

Rally (v0.1.x) is a proven agent orchestrator — it connects to four coding agent CLIs and runs them in a loop, one task per session, avoiding long-context degradation. However its internal structure is loose and lacks strong verification loops. A sister repo (gry) was built as a structured experiment with an Executor interface, fixture-driven integration testing, dependency injection, and a richer TUI — but only supports one agent. Consolidating both into rally v0.2.0 combines rally's real-world agent support with gry's architectural discipline, eliminating the maintenance burden of two diverging codebases.

## What Changes

- **BREAKING**: Rename core concepts: "session" becomes "run", "batch" becomes "relay"
- **BREAKING**: Move repo-accessible data from `~/.local/share/rally` to `.rally/` in the repo root
- Introduce `Executor` interface (from gry) for agent lifecycle — `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`
- Add JSONL-in-git as source of truth for runs, messages, and relays (one file per record type, ~100 record window)
- Add SQLite as an ephemeral local cache rebuilt from JSONL on startup
- Add stop-hook-based structured output collection from agents (each agent's hook system invoked at run end)
- Add retry logic with error resilience: retry (3x) → pause agent (1hr) → freeze agent (5hr) → batch failure
- Replace hand-rolled CLI flag parsing with Cobra
- Replace line-based TUI with full-screen gitui-style panel layout using bubbles
- Add dashboard panel (relay progress, run history), inbox panel (message management), and live run status panel (runtime, git lines +/-, files changed)
- Add fixture-driven e2e test infrastructure with mock agent CLIs
- Add `.rally/current_task.md` as ephemeral gitignored context file for agents
- Keep beads as one pluggable task backend (not a hard dependency); rally remains focused on delegating work to agents

## Capabilities

### New Capabilities
- `executor`: Agent lifecycle abstraction — Executor interface with per-agent implementations, structured run results via stop hooks, and fixture executor for testing
- `store`: Dual-layer storage — JSONL files in `.rally/` as git-tracked source of truth, SQLite cache in system-local directory for fast queries, replay-on-startup cache rebuild
- `relay-runner`: Relay orchestration loop — agent mix cycling, run execution, retry logic (3x retry → 1hr pause → 5hr freeze → relay failure), graceful stop
- `tui-dashboard`: Full-screen gitui-style terminal UI — bordered panels via bubbles, dashboard view (relay progress + run history), inbox view (message CRUD + FIFO ordering), live run status (runtime, git stats), responsive layout
- `agent-hooks`: Stop hook integration for structured agent output — rally-provided hook scripts that collect completion status, summary, remaining work, files changed from each agent type

### Modified Capabilities

## Impact

- All internal packages restructured: `internal/rally/runner/` → `internal/relay/`, new `internal/agent/`, `internal/store/`, `internal/tui/` with sub-packages
- Go dependencies added: `github.com/spf13/cobra`, `gorm.io/gorm`, `gorm.io/driver/sqlite`, `github.com/charmbracelet/bubbles`
- Go dependencies removed: none (bubbletea, lipgloss, go-toml, yaml already present)
- Data directory split: `.rally/` (git-tracked JSONL, gitignored ephemeral files) + `~/.local/share/rally/` (SQLite cache, transcripts)
- CLI interface changes: commands restructured under Cobra, `rally run` → `rally relay`, `rally tui` → default (no subcommand)
- Install/release pipeline: unchanged (goreleaser, install.sh, self-update)
