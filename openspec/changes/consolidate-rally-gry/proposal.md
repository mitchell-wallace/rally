## Why

Rally (v0.1.x) is a proven agent orchestrator — it connects to four coding agent CLIs and runs them in a loop, one task per session, avoiding long-context degradation. However its internal structure is loose and lacks strong verification loops. A sister repo (gry) was built as a structured experiment with an Executor interface, fixture-driven integration testing, dependency injection, and a richer TUI — but only supports one agent. Rally v0.2.0 is inspired by gry's architectural discipline while retaining rally's real-world multi-agent support, eliminating the maintenance burden of two diverging codebases.

## What Changes

- **BREAKING**: Rename core concepts: three-tier naming — "session" (one agent CLI invocation), "run" (one iteration counting against relay target, may include retries), "relay" (a campaign of N runs)
- **BREAKING**: Move repo-accessible data from `~/.local/share/rally` to `.rally/` in the repo root
- Introduce `Executor` interface (inspired by gry) for agent lifecycle — `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`
- Add JSONL-in-git as source of truth for sessions, messages, relays, and agent status (one file per record type, per-type window sizes)
- Add in-memory cache loaded from JSONL on startup (no external database)
- Add structured output collection via per-agent strategies: block-and-report stop hooks (Claude), resume-and-report (Codex, OpenCode), or hybrid approaches (Gemini). Strategy varies by agent CLI maturity.
- Add retry logic with error resilience: retry session (3x within a run) → pause agent (1hr) → freeze agent (5hr) → relay failure. Agent status persisted in dedicated JSONL store across relays.
- Replace hand-rolled CLI flag parsing with Cobra
- Remove existing line-based TUI — rally v0.2.0 is CLI-only (new TUI planned as a separate future change)
- Add fixture-driven e2e test infrastructure with mock agent CLIs
- Add `.rally/current_task.md` as ephemeral gitignored context file (contains the prompt fed to the agent)
- Add `.rally/README.md` with instructions for agents on accessing rally data (e.g. `tail -10 sessions.jsonl`)
- Keep beads as one pluggable task backend (not a hard dependency); rally remains focused on delegating work to agents
- Drop scout mode (task discovery is out of scope — users manage their own workflow for creating beads or similar)
- Simplify `rally init` to programmatic-only operations (git init, create `.rally/` directory) — no agent invocation

## Capabilities

### New Capabilities
- `executor`: Agent lifecycle abstraction — Executor interface with per-agent implementations, structured session results via agent-specific output mechanisms, and fixture executor for testing
- `store`: JSONL files in `.rally/` as git-tracked source of truth with in-memory cache for fast queries, loaded on startup. Per-type window sizes (200 sessions, 50 relays, 50 agent status events). Messages windowed only when resolved/cancelled. Commit-then-truncate to preserve history in git.
- `relay-runner`: Relay orchestration loop — agent mix cycling, run execution with session retries, error resilience cascade (3x retry → 1hr pause → 5hr freeze → relay failure), graceful stop
- `agent-hooks`: Per-agent structured output collection — block-and-report stop hooks for Claude Code (primary, avoids potential cache invalidation with resume), resume-and-report for Codex (hooks flagged experimental) and OpenCode, hybrid for Gemini CLI. All strategies collect JSON progress report (completed, summary, remaining work, files changed). CLI flags for all four agents have been tested and verified.

### Modified Capabilities

## Impact

- All internal packages restructured: `internal/rally/runner/` → `internal/relay/`, new `internal/agent/`, `internal/store/`
- Go dependencies added: `github.com/spf13/cobra`
- Go dependencies removed: `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss` (TUI removed)
- Go dependencies NOT added: `gorm.io/gorm`, `gorm.io/driver/sqlite` (using in-memory cache instead)
- Data directory split: `.rally/` (git-tracked JSONL, gitignored ephemeral files) + `~/.local/share/rally/` (transcripts)
- CLI interface changes: commands restructured under Cobra, `rally run` → `rally relay`, existing TUI removed (CLI-only)
- Removed: scout mode (task discovery out of scope), agent-invoked init steps
- Replaced: `docs/orchestration/rally-progress.yaml` → `.rally/sessions.jsonl` (recent session context fed via prompt; `.rally/README.md` guides agents to access data directly)
- Install/release pipeline: unchanged (goreleaser, install.sh, self-update)
