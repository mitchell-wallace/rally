## 1. Project Scaffolding

- [ ] 1.1 Add Cobra dependency to go.mod (no GORM/SQLite, no bubbles)
- [ ] 1.2 Create new package layout: `internal/agent/`, `internal/store/`, `internal/relay/`
- [ ] 1.3 Set up `.rally/` directory structure with `.gitignore` (exclude `current_task.md`, `relays/`)
- [ ] 1.4 Migrate CLI from hand-rolled flags to Cobra root command with subcommands (`relay`, `init`, `instructions`, `update`, `version`)
- [ ] 1.5 Make `rally relay` the primary command for starting/resuming relays (CLI-only, no TUI)
- [ ] 1.6 Simplify `rally init` to programmatic-only: git init, create `.rally/` directory, create `.rally/.gitignore`, create `.rally/config.toml`, create `.rally/README.md`. No agent invocation.
- [ ] 1.7 Create `.rally/README.md` template with instructions for agents on accessing rally data (e.g. `tail -10 tries.jsonl` for recent context)
- [ ] 1.8 Consolidate `rally.toml` + `.rally/config` into `.rally/config.toml` (TOML format, all config in one file: agent models, beads mode, runtime paths, `run_hooks_on_autocommit`)

## 2. Store Layer

- [ ] 2.1 Define JSONL record types: `TryRecord`, `MessageRecord`, `RelayRecord`, `AgentStatusEvent` with JSON tags
- [ ] 2.2 Implement JSONL append/read/rewrite functions for a generic record type
- [ ] 2.3 Implement per-type record windowing: 200 tries, 50 relays, 50 agent status events. Messages windowed only when resolved/cancelled (pending messages exempt).
- [ ] 2.4 Implement commit-then-truncate: when window exceeded, commit current file to git, truncate to window size, commit again
- [ ] 2.5 Implement in-memory cache: load all JSONL files into typed slices/maps on startup
- [ ] 2.6 Implement unified Store interface: write (JSONL append/rewrite + cache update) and read (in-memory query)
- [ ] 2.7 Implement message model: JSON objects updated in place (not event-sourced), with position-based FIFO ordering
- [ ] 2.8 Implement relay record with fields: id, target_iterations, completed_iterations, agent_mix, started_at, ended_at, first_try_id, last_try_id, consumed_message_ids
- [ ] 2.9 Implement agent status store: dedicated `agent_status.jsonl` with pause/freeze/unfreeze/active events, persists across relays
- [ ] 2.10 Write tests: round-trip JSONL write/read, in-memory cache reload, commit-then-truncate, message in-place update, agent status replay, pending message exemption from windowing

## 3. Executor Interface & Agent Implementations

- [ ] 3.1 Define `Executor` interface and `RunOptions`/`TryResult` types (inspired by gry). `TryResult` does NOT include `CommitHash` — that's determined by the runner.
- [ ] 3.2 Implement prompt builder: persona + task + inbox message + previous summary + recent try context sections. Include brief mention of `.rally/README.md` for agent self-service data access.
- [ ] 3.3 Implement `ClaudeExecutor`: subprocess spawn, `--output-format stream-json --verbose`, parse NDJSON stream for `result` events, model flag
- [ ] 3.4 Implement `CodexExecutor`: subprocess spawn, full-auto flags, `--output-schema` + `-o` for structured output parsing
- [ ] 3.5 Implement `GeminiExecutor`: subprocess spawn, `--output-format json`, extract and re-parse `response` field from JSON wrapper, discard noisy stderr
- [ ] 3.6 Implement `OpenCodeExecutor`: subprocess spawn, `--format json`, parse NDJSON stream for `text` events
- [ ] 3.7 Implement `FixtureExecutor` from gry (diff application, canned output, configurable delay)
- [ ] 3.8 Implement shared git helpers: `gitRepoRoot`, `gitOutput`, `gitCommandError`, `gitUserFallbackConfig` — used by relay runner, not per-executor
- [ ] 3.9 Write tests: fixture executor round-trip, prompt builder with all field combinations, output parsing per agent format (valid, malformed, missing fields)

## 4. Relay Runner

- [ ] 4.1 Implement agent mix parsing (`ParseAgentMix`) and deterministic cycling (port from rally)
- [ ] 4.2 Implement relay lifecycle: create, resume, complete relay records via Store. Relay records track first/last try ID and consumed_message_ids.
- [ ] 4.3 Implement try execution loop: write current_task.md (= the prompt), build prompt, invoke executor, track commit hash (compare HEAD before/after; use agent's commit if exists, auto-commit with `--no-verify` if uncommitted changes, record no hash if no changes), record result
- [ ] 4.4 Implement run-level retry logic: up to 3 try attempts per run, pass previous summary on retry. Failed tries do NOT count against iteration target.
- [ ] 4.5 Implement failure detection: agent reports Completed: false, non-zero exit, or no-op try (no file changes + <3min runtime)
- [ ] 4.6 Implement error resilience cascade: pause agent type (1hr) after 3 consecutive try failures → hourly retry → freeze agent (after 5hr) → relay failure if all frozen. Wait if all paused. State persisted via agent_status.jsonl (persists across relays).
- [ ] 4.7 Implement graceful stop: atomic flag, complete current try then halt
- [ ] 4.8 Implement inbox message consumption: oldest pending message consumed per run (not per try), same message across retries, mark addressed based on TryResult
- [ ] 4.9 Implement relay logging: dual-write to `~/.local/share/rally/relays/relay-N.log` (durable) and `.rally/relays/relay-N.log` (repo cache, 10-file limit). Prune old repo cache logs.
- [ ] 4.10 Write tests: agent cycling determinism, retry within run, retry exhaustion triggers cascade, graceful stop, message consumption across retries, error resilience state transitions (pause/unfreeze/freeze), commit hash tracking (agent-committed, auto-committed, no changes)

## 5. Migration & Cleanup

- [ ] 5.1 Migrate config: port `rally.toml` + `.rally/config` loading to `.rally/config.toml` (TOML, single file)
- [ ] 5.2 Port beads prompt mode from rally's prompt package (scout mode dropped)
- [ ] 5.3 Port self-update and release infrastructure (release.go with injectable URL for testing, install.sh, goreleaser). Existing release tests are portable.
- [ ] 5.4 Ensure update check errors are concise (not full stack traces)
- [ ] 5.5 Remove old internal/rally/ package tree (runner, state, messages, progress, prompt, tui) — including existing Bubble Tea TUI code
- [ ] 5.6 Update AGENTS.md and README for v0.2.0
- [ ] 5.7 Bump VERSION to 0.2.0

## 6. Test Infrastructure

- [ ] 6.1 Port gry's testdata/ directory: fixture projects, diffs, and output JSON files
- [ ] 6.2 Port gry's test helpers: fixture seeding, copyFixtureProject (adapted for run-centric model, no task/phase/sprint DB)
- [ ] 6.3 Create e2e test: full relay workflow with fixture executor (try, record, verify store)
- [ ] 6.4 Create e2e test: relay with inbox message consumption — message consumed per run, same message across retries
- [ ] 6.5 Create e2e test: retry exhaustion triggers error cascade (not relay halt), agent paused then frozen
- [ ] 6.6 Create e2e test: graceful stop completes current try then halts
- [ ] 6.7 Create e2e test: commit hash tracking — verify agent-committed, auto-committed, and no-changes scenarios
