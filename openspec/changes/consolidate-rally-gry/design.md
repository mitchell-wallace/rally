## Context

Rally v0.1.x is a Go CLI that orchestrates autonomous coding agents (Claude Code, Codex, Gemini, OpenCode) in a serial loop. It runs inside sandboxed containers alongside the agents it spawns, calling them as subprocesses and capturing stdout. State is stored in YAML files and an append-only JSONL event log at `~/.local/share/rally/`.

A sister repo (gry) was built with stronger architecture: an `Executor` interface for agent abstraction, fixture-driven e2e tests using real git repos and precomputed diffs, Cobra for CLI, and a richer Bubble Tea TUI. However gry only supports Codex.

Rally v0.2.0 is inspired by gry's architectural discipline while retaining rally's proven multi-agent integration. The tool runs in ephemeral sandboxed containers, so local-only state can be lost — repo-committed JSONL files are the durable source of truth.

## Goals / Non-Goals

**Goals:**
- Unified codebase inspired by gry's testability with rally's multi-agent support
- JSONL-in-git source of truth that survives container destruction
- In-memory cache for fast TUI queries, loaded from JSONL at startup
- Executor interface enabling fixture-driven e2e tests without real agent CLIs
- Per-agent structured output collection (block-and-report for Claude, resume-and-report for Codex/OpenCode, hybrid for Gemini)
- Error resilience: retry sessions within a run → pause agent → freeze agent → relay failure cascade. Agent status persisted across relays.
- Full-screen gitui-style TUI with bordered panels, inbox, live session status, and relay start overlay
- Cobra CLI replacing hand-rolled flag parsing

**Non-Goals:**
- Parallel agent execution (serial only, for reliability)
- Cloud/shared database (future consideration, not v0.2.0)
- Internalized task/planning model (externalized to beads or similar)
- Dynamic iteration counts driven by task availability (fixed count for now)
- Streaming agent stdout into TUI panels (show stats only)
- External database (SQLite/GORM unnecessary at ~100 record scale; in-memory is sufficient)
- Mock agent CLI binaries (future — see future-proposals.md; FixtureExecutor covers v0.2.0 testing needs)
- Scout mode (task discovery is out of scope — users manage their own workflow)
- Agent-invoked init steps (init is programmatic only: git init, create `.rally/`)

## Decisions

### 1. JSONL as source of truth, in-memory as cache

**Decision**: One JSONL file per record type in `.rally/`, git-tracked: `sessions.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl`. On startup, all records loaded into in-memory Go data structures for fast reads. Session IDs in rally's stores are named `relay_session_id` to disambiguate from agent CLI session identifiers.

Per-type window sizes reflect different population rates:
- Sessions: 200 records
- Relays: 50 records
- Agent status events: 50 records
- Messages: windowed only when resolved/cancelled (pending messages are never truncated)

When appending would exceed the window, the store commits the current file, then truncates and commits again — ensuring all records are preserved in git history.

**Alternatives considered**:
- SQLite as cache (gry's approach): Adds GORM + SQLite driver dependencies, cache staleness detection, rebuild logic — all for records that fit trivially in memory
- YAML files only (rally v0.1.x approach): No query capability, slow for dashboard aggregations
- Dolt (git-native SQL): Reliability issues observed in practice — startup failures caused cascading problems
- Uniform ~100 record window: Different record types populate at very different rates; uniform limits would either over-retain relays or under-retain sessions

**Rationale**: JSONL is human-readable, diffable, git-mergeable, and agents can grep it directly. Loading everything into memory on startup is instant and eliminates an entire class of cache-divergence bugs. The write path is: append to JSONL, then update in-memory structs. The commit-then-truncate approach ensures nothing is lost even when files are trimmed.

### 2. Executor interface for agent abstraction

**Decision**: Port gry's `Executor` interface with `Execute(ctx, opts) (*SessionResult, error)`. Create per-agent implementations: `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`.

**Alternatives considered**:
- Keep rally's inline subprocess spawning: Untestable, no abstraction boundary
- Plugin system: Over-engineered for four known agents

**Rationale**: The interface is the natural seam for testing. FixtureExecutor replays diffs and canned outputs. Real executors construct CLI commands and parse results. Agent mix cycling lives above the interface in the relay runner.

### 3. Per-agent structured output collection

**Decision**: Each agent uses the most reliable output collection strategy available for its CLI. This is not one-size-fits-all — different agents have different CLI maturity levels and known issues. All CLI flags below have been tested and verified.

**Per-agent strategies**:
- **Claude Code: block-and-report (primary)**. Uses stop hook with `decision: "block"` to force a reporting turn, then parses `last_assistant_message` on the second stop event. Resume (`claude -c -p "<prompt>" --json-schema '<schema>' --output-format json`) exists but is demoted due to a potential cache invalidation bug in Claude Code's resume behavior.
- **Codex: resume-and-report (primary)**. `codex exec resume --last "<prompt>" --output-schema ./schema.json -o ./report.json`. Codex hooks are flagged as experimental, so resume is preferred. Block-and-report via stop hook available as fallback.
- **Gemini CLI: resume-and-report**. `gemini --resume -p "<prompt>" --output-format json`. Response is in `{"response": "...", "session_id": "...", "stats": {...}}` wrapper — report JSON inside `response` must be double-parsed. No schema validation. Stderr is noisy (MCP messages) — discard.
- **OpenCode: resume-and-report**. `opencode run --continue "<prompt>" --format json`. No schema validation — prompt-guided.

**Hook systems (for triggering and fallback)**:
- All four CLIs have hook systems (Claude `Stop`, Codex `Stop`, Gemini `SessionEnd`, OpenCode `session.idle`)
- Hooks signal session end to the executor, triggering the appropriate collection strategy
- For Claude, the stop hook IS the primary strategy (block-and-report)
- For Codex, the stop hook is a fallback if resume fails

**Alternatives considered**:
- Uniform resume-and-report for all agents: Cleaner in theory, but Claude's potential cache invalidation issue and Codex's experimental hooks make per-agent strategies more reliable in practice
- Post-run transcript parsing: Fragile, agent-specific, no structured contract
- Output schema flags during session: Places schema burden on the agent throughout the session rather than at the reporting moment

**Rationale**: The reality of orchestrating four different agent CLIs is that each has different maturity and quirks. A per-agent strategy is more complex but more reliable. The output contract (JSON report schema) remains the same regardless of collection method.

### 4. Prompt modes (beads only) carried forward; scout dropped

**Decision**: The prompt building system (base template, beads mode, session headers, exploration fallback) is ported from rally v0.1.x without redesign, minus scout mode. No new spec — the existing prompt package is the spec. Recent session context (summaries, remaining work) is fed into prompts similarly to how `rally-progress.yaml` worked, but sourced from `sessions.jsonl`. A `.rally/README.md` provides agents with instructions for accessing rally data directly (e.g. `tail -10 sessions.jsonl`).

**Scout mode dropped**: Scout was built around gry's task model (discover tasks, create beads). With rally's externalized task management, task discovery is the user's workflow — out of scope for the orchestrator.

**Rationale**: The prompt system works in production. Redesigning it alongside all the other v0.2.0 changes would add risk without clear benefit. Dropping scout simplifies the prompt system. If changes are needed later, they can be a separate change.

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

### 8. Agent status store (separate from relay records)

**Decision**: Agent status (pause/freeze state, failure timestamps, hourly retry history) is tracked in a dedicated `agent_status.jsonl` file, separate from relay records. This store persists across relays with a 50-event window.

**Alternatives considered**:
- Embed in relay records: Pause/freeze state needs to persist across relays. If an agent is frozen at the end of one relay, a new relay should know about it.
- In-memory only: Lost on restart, timers reset

**Rationale**: Agent reliability history is a distinct concern from relay execution logs. A separate store keeps relay records clean and allows agent status to span multiple relays. The 50-event window provides sufficient history for debugging while keeping the file small.

### 9. Full-screen TUI with bubbles panels

**Decision**: Full-screen Bubble Tea app using the bubbles component library for bordered panels. Layout: dashboard (relay progress + session history), inbox (message management), live session status (runtime, git lines +/-, files changed). Responsive to terminal size.

**Alternatives considered**:
- Line-based TUI (gry's current approach): No boxes, no full-screen, no clean redraws
- Web dashboard: Requires server, adds complexity, doesn't work in sandboxes
- Simple CLI output (rally v0.1.x): No interactivity during relay

**Rationale**: gitui proves that Bubble Tea + bordered panels can create a professional, responsive terminal UI. The dashboard needs to show both current relay state and historical sessions, which benefits from structured panel layout. bubbles provides viewport, list, and other components that reduce boilerplate.

### 10. Data directory layout

**Decision**: Split between `.rally/` (repo root, git-tracked) and `~/.local/share/rally/` (system-local).

`.rally/` contains:
- `sessions.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl` — source of truth
- `README.md` — instructions for agents on accessing rally data (e.g. `tail -10 sessions.jsonl` for recent context)
- `current_task.md` — ephemeral, gitignored, contains the prompt fed to the agent
- `.gitignore` — excludes ephemeral files

`~/.local/share/rally/` contains:
- `sessions/<session-id>/terminal.log` — transcripts
- Config and other internal state

**Replaces**: `docs/orchestration/rally-progress.yaml` from v0.1.x. Recent session context (summaries, remaining work) is now sourced from `sessions.jsonl` and fed into prompts directly.

**Rationale**: JSONL in-repo survives container destruction and is accessible across hosts via git. Ephemeral files (task context) are gitignored but locally accessible to agents during sessions. System-local storage handles large/binary data (transcripts) that shouldn't bloat the repo. The `.rally/README.md` gives agents a self-service path to access rally data without needing it all injected into the prompt.

### 11. Commit hash tracking

**Decision**: The relay runner (not the executor) is responsible for tracking commit hashes. Before a session, the runner records the current HEAD. After the session, the runner checks HEAD again. If the agent committed (HEAD changed), use that hash. If the agent left uncommitted changes, the runner auto-commits and uses that hash. If there are no changes, the session result records no commit hash.

**Rationale**: Agents typically commit their own changes with descriptive messages. The runner should respect those commits rather than always auto-committing. This keeps the git history readable while ensuring no changes are lost.

### 12. Relay resume

**Decision**: When rally launches and an incomplete relay exists, the TUI displays a modal prompt offering to resume. Resuming continues with the relay's existing settings (iteration target, agent mix). The UI hints at the relay's state (completed/total runs, agent mix).

**Rationale**: Explicit modal avoids accidental resumption or silent discard. Preserving original settings ensures the relay continues as configured.

## Risks / Trade-offs

- [JSONL git merge conflicts] → Each file is append-only with distinct record IDs; conflicts are mechanically resolvable. Commit-then-truncate ensures full history in git.
- [Gemini CLI resume output wrapper] → Gemini's `--output-format json` wraps the response in a `{"response": "...", "session_id": "...", "stats": {...}}` envelope. The report JSON is inside the `response` string and must be double-parsed. Gemini stderr is noisy with MCP server messages — must be discarded.
- [Per-agent strategy complexity] → Different collection strategies per agent adds implementation complexity. Justified by reliability — each agent CLI has different maturity and quirks. The output contract (JSON schema) is uniform.
- [Claude cache invalidation with resume] → Potential bug in Claude Code's resume behavior may invalidate input token cache. Block-and-report via stop hook avoids this entirely. If the bug is resolved, Claude can be switched to resume-and-report later.
- [JSONL file size with many sessions] → Per-type windows (200 sessions, 50 relays, 50 agent status). Commit-then-truncate preserves history in git.
- [bubbles dependency weight] → Adds ~5 transitive dependencies. Acceptable for the UI quality improvement.
- [Container ephemeral state] → Transcripts and current_task.md are lost on container wipe. By design — JSONL is the durable layer, everything else is derived or ephemeral.
- [In-memory cache consistency] → Single process, single writer — no concurrent access concerns. Cache is always rebuilt from JSONL on startup.
