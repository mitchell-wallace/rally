## Context

Rally v0.1.x is a Go CLI that orchestrates autonomous coding agents (Claude Code, Codex, Gemini, OpenCode) in a serial loop. It runs inside sandboxed containers alongside the agents it spawns, calling them as subprocesses and capturing stdout. State is stored in YAML files and an append-only JSONL event log at `~/.local/share/rally/`.

A sister repo (gry) was built with stronger architecture: an `Executor` interface for agent abstraction, fixture-driven e2e tests using real git repos and precomputed diffs, SQLite/GORM for structured data, Cobra for CLI, and a richer Bubble Tea TUI. However gry only supports Codex.

Rally v0.2.0 consolidates both, using gry's structural patterns as the skeleton and rally's proven agent integration as the runtime. The tool runs in ephemeral sandboxed containers, so local-only state (SQLite) can be lost — repo-committed JSONL files are the durable source of truth.

## Goals / Non-Goals

**Goals:**
- Unified codebase with gry's testability and rally's multi-agent support
- JSONL-in-git source of truth that survives container destruction
- SQLite cache for fast TUI queries, rebuildable from JSONL at any time
- Executor interface enabling fixture-driven e2e tests without real agent CLIs
- Stop-hook-based structured output from all four agents
- Error resilience: retry → pause → freeze → relay failure cascade
- Full-screen gitui-style TUI with bordered panels, inbox, and live run status
- Cobra CLI replacing hand-rolled flag parsing

**Non-Goals:**
- Parallel agent execution (serial only, for reliability)
- Cloud/shared database (future consideration, not v0.2.0)
- Internalized task/planning model (externalized to beads or similar)
- Dynamic iteration counts driven by task availability (fixed count for now)
- Streaming agent stdout into TUI panels (show stats only)
- Mock agent CLIs (planned but separate from this change)

## Decisions

### 1. JSONL as source of truth, SQLite as cache

**Decision**: One JSONL file per record type (runs.jsonl, messages.jsonl, relays.jsonl) in `.rally/`, git-tracked. SQLite in `~/.local/share/rally/rally.db`, gitignored, rebuilt from JSONL on startup.

**Alternatives considered**:
- SQLite only (gry's approach): Lost on container wipe, doesn't work with git version control
- YAML files only (rally v0.1.x approach): No query capability, slow for dashboard aggregations
- Dolt (git-native SQL): Reliability issues observed in practice — startup failures caused cascading problems

**Rationale**: JSONL is human-readable, diffable, git-mergeable, and agents can grep it directly. SQLite provides fast materialized views for the TUI. The write path is: append to JSONL, then update SQLite. On cold start, replay JSONL into SQLite. Limited to ~100 records per file to keep repo size manageable.

### 2. Executor interface for agent abstraction

**Decision**: Port gry's `Executor` interface with `Execute(ctx, opts) (*RunResult, error)`. Create per-agent implementations: `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`.

**Alternatives considered**:
- Keep rally's inline subprocess spawning: Untestable, no abstraction boundary
- Plugin system: Over-engineered for four known agents

**Rationale**: The interface is the natural seam for testing. FixtureExecutor replays diffs and canned outputs. Real executors construct CLI commands and parse results. Agent mix cycling lives above the interface in the relay runner.

### 3. Stop hooks for structured output

**Decision**: Each agent's native hook system (Claude Code stop hooks, Gemini AfterAgent, Codex/OpenCode equivalents) invokes a rally-provided prompt at run end. The hook asks the agent to report: completed (bool), summary, remaining_work, message_addressed, files_changed. Output parsed as JSON.

**Alternatives considered**:
- Structured output mode (gry's `--output-schema`): Forces agent to maintain output schema awareness throughout the session, placing instructions far from the reporting event
- Post-run transcript parsing: Fragile, agent-specific, no structured contract

**Rationale**: Stop hooks place the reporting instruction at the moment of reporting, reducing cognitive load on the agent during the session. All four agent CLIs support command-based hooks. Exception: Gemini requires JSON output mode as it streams everything otherwise.

### 4. Externalized task tracking

**Decision**: Rally does not own task/planning data. Task context comes via the prompt (beads, or another backend). Rally records what happened (runs, results) but not what should happen (tasks, sprints).

**Alternatives considered**:
- Internalized Sprint/Phase/Task model (gry's approach): Duplicates external planners, creates sync burden
- Tight beads coupling: Creates hard dependency on a tool with known reliability issues (dolt)

**Rationale**: Rally is an orchestrator, not a planner. Beads (or beads_rust, or another backend) provides `bd ready` / `bd show` for task context. Rally's prompt builder injects this into the agent prompt. The link is through prompt mode configuration, not data model coupling.

### 5. Naming: run + relay

**Decision**: Rename "session" → "run" (one agent invocation) and "batch" → "relay" (a campaign of N runs with an agent mix).

**Alternatives considered**:
- Keep rally's naming (session/batch): Less descriptive
- Use gry's naming fully (run/phase): "Phase" implies hierarchical planning which is externalized

**Rationale**: "Run" is more concrete than "session" for a single agent execution. "Relay" captures the serial handoff between agents. Both terms are self-explanatory without needing to understand a planning hierarchy.

### 6. Full-screen TUI with bubbles panels

**Decision**: Full-screen Bubble Tea app using the bubbles component library for bordered panels. Layout: dashboard (relay progress + run history), inbox (message management), live run status (runtime, git lines +/-, files changed). Responsive to terminal size.

**Alternatives considered**:
- Line-based TUI (gry's current approach): No boxes, no full-screen, no clean redraws
- Web dashboard: Requires server, adds complexity, doesn't work in sandboxes
- Simple CLI output (rally v0.1.x): No interactivity during relay

**Rationale**: gitui proves that Bubble Tea + bordered panels can create a professional, responsive terminal UI. The dashboard needs to show both current relay state and historical runs, which benefits from structured panel layout. bubbles provides viewport, list, and other components that reduce boilerplate.

### 7. Data directory layout

**Decision**: Split between `.rally/` (repo root, git-tracked) and `~/.local/share/rally/` (system-local).

`.rally/` contains:
- `runs.jsonl`, `messages.jsonl`, `relays.jsonl` — source of truth
- `current_task.md` — ephemeral, gitignored, written before each run for agent reference
- `.gitignore` — excludes ephemeral files and SQLite

`~/.local/share/rally/` contains:
- `rally.db` — SQLite cache
- `sessions/<run-id>/terminal.log` — transcripts
- Config and other internal state

**Rationale**: JSONL in-repo survives container destruction and is accessible across hosts via git. Ephemeral files (task context, SQLite) are gitignored but locally accessible to agents during runs. System-local storage handles large/binary data (transcripts) that shouldn't bloat the repo.

## Risks / Trade-offs

- [JSONL git merge conflicts] → Each file is append-only with distinct record IDs; conflicts are mechanically resolvable. The ~100 record window limits file size.
- [SQLite cache diverges from JSONL] → Cache is always rebuilt from JSONL on startup. No writes go to SQLite without first going to JSONL.
- [Stop hook compatibility across agents] → Each agent CLI has a different hook API. Mitigation: per-agent executor handles hook registration, rally provides the prompt template.
- [Gemini JSON output exception] → Gemini requires JSON output mode rather than stop hooks. The GeminiExecutor handles this difference internally behind the Executor interface.
- [JSONL file size with many runs] → Window of ~100 records, older records truncated. Historical data available via git history if needed.
- [bubbles dependency weight] → Adds ~5 transitive dependencies. Acceptable for the UI quality improvement.
- [Container ephemeral state] → SQLite, transcripts, and current_task.md are lost on container wipe. By design — JSONL is the durable layer, everything else is derived or ephemeral.
