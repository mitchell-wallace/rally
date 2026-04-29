## Context

Rally v0.1.x is a Go CLI that orchestrates autonomous coding agents (Claude Code, Codex, Gemini, OpenCode) in a serial loop. It runs inside sandboxed containers alongside the agents it spawns, calling them as subprocesses and capturing stdout. State is stored in YAML files and an append-only JSONL event log at `~/.local/share/rally/`.

A sister repo (gry) was built with stronger architecture: an `Executor` interface for agent abstraction, fixture-driven e2e tests using real git repos and precomputed diffs, Cobra for CLI, and a richer Bubble Tea TUI. However gry only supports Codex.

Rally v0.2.0 is inspired by gry's architectural discipline while retaining rally's proven multi-agent integration. The tool runs in ephemeral sandboxed containers, so local-only state can be lost — repo-committed JSONL files are the durable source of truth.

## Goals / Non-Goals

**Goals:**
- Unified codebase inspired by gry's testability with rally's multi-agent support
- JSONL-in-git source of truth that survives container destruction
- In-memory cache for fast CLI queries, loaded from JSONL at startup
- Executor interface enabling fixture-driven e2e tests without real agent CLIs
- Per-agent inline structured output parsing (stream-json for Claude, JSON events for OpenCode, JSON wrapper for Gemini, structured output for Codex) — no hooks
- Error resilience: retry tries within a run → pause agent → freeze agent → relay failure cascade. Agent status persisted across relays.
- CLI-only interface via Cobra (existing TUI removed; new TUI planned as a separate future change)
- Cobra CLI replacing hand-rolled flag parsing

**Non-Goals:**
- Parallel agent execution (serial only, for reliability)
- Cloud/shared database (future consideration, not v0.2.0)
- Internalized task/planning model (externalized to beads or similar)
- Dynamic iteration counts driven by task availability (fixed count for now)
- Streaming agent stdout to the user (relay runner shows stats only, not raw output)
- External database (SQLite/GORM unnecessary at ~100 record scale; in-memory is sufficient)
- Mock agent CLI binaries (future — see future-proposals.md; FixtureExecutor covers v0.2.0 testing needs)
- Scout mode (task discovery is out of scope — users manage their own workflow)
- Agent-invoked init steps (init is programmatic only: git init, create `.rally/`)
- Agent hooks for output collection (hooks left entirely free for per-harness customisations)

## Decisions

### 1. JSONL as source of truth, in-memory as cache

**Decision**: One JSONL file per record type in `.rally/`, git-tracked: `tries.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl`. On startup, all records loaded into in-memory Go data structures for fast reads.

Per-type window sizes reflect different population rates:
- Tries: 200 records
- Relays: 50 records
- Agent status events: 50 records
- Messages: windowed only when resolved/cancelled (pending messages are never truncated)

When appending would exceed the window, the store commits the current file, then truncates and commits again — ensuring all records are preserved in git history.

**Alternatives considered**:
- SQLite as cache (gry's approach): Adds GORM + SQLite driver dependencies, cache staleness detection, rebuild logic — all for records that fit trivially in memory
- YAML files only (rally v0.1.x approach): No query capability, slow for dashboard aggregations
- Dolt (git-native SQL): Reliability issues observed in practice — startup failures caused cascading problems
- Uniform ~100 record window: Different record types populate at very different rates; uniform limits would either over-retain relays or under-retain tries

**Rationale**: JSONL is human-readable, diffable, git-mergeable, and agents can grep it directly. Loading everything into memory on startup is instant and eliminates an entire class of cache-divergence bugs. The write path is: append to JSONL, then update in-memory structs. The commit-then-truncate approach ensures nothing is lost even when files are trimmed.

### 2. Executor interface for agent abstraction

**Decision**: Port gry's `Executor` interface with `Execute(ctx, opts) (*TryResult, error)`. Create per-agent implementations: `ClaudeExecutor`, `CodexExecutor`, `GeminiExecutor`, `OpenCodeExecutor`, `FixtureExecutor`. Git operations (commit hash tracking, auto-commit, repo root detection) are shared helpers used by the relay runner, not reimplemented per executor.

**Alternatives considered**:
- Keep rally's inline subprocess spawning: Untestable, no abstraction boundary
- Plugin system: Over-engineered for four known agents

**Rationale**: The interface is the natural seam for testing. FixtureExecutor replays diffs and canned outputs. Real executors construct CLI commands and parse results. Agent mix cycling lives above the interface in the relay runner. Git operations are a runner concern, not an executor concern.

### 3. Per-agent inline output parsing (no hooks)

**Decision**: Each executor parses structured output inline from the agent's stdout stream during the primary session. No hooks are registered. This follows the approach already proven on main (v0.1.x).

**Per-agent output formats**:
- **Claude Code**: `--output-format stream-json --verbose`. Parses NDJSON stream of `claudeStreamEvent` structs, extracts `result` from events with `type: "result"`.
- **Codex**: Structured output via `--output-schema ./schema.json -o ./report.json`. Parse output file.
- **Gemini CLI**: `--output-format json`. Response wrapped in `{"response": "...", "session_id": "...", "stats": {...}}` — extract and re-parse `response` field. Stderr is noisy (MCP messages) — discard.
- **OpenCode**: `--format json`. Parses NDJSON stream of `opencodeJSONEvent` structs, extracts `text` from events with `type: "text"`.

**Alternatives considered**:
- Hook-based block-and-report (previous plan): Conflicts with using hooks for per-harness optimisation. Hooks are a limited resource — rally shouldn't consume them for its own output parsing.
- Post-run transcript parsing: Fragile, agent-specific, no structured contract.
- Resume-and-report: Adds a second agent invocation after each try — doubles API cost and time.

**Rationale**: Inline parsing is simpler, cheaper, and already working in production on v0.1.x. Keeping hooks entirely free means each deployment harness can customise hooks for its own needs (e.g., auto-approval, metrics, notifications) without conflicting with rally's output collection.

### 4. Prompt modes (beads only) carried forward; scout dropped

**Decision**: The prompt building system (base template, beads mode, try headers, exploration fallback) is ported from rally v0.1.x without redesign, minus scout mode. No new spec — the existing prompt package is the spec. Recent try context (summaries, remaining work) is fed into prompts similarly to how `rally-progress.yaml` worked, but sourced from `tries.jsonl`. A `.rally/README.md` provides agents with instructions for accessing rally data directly (e.g. `tail -10 tries.jsonl`).

**Scout mode dropped**: Scout was built around gry's task model (discover tasks, create beads). With rally's externalized task management, task discovery is the user's workflow — out of scope for the orchestrator.

**Rationale**: The prompt system works in production. Redesigning it alongside all the other v0.2.0 changes would add risk without clear benefit. Dropping scout simplifies the prompt system. If changes are needed later, they can be a separate change.

### 5. Externalized task tracking (unchanged)

**Decision**: Rally does not own task/planning data. Task context comes via the prompt (beads, or another backend). Rally records what happened (tries, results) but not what should happen (tasks, sprints).

**Alternatives considered**:
- Internalized Sprint/Phase/Task model (gry's approach): Duplicates external planners, creates sync burden
- Tight beads coupling: Creates hard dependency on a tool with known reliability issues (dolt)

**Rationale**: Rally is an orchestrator, not a planner. Beads (or beads_rust, or another backend) provides `bd ready` / `bd show` for task context. Rally's prompt builder injects this into the agent prompt. The link is through prompt mode configuration, not data model coupling.

### 6. Naming: try + run + relay

**Decision**: Three-tier naming:
- **Try**: One invocation of an agent CLI, regardless of outcome. The atomic unit. Previously called "session" in earlier plans, renamed to "try" to avoid ambiguity with agent CLI session identifiers and to better convey the retry semantics.
- **Run**: One logical iteration counting against the relay's target count. A run consumes one run-level inbox message and receives the same task context throughout retries. If no failures, one run = one try. On failure, the run retries — each retry is a new try.
- **Relay**: A campaign of N runs with a configured agent mix.

**Alternatives considered**:
- rally v0.1.x naming (session/batch): Only two tiers, no distinction between "one CLI call" and "one iteration"
- gry's naming (run/phase): "Phase" implies hierarchical planning which is externalized
- session/run/relay: "Session" collides with agent CLI session identifiers, creates confusion

**Rationale**: Three tiers cleanly separate concerns. Try is the atomic unit — a single attempt. Run is the retry-boundary and message-consumption unit. Relay is the campaign. Retries don't count against iteration targets — they're new tries within the same run.

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

### 9. CLI-only interface (TUI removed)

**Decision**: Remove the existing line-based Bubble Tea TUI. Rally v0.2.0 is CLI-only — `rally relay` starts a relay from the command line with flags for iteration count and agent mix. The existing TUI is removed as part of the old `internal/rally/` cleanup. A new TUI will be built as a separate future change once the core architecture is stable.

**Alternatives considered**:
- Keep the existing TUI: It's tightly coupled to v0.1.x state/runner internals, which are being replaced entirely. Porting it would add scope without value.
- Build the new full-screen TUI now: Adds significant scope. Better to stabilize the core (store, executor, relay runner) first, then build the TUI on solid foundations.

**Rationale**: Focusing on CLI-only for v0.2.0 reduces scope, ensures the core architecture is solid, and avoids building a TUI on top of APIs that are still being designed. The CLI provides all the functionality needed to run relays.

### 10. Data directory layout

**Decision**: Split between `.rally/` (repo root, git-tracked + gitignored sections) and `~/.local/share/rally/` (system-local).

`.rally/` contains:
- `config.toml` — consolidated configuration (agent models, beads mode, runtime paths, auto-commit hooks)
- `tries.jsonl`, `messages.jsonl`, `relays.jsonl`, `agent_status.jsonl` — source of truth
- `README.md` — instructions for agents on accessing rally data (e.g. `tail -10 tries.jsonl` for recent context)
- `current_task.md` — ephemeral, gitignored, contains the prompt fed to the agent
- `relays/relay-N.log` — recent relay log cache (gitignored, 10-file limit)
- `.gitignore` — excludes ephemeral files and relay log cache

`~/.local/share/rally/` contains:
- `tries/<try-id>/terminal.log` — transcripts
- `relays/relay-N.log` — durable relay logs (full history)

**Config consolidation**: `rally.toml` (workspace root) and `.rally/config` (env-style runtime paths) from v0.1.x are merged into `.rally/config.toml`. This eliminates the ambiguity of having two config files with overlapping concerns.

**Replaces**: `docs/orchestration/rally-progress.yaml` from v0.1.x. Recent try context (summaries, remaining work) is now sourced from `tries.jsonl` and fed into prompts directly.

**Rationale**: JSONL in-repo survives container destruction and is accessible across hosts via git. Ephemeral files (task context) are gitignored but locally accessible to agents during tries. System-local storage handles large/binary data (transcripts, full relay logs) that shouldn't bloat the repo. The `.rally/README.md` gives agents a self-service path to access rally data without needing it all injected into the prompt.

### 11. Commit hash tracking and auto-commit hardening

**Decision**: The relay runner (not the executor) is responsible for tracking commit hashes and auto-committing. Before a try, the runner records the current HEAD. After the try, the runner checks HEAD again. If the agent committed (HEAD changed), use that hash. If the agent left uncommitted changes, the runner auto-commits and uses that hash. If there are no changes, the try result records no commit hash.

**Auto-commit hardening** (ported from v0.1.x main):
- Auto-commits use `--no-verify` by default to prevent repo hooks from blocking progress/logging commits. Opt-in via `run_hooks_on_autocommit = true` in `.rally/config.toml`.
- Git user fallback: if `user.name` or `user.email` are not configured (common in containers), rally sets `user.name=Rally` and `user.email=rally@localhost` for the commit.
- Shared git helper functions (`gitRepoRoot`, `gitOutput`, `gitUserFallbackConfig`) used by the runner — not reimplemented per executor.

**Rationale**: Agents typically commit their own changes with descriptive messages. The runner should respect those commits rather than always auto-committing. `--no-verify` prevents hooks from interfering with rally's operational commits. Git user fallback ensures commits work in minimal container environments.

### 12. Relay resume

**Decision**: When `rally relay` is invoked and an incomplete relay exists, rally prints a summary of the incomplete relay (completed/total runs, agent mix) and prompts the user to resume or discard. A `--resume` flag can skip the prompt and resume automatically.

**Rationale**: CLI prompt is simple and predictable. The `--resume` flag enables scripted/automated usage without interaction.

### 13. Relay logging

**Decision**: Relay logs are human-readable text logs capturing filtered output from all tries within a relay. Dual-write: `~/.local/share/rally/relays/relay-N.log` (durable, full history) and `.rally/relays/relay-N.log` (repo cache, 10-file limit, gitignored). This continues the batch logging system from v0.1.x, renamed from "batch" to "relay" to match the new naming.

**Rationale**: JSONL is the structured source of truth, but human-readable logs are valuable for quick debugging and for agents to reference recent context. The dual-write ensures durable history in the system-local directory while keeping recent logs accessible in the repo for agents.

## Risks / Trade-offs

- [JSONL git merge conflicts] → Each file is append-only with distinct record IDs; conflicts are mechanically resolvable. Commit-then-truncate ensures full history in git.
- [Gemini CLI output wrapper] → Gemini's `--output-format json` wraps the response in a `{"response": "...", "session_id": "...", "stats": {...}}` envelope. The report JSON inside `response` must be double-parsed. Gemini stderr is noisy with MCP server messages — must be discarded.
- [Inline output parsing per agent] → Different parsing strategies per agent adds implementation complexity. Justified by the simplicity of not using hooks and the fact that this is already working on v0.1.x main.
- [JSONL file size with many tries] → Per-type windows (200 tries, 50 relays, 50 agent status). Commit-then-truncate preserves history in git.
- [Container ephemeral state] → Transcripts, current_task.md, and relay log cache are lost on container wipe. By design — JSONL is the durable layer, everything else is derived or ephemeral.
- [In-memory cache consistency] → Single process, single writer — no concurrent access concerns. Cache is always rebuilt from JSONL on startup.
- [Config consolidation migration] → Users with existing `rally.toml` files need to move config to `.rally/config.toml`. `rally init` can auto-migrate.
