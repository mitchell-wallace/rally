## 1. Project Scaffolding

- [ ] 1.1 Add Cobra dependency to go.mod (no GORM/SQLite, no bubbles)
- [ ] 1.2 Create new package layout: `internal/agent/`, `internal/store/`, `internal/relay/`
- [ ] 1.3 Set up `.rally/` directory structure with `.gitignore` (exclude `current_task.md`)
- [ ] 1.4 Migrate CLI from hand-rolled flags to Cobra root command with subcommands (`relay`, `init`, `instructions`, `update`, `version`)
- [ ] 1.5 Make `rally relay` the primary command for starting/resuming relays (CLI-only, no TUI)
- [ ] 1.6 Simplify `rally init` to programmatic-only: git init, create `.rally/` directory, create `.rally/.gitignore`, create `.rally/README.md`. No agent invocation.
- [ ] 1.7 Create `.rally/README.md` template with instructions for agents on accessing rally data (e.g. `tail -10 sessions.jsonl` for recent context)

## 2. Store Layer

- [ ] 2.1 Define JSONL record types: `SessionRecord` (with `relay_session_id`), `MessageRecord`, `RelayRecord`, `AgentStatusEvent` with JSON tags
- [ ] 2.2 Implement JSONL append/read/rewrite functions for a generic record type
- [ ] 2.3 Implement per-type record windowing: 200 sessions, 50 relays, 50 agent status events. Messages windowed only when resolved/cancelled (pending messages exempt).
- [ ] 2.4 Implement commit-then-truncate: when window exceeded, commit current file to git, truncate to window size, commit again
- [ ] 2.5 Implement in-memory cache: load all JSONL files into typed slices/maps on startup
- [ ] 2.6 Implement unified Store interface: write (JSONL append/rewrite + cache update) and read (in-memory query)
- [ ] 2.7 Implement message model: JSON objects updated in place (not event-sourced), with position-based FIFO ordering
- [ ] 2.8 Implement relay record with fields: id, target_iterations, completed_iterations, agent_mix, started_at, ended_at, first_relay_session_id, last_relay_session_id, consumed_message_ids
- [ ] 2.9 Implement agent status store: dedicated `agent_status.jsonl` with pause/freeze/unfreeze/active events, persists across relays
- [ ] 2.10 Write tests: round-trip JSONL write/read, in-memory cache reload, commit-then-truncate, message in-place update, agent status replay, pending message exemption from windowing

## 3. Executor Interface & Agent Implementations

- [ ] 3.1 Define `Executor` interface and `RunOptions`/`SessionResult` types (inspired by gry). `SessionResult` does NOT include `CommitHash` — that's determined by the runner.
- [ ] 3.2 Implement prompt builder: persona + task + inbox message + previous summary + recent session context sections. Include brief mention of `.rally/README.md` for agent self-service data access.
- [ ] 3.3 Port `ClaudeExecutor` from rally's runner (subprocess spawn, stdout capture, model flag)
- [ ] 3.4 Port `CodexExecutor` from gry (subprocess spawn, full-auto flags, structured output parsing)
- [ ] 3.5 Port `GeminiExecutor` from rally's runner (subprocess spawn, JSON output mode)
- [ ] 3.6 Port `OpenCodeExecutor` from rally's runner (subprocess spawn, stdout capture)
- [ ] 3.7 Port `FixtureExecutor` from gry (diff application, canned output, configurable delay)
- [ ] 3.8 Write tests: fixture executor round-trip, prompt builder with all field combinations

## 4. Agent Hooks / Structured Output

- [ ] 4.1 Define the structured report JSON schema (completed, summary, remaining_work, message_addressed, files_changed)
- [ ] 4.2 Implement reporting prompt template (concise prompt requesting JSON report matching schema)
- [ ] 4.3 Implement output parser with fallback for malformed JSON (return Completed: false)
- [ ] 4.4 Implement Claude block-and-report (primary): Register Stop hook. First stop event returns `{"decision": "block", "reason": "<reporting prompt>"}`. Second stop event parses `last_assistant_message`. Fallback: resume via `claude -c -p "<prompt>" --json-schema '<schema>' --output-format json`.
- [ ] 4.5 Implement Codex resume-and-report (primary): `codex exec resume --last "<prompt>" --output-schema ./schema.json -o ./report.json`. Register Stop hook as fallback (block-and-report). Hooks flagged experimental — resume preferred.
- [ ] 4.6 Implement OpenCode resume-and-report: `opencode run --continue "<prompt>" --format json`. Register `session.idle` plugin as trigger. No schema validation — prompt-guided.
- [ ] 4.7 Implement Gemini resume-and-report: `gemini --resume -p "<prompt>" --output-format json`. Extract report from `response` field of JSON wrapper (double-parse). Discard noisy stderr (MCP messages). No schema validation — prompt-guided. Register `SessionEnd` hook as trigger.
- [ ] 4.8 Write tests: JSON parsing (valid, malformed, missing optional fields), resume command construction per agent type, block-and-report hook flow

## 5. Relay Runner

- [ ] 5.1 Implement agent mix parsing (`ParseAgentMix`) and deterministic cycling (port from rally)
- [ ] 5.2 Implement relay lifecycle: create, resume, complete relay records via Store. Relay records track first/last relay_session_id and consumed_message_ids.
- [ ] 5.3 Implement session execution loop: write current_task.md (= the prompt), build prompt, invoke executor, track commit hash (compare HEAD before/after; use agent's commit if exists, auto-commit if uncommitted changes, record no hash if no changes), record result
- [ ] 5.4 Implement run-level retry logic: up to 3 session attempts per run, pass previous summary on retry. Failed sessions do NOT count against iteration target.
- [ ] 5.5 Implement failure detection: agent reports Completed: false, non-zero exit, or no-op session (no file changes + <3min runtime)
- [ ] 5.6 Implement error resilience cascade: pause agent type (1hr) after 3 consecutive session failures → hourly retry → freeze agent (after 5hr) → relay failure if all frozen. Wait if all paused. State persisted via agent_status.jsonl (persists across relays).
- [ ] 5.7 Implement graceful stop: atomic flag, complete current session then halt
- [ ] 5.8 Implement inbox message consumption: oldest pending message consumed per run (not per session), same message across retries, mark addressed based on SessionResult
- [ ] 5.9 Write tests: agent cycling determinism, retry within run, retry exhaustion triggers cascade, graceful stop, message consumption across retries, error resilience state transitions (pause/unfreeze/freeze), commit hash tracking (agent-committed, auto-committed, no changes)

## 6. Migration & Cleanup

- [ ] 6.1 Port rally.toml config loading to new structure (beads mode, agent models)
- [ ] 6.2 Port beads prompt mode from rally's prompt package (scout mode dropped)
- [ ] 6.3 Port self-update and release infrastructure (release.go, install.sh, goreleaser)
- [ ] 6.4 Remove old internal/rally/ package tree (runner, state, messages, progress, prompt, tui) — including existing Bubble Tea TUI code
- [ ] 6.5 Update AGENTS.md and README for v0.2.0
- [ ] 6.6 Bump VERSION to 0.2.0

## 7. Test Infrastructure

- [ ] 7.1 Port gry's testdata/ directory: fixture projects, diffs, and output JSON files
- [ ] 7.2 Port gry's test helpers: fixture seeding, copyFixtureProject (adapted for run-centric model, no task/phase/sprint DB)
- [ ] 7.3 Create e2e test: full relay workflow with fixture executor (session, record, verify store)
- [ ] 7.4 Create e2e test: relay with inbox message consumption — message consumed per run, same message across retries
- [ ] 7.5 Create e2e test: retry exhaustion triggers error cascade (not relay halt), agent paused then frozen
- [ ] 7.6 Create e2e test: graceful stop completes current session then halts
- [ ] 7.7 Create e2e test: commit hash tracking — verify agent-committed, auto-committed, and no-changes scenarios

