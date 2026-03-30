---
name: rally-gry-consolidation
description: Consolidating rally and gry repos into rally v0.2.0 - architecture decisions and approach
type: project
---

Rally v0.2.0 consolidation of rally + gry repos using "Approach A" (gry skeleton, rally organs).

**Why:** Rally has proven agent integration (4 agents, CLI calls, sandbox auto-install) but loose structure. Gry has strong architecture (Executor interface, fixture testing, DI) but only supports Codex. Merging gets the best of both.

**How to apply:**
- Use gry's Executor interface, fixture testing, Cobra CLI, structured error types, DI patterns as structural foundation
- Port rally's multi-agent support, beads/scout modes, self-update, install pipeline as runtime features
- Data storage: JSONL in git as source of truth (one file per table, ~100 record window), SQLite as local cache (not primary store) — because rally runs inside sandboxed containers where local DBs may be wiped, and SQLite doesn't work with git version control
- Task structure: Externalize planning/issue tracking (rally's approach) via beads or similar (beads_rust/JSONL variant preferred over dolt-based beads due to dolt reliability issues), rally focuses on delegating work to agents — NOT gry's internalized Sprint/Phase/Task model
- Semi-realistic mock CLIs for each agent planned for integration tests (accept same CLI shape, simulate runs with short timeouts)
- Naming: "run" (not session) and "relay" (not batch) — from gry's naming
- Rally runs INSIDE sandboxes alongside agents, calls agents as subcommands, grabs stdout on finish
- Serial execution only (no parallel agents planned) — reliability over throughput
- Agent output: use stop hooks to collect structured results at end of run, not structured output mode (less for agent to remember, instructions closer to event). Exception: Gemini needs JSON output mode as it streams everything otherwise
- Beads is just one task backend, not a hard dependency — may switch to beads_rust (JSONL+SQLite) due to dolt reliability issues
- Fixed iteration count for now; dynamic (task-availability-driven) depends on task backend commitment
- Dashboard: full-screen takeover with gitui-style box panels (use bubbles lib), inbox/messaging, live relay status (runtime, git lines +/-, files changed — no stdout streaming), responsive UI
- Stop hooks: rally provides a hook script/prompt that runs as agent's stop hook (Claude stop hook, Gemini AfterAgent, Codex/OpenCode equivalents). Hook asks agent to report in structured format. All agents support command-based hooks similar to Claude Code pattern.
- Context passing: prompt as CLI argument (no issues with length so far), plus .rally/current_task.md as ephemeral gitignored file agents can reference
- Data layout: .rally/ in repo root for repo-accessible stuff (current task, JSONL data files), system-local (~/.local/share/rally) for SQLite cache and rally internal state
- No env vars for context passing — use prompt and context files instead
