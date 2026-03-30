## Context

Rally v0.1.x is a Go CLI that orchestrates autonomous coding agents (Claude Code, Codex, Gemini, OpenCode) in a serial loop. It runs inside sandboxed containers alongside the agents it spawns, calling them as subprocesses and capturing stdout. State is stored in YAML files and an append-only JSONL event log at `~/.local/share/rally/`.

A sister repo (gry) was built with stronger architecture: an `Executor` interface for agent abstraction, fixture-driven e2e tests using real git repos and precomputed diffs, Cobra for CLI, and a richer Bubble Tea TUI. However gry only supports Codex.

Rally v0.2.0 consolidates both, using gry's structural patterns as the skeleton and rally's proven agent integration as the runtime. The tool runs in ephemeral sandboxed containers, so local-only state can be lost — repo-committed JSONL files are the durable source of truth.

## Goals / Non-Goals

**Goals:**
- Unified codebase with gry's testability and rally's multi-agent support
- JSONL-in-git source of truth that survives container destruction
- In-memory cache for fast TUI queries, loaded from JSONL at startup
- Executor interface enabling fixture-driven e2e tests without real agent CLIs
- Stop-hook-based structured output collection from all four agents
- Error resilience: retry sessions within a run → pause agent → freeze agent → relay failure cascade
- Full-screen gitui-style TUI with bordered panels, inbox, and live session status
- Cobra CLI replacing hand-rolled flag parsing

**Non-Goals:**
- Parallel agent execution (serial only, for reliability)
- Cloud/shared database (future consideration, not v0.2.0)
- Internalized task/planning model (externalized to beads or similar)
- Dynamic iteration counts driven by task availability (fixed count for now)
- Streaming agent stdout into TUI panels (show stats only)
- External database (SQLite/GORM unnecessary at ~100 record scale; in-memory is sufficient)
- Mock agent CLI binaries (future — see future-proposals.md; FixtureExecutor covers v0.2.0 testing needs)

## Decisions

### 1. JSONL as source of truth, in-memory as cache

**Decision**: One JSONL file per record type (sessions.jsonl, messages.jsonl, relays.jsonl) in `.rally/`, git-tracked. On startup, all records loaded into in-memory Go data structures for fast reads.

**Alternatives considered**:
- SQLite as cache (gry's approach): Adds GORM + SQLite driver dependencies, cache staleness detection, rebuild logic — all for ~100 records that fit trivially in memory
- YAML files only (rally v0.1.x approach): No query capability, slow for dashboard aggregations
- Dolt (git-native SQL): Reliability issues observed in practice — startup failures caused cascading problems

**Rationale**: JSONL is human-readable, diffable, git-mergeable, and agents can grep it directly. At ~100 records per file, loading everything into memory on startup is instant and eliminates an entire class of cache-divergence bugs. The write path is: append to JSONL, then update in-memory structs. Limited to ~100 records per file to keep repo size manageable.

### 2. Executor interface for agent abstraction

**Decision**: Port gry's `Executor` interface with `Execute(ctx, opts) (*SessionResult, error)`. Create per-agent implementations: `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`.

**Alternatives considered**:
- Keep rally's inline subprocess spawning: Untestable, no abstraction boundary
- Plugin system: Over-engineered for four known agents

**Rationale**: The interface is the natural seam for testing. FixtureExecutor replays diffs and canned outputs. Real executors construct CLI commands and parse results. Agent mix cycling lives above the interface in the relay runner.

### 3. Resume-and-report for structured output

**Decision**: After a session completes, the executor resumes the same session with a short reporting prompt requesting the structured JSON report. This preserves the full conversation context and leverages input token caching — the agent already knows what it did. Stop hooks serve as the trigger (signaling session end) and as a fallback for Claude/Codex.

**Resume capabilities (researched + validated)**:
- Claude Code: `claude -c -p "<prompt>" --json-schema '<schema>' --output-format json` — resume + prompt + schema validation
- Codex: `codex exec resume --last "<prompt>" --output-schema ./schema.json -o ./report.json` — resume + prompt + schema file
- Gemini CLI: `gemini --resume -p "<prompt>" --output-format json` — resume + prompt + JSON output. Response is in `{"response": "...", "session_id": "...", "stats": {...}}` wrapper. No schema validation. Stderr is noisy (MCP messages) — discard.
- OpenCode: `opencode run --continue "<prompt>" --format json` — resume + prompt + JSON output (no schema validation)

**Stop hooks as trigger + fallback**:
- All four CLIs have hook systems (Claude `Stop`, Codex `Stop`, Gemini `SessionEnd`, OpenCode `session.idle`)
- The hook fires at session end, signaling the executor to issue the resume command
- For Claude/Codex, stop hooks also serve as fallback via block-and-report (`decision: "block"`) if resume fails

**Alternatives considered**:
- Block-and-report hooks for all agents: Works for Claude/Codex but not Gemini/OpenCode; also wastes a turn inside the session context window
- Post-run transcript parsing: Fragile, agent-specific, no structured contract
- Output schema flags during session: Places schema burden on the agent throughout the session rather than at the reporting moment

**Rationale**: Resume-and-report is the cleanest approach — it happens outside the main session, preserves token caching, and lets rally control the reporting prompt precisely. Three of four CLIs support it natively. Gemini requires a workaround but the output contract remains the same.

### 4. Prompt modes (scout, beads) carried forward as-is

**Decision**: The prompt building system (base template, scout mode, beads mode, session headers, exploration fallback) is ported from rally v0.1.x without redesign. No new spec — the existing prompt package is the spec.

**Rationale**: The prompt system works in production. Redesigning it alongside all the other v0.2.0 changes would add risk without clear benefit. If changes are needed later, they can be a separate change.

### 5. Externalized task tracking (unchanged)

**Decision**: Rally does not own task/planning data. Task context comes via the prompt (beads, or another backend). Rally records what happened (sessions, results) but not what should happen (tasks, sprints).

**Alternatives considered**:
- Internalized Sprint/Phase/Task model (gry's approach): Duplicates external planners, creates sync burden
- Tight beads coupling: Creates hard dependency on a tool with known reliability issues (dolt)

**Rationale**: Rally is an orchestrator, not a planner. Beads (or beads_rust, or another backend) provides `bd ready` / `bd show` for task context. Rally's prompt builder injects this into the agent prompt. The link is through prompt mode configuration, not data model coupling.

### 6. Naming: session + run + relay

**Decision**: Three-tier naming:
- **Session**: One invocation of an agent CLI, regardless of outcome. The fundamental unit. This aligns with the term used by most agent CLIs themselves.
- **Run**: One logical iteration counting against the relay's target count. A run consumes one run-level inbox message and receives the same task context throughout retries. If no failures, one run = one session. On failure, the run retries — each retry is a new session.
- **Relay**: A campaign of N runs with a configured agent mix.

**Alternatives considered**:
- rally v0.1.x naming (session/batch): Only two tiers, no distinction between "one CLI call" and "one iteration"
- gry's naming (run/phase): "Phase" implies hierarchical planning which is externalized

**Rationale**: Three tiers cleanly separate concerns. Session is the atomic unit matching what agent CLIs call their invocations. Run is the retry-boundary and message-consumption unit. Relay is the campaign. Retries don't count against iteration targets — they're new sessions within the same run.

### 7. Git branching: current branch only

**Decision**: Relays run on whatever branch is currently checked out. Rally does not create, switch, or merge branches.

**Alternatives considered**:
- Branch per relay (gry creates branch per phase): Adds git complexity, requires auto-merge logic, conflicts with externalized task model
- Branch per run: Even more overhead for minimal benefit

**Rationale**: Lighter git footprint. Users manage their own branching strategy. Rally just auto-commits to the current branch. This also avoids the merge conflict handling that gry's per-phase branching required.

### 8. Full-screen TUI with bubbles panels

**Decision**: Full-screen Bubble Tea app using the bubbles component library for bordered panels. Layout: dashboard (relay progress + session history), inbox (message management), live session status (runtime, git lines +/-, files changed). Responsive to terminal size.

**Alternatives considered**:
- Line-based TUI (gry's current approach): No boxes, no full-screen, no clean redraws
- Web dashboard: Requires server, adds complexity, doesn't work in sandboxes
- Simple CLI output (rally v0.1.x): No interactivity during relay

**Rationale**: gitui proves that Bubble Tea + bordered panels can create a professional, responsive terminal UI. The dashboard needs to show both current relay state and historical sessions, which benefits from structured panel layout. bubbles provides viewport, list, and other components that reduce boilerplate.

### 9. Data directory layout

**Decision**: Split between `.rally/` (repo root, git-tracked) and `~/.local/share/rally/` (system-local).

`.rally/` contains:
- `sessions.jsonl`, `messages.jsonl`, `relays.jsonl` — source of truth
- `current_task.md` — ephemeral, gitignored, written before each session for agent reference
- `.gitignore` — excludes ephemeral files

`~/.local/share/rally/` contains:
- `sessions/<session-id>/terminal.log` — transcripts
- Config and other internal state

**Rationale**: JSONL in-repo survives container destruction and is accessible across hosts via git. Ephemeral files (task context) are gitignored but locally accessible to agents during sessions. System-local storage handles large/binary data (transcripts) that shouldn't bloat the repo.

## Risks / Trade-offs

- [JSONL git merge conflicts] → Each file is append-only with distinct record IDs; conflicts are mechanically resolvable. The ~100 record window limits file size.
- [Gemini CLI resume output wrapper] → Gemini's `--output-format json` wraps the response in a `{"response": "...", "session_id": "...", "stats": {...}}` envelope. The report JSON is inside the `response` string and must be double-parsed. Gemini stderr is noisy with MCP server messages — must be discarded.
- [Resume command adds a short follow-up session] → The resume-and-report call is a brief additional API call after each session. At ~100 tokens of reporting prompt, this is negligible compared to the main session. Token caching means the context is not re-processed.
- [JSONL file size with many sessions] → Window of ~100 records, older records truncated. Historical data available via git history if needed.
- [bubbles dependency weight] → Adds ~5 transitive dependencies. Acceptable for the UI quality improvement.
- [Container ephemeral state] → Transcripts and current_task.md are lost on container wipe. By design — JSONL is the durable layer, everything else is derived or ephemeral.
- [In-memory cache consistency] → Single process, single writer — no concurrent access concerns. Cache is always rebuilt from JSONL on startup.
