## Why

Rally (v0.1.x) is a proven agent orchestrator — it connects to four coding agent CLIs and runs them in a loop, one task per session, avoiding long-context degradation. However its internal structure is loose and lacks strong verification loops. A sister repo (gry) was built as a structured experiment with an Executor interface, fixture-driven integration testing, dependency injection, and a richer TUI — but only supports one agent. Consolidating both into rally v0.2.0 combines rally's real-world agent support with gry's architectural discipline, eliminating the maintenance burden of two diverging codebases.

## What Changes

- **BREAKING**: Rename core concepts: three-tier naming — "session" (one agent CLI invocation), "run" (one iteration counting against relay target, may include retries), "relay" (a campaign of N runs)
- **BREAKING**: Move repo-accessible data from `~/.local/share/rally` to `.rally/` in the repo root
- Introduce `Executor` interface (from gry) for agent lifecycle — `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`
- Add JSONL-in-git as source of truth for sessions, messages, and relays (one file per record type, ~100 record window)
- Add in-memory cache loaded from JSONL on startup (no external database)
- Add resume-and-report structured output collection — after session ends, resume with a reporting prompt to collect progress JSON (preserves token caching). Stop hooks as trigger/fallback.
- Add retry logic with error resilience: retry session (3x within a run) → pause agent (1hr) → freeze agent (5hr) → relay failure
- Replace hand-rolled CLI flag parsing with Cobra
- Replace line-based TUI with full-screen gitui-style panel layout using bubbles
- Add dashboard panel (relay progress, session history), inbox panel (message management), and live session status panel (runtime, git lines +/-, files changed)
- Add fixture-driven e2e test infrastructure with mock agent CLIs
- Add `.rally/current_task.md` as ephemeral gitignored context file for agents
- Keep beads as one pluggable task backend (not a hard dependency); rally remains focused on delegating work to agents

## Capabilities

### New Capabilities
- `executor`: Agent lifecycle abstraction — Executor interface with per-agent implementations, structured session results via agent-specific output mechanisms, and fixture executor for testing
- `store`: JSONL files in `.rally/` as git-tracked source of truth with in-memory cache for fast queries, loaded on startup
- `relay-runner`: Relay orchestration loop — agent mix cycling, run execution with session retries, error resilience cascade (3x retry → 1hr pause → 5hr freeze → relay failure), graceful stop
- `tui-dashboard`: Full-screen gitui-style terminal UI — bordered panels via bubbles, dashboard view (relay progress + session history), inbox view (message CRUD + FIFO ordering), live session status (runtime, git stats), responsive layout
- `agent-hooks`: Resume-and-report structured output — after session ends, resume with reporting prompt to collect JSON progress report (completed, summary, remaining work, files changed). All four CLIs (Claude Code, Codex, Gemini CLI, OpenCode) support resume-and-report. Stop hooks as trigger/fallback.

### Modified Capabilities

## Impact

- All internal packages restructured: `internal/rally/runner/` → `internal/relay/`, new `internal/agent/`, `internal/store/`, `internal/tui/` with sub-packages
- Go dependencies added: `github.com/spf13/cobra`, `github.com/charmbracelet/bubbles`
- Go dependencies removed: none (bubbletea, lipgloss, go-toml, yaml already present)
- Go dependencies NOT added: `gorm.io/gorm`, `gorm.io/driver/sqlite` (using in-memory cache instead)
- Data directory split: `.rally/` (git-tracked JSONL, gitignored ephemeral files) + `~/.local/share/rally/` (transcripts)
- CLI interface changes: commands restructured under Cobra, `rally run` → `rally relay`, `rally tui` → default (no subcommand)
- Install/release pipeline: unchanged (goreleaser, install.sh, self-update)
