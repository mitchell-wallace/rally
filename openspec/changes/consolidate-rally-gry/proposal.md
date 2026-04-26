## Why

Rally (v0.1.x) is a proven agent orchestrator — it connects to four coding agent CLIs and runs them in a loop, one task per try, avoiding long-context degradation. However its internal structure is loose and lacks strong verification loops. A sister repo (gry) was built as a structured experiment with an Executor interface, fixture-driven integration testing, dependency injection, and a richer TUI — but only supports one agent. Rally v0.2.0 is inspired by gry's architectural discipline while retaining rally's real-world multi-agent support, eliminating the maintenance burden of two diverging codebases.

## What Changes

- **BREAKING**: Rename core concepts: three-tier naming — "try" (one agent CLI invocation), "run" (one iteration counting against relay target, may include retries), "relay" (a campaign of N runs)
- **BREAKING**: Move repo-accessible data from `~/.local/share/rally` to `.rally/` in the repo root
- **BREAKING**: Consolidate `rally.toml` + `.rally/config` into `.rally/config.toml`
- Introduce `Executor` interface (inspired by gry) for agent lifecycle — `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`
- Add JSONL-in-git as source of truth for tries, messages, relays, and agent status (one file per record type, per-type window sizes)
- Add in-memory cache loaded from JSONL on startup (no external database)
- Add per-agent structured output parsing inline in executors (stream-json for Claude, JSON events for OpenCode, JSON wrapper for Gemini, structured output for Codex) — no hooks, keeping hooks available for per-harness customisation
- Add retry logic with error resilience: retry try (3x within a run) → pause agent (1hr) → freeze agent (5hr) → relay failure. Agent status persisted in dedicated JSONL store across relays.
- Replace hand-rolled CLI flag parsing with Cobra
- Remove existing line-based TUI — rally v0.2.0 is CLI-only (new TUI planned as a separate future change)
- Add relay logging: dual-write to `~/.local/share/rally/relays/relay-N.log` (durable) and `.rally/relays/relay-N.log` (repo cache)
- Add fixture-driven e2e test infrastructure with mock agent CLIs
- Add `.rally/current_task.md` as ephemeral gitignored context file (contains the prompt fed to the agent)
- Add `.rally/README.md` with instructions for agents on accessing rally data (e.g. `tail -10 tries.jsonl`)
- Keep beads as one pluggable task backend (not a hard dependency); rally remains focused on delegating work to agents
- Drop scout mode (task discovery is out of scope — users manage their own workflow for creating beads or similar)
- Simplify `rally init` to programmatic-only operations (git init, create `.rally/` directory, create `.rally/config.toml`) — no agent invocation
- Add `--no-verify` default for auto-commits with opt-in via `run_hooks_on_autocommit` config
- Add git user fallback (`user.name=Rally`, `user.email=rally@localhost`) for container environments

## Capabilities

### New Capabilities
- `executor`: Agent lifecycle abstraction — Executor interface with per-agent implementations, inline structured output parsing, and fixture executor for testing
- `store`: JSONL files in `.rally/` as git-tracked source of truth with in-memory cache for fast queries, loaded on startup. Per-type window sizes (200 tries, 50 relays, 50 agent status events). Messages windowed only when resolved/cancelled. Commit-then-truncate to preserve history in git.
- `relay-runner`: Relay orchestration loop — agent mix cycling, run execution with try retries, error resilience cascade (3x retry → 1hr pause → 5hr freeze → relay failure), graceful stop, relay logging

### Modified Capabilities

## Impact

- All internal packages restructured: `internal/rally/runner/` → `internal/relay/`, new `internal/agent/`, `internal/store/`
- Go dependencies added: `github.com/spf13/cobra`
- Go dependencies removed: `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss` (TUI removed)
- Go dependencies NOT added: `gorm.io/gorm`, `gorm.io/driver/sqlite` (using in-memory cache instead)
- Data directory split: `.rally/` (git-tracked JSONL + config, gitignored ephemeral files + relay log cache) + `~/.local/share/rally/` (transcripts, durable relay logs)
- Config consolidated: `rally.toml` + `.rally/config` → `.rally/config.toml`
- CLI interface changes: commands restructured under Cobra, `rally run` → `rally relay`, existing TUI removed (CLI-only)
- Removed: scout mode (task discovery out of scope), agent-invoked init steps, agent hooks (output parsed inline)
- Replaced: `docs/orchestration/rally-progress.yaml` → `.rally/tries.jsonl` (recent try context fed via prompt; `.rally/README.md` guides agents to access data directly)
- Install/release pipeline: unchanged (goreleaser, install.sh, self-update). Existing release tests portable.
