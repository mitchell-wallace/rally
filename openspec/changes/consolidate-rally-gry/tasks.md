## 1. Project Scaffolding

- [ ] 1.1 Add Cobra and bubbles dependencies to go.mod (no GORM/SQLite)
- [ ] 1.2 Create new package layout: `internal/agent/`, `internal/store/`, `internal/relay/`, `internal/tui/dashboard/`, `internal/tui/inbox/`, `internal/tui/runstatus/`
- [ ] 1.3 Set up `.rally/` directory structure with `.gitignore` (exclude `current_task.md`)
- [ ] 1.4 Migrate CLI from hand-rolled flags to Cobra root command with subcommands (`relay`, `init`, `instructions`, `update`, `version`)
- [ ] 1.5 Make default (no subcommand) launch the full-screen TUI

## 2. Store Layer

- [ ] 2.1 Define JSONL record types: `SessionRecord`, `MessageRecord`, `RelayRecord` with JSON tags
- [ ] 2.2 Implement JSONL append/read/truncate functions for a generic record type
- [ ] 2.3 Implement record windowing (truncate to ~100 records on write)
- [ ] 2.4 Implement in-memory cache: load all JSONL files into typed slices/maps on startup
- [ ] 2.5 Implement unified Store interface: write (JSONL append + cache update) and read (in-memory query)
- [ ] 2.6 Write tests: round-trip JSONL write/read, in-memory cache reload, windowing truncation

## 3. Executor Interface & Agent Implementations

- [ ] 3.1 Define `Executor` interface and `RunOptions`/`SessionResult` types (port from gry)
- [ ] 3.2 Implement prompt builder: persona + task + inbox message + previous summary sections
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
- [ ] 4.4 Implement Claude resume-and-report: `claude -c -p "<prompt>" --json-schema '<schema>' --output-format json`. Register Stop hook as trigger. Fallback: block-and-report via `decision: "block"`.
- [ ] 4.5 Implement Codex resume-and-report: `codex exec resume --last "<prompt>" --output-schema ./schema.json -o ./report.json`. Register Stop hook as trigger. Fallback: block-and-report.
- [ ] 4.6 Implement OpenCode resume-and-report: `opencode run --continue "<prompt>" --format json`. Register `session.idle` plugin as trigger. No schema validation — prompt-guided.
- [ ] 4.7 Implement Gemini resume-and-report: `gemini --resume -p "<prompt>" --output-format json`. Extract report from `response` field of JSON wrapper (double-parse). Discard noisy stderr (MCP messages). No schema validation — prompt-guided. Register `SessionEnd` hook as trigger.
- [ ] 4.8 Write tests: JSON parsing (valid, malformed, missing optional fields), resume command construction per agent type

## 5. Relay Runner

- [ ] 5.1 Implement agent mix parsing (`ParseAgentMix`) and deterministic cycling (port from rally)
- [ ] 5.2 Implement relay lifecycle: create, resume, complete relay records via Store
- [ ] 5.3 Implement session execution loop: write current_task.md, build prompt, invoke executor, record result, auto-commit
- [ ] 5.4 Implement run-level retry logic: up to 3 session attempts per run, pass previous summary on retry. Failed sessions do NOT count against iteration target.
- [ ] 5.5 Implement failure detection: agent reports Completed: false, non-zero exit, or no-op session (no file changes + <3min runtime)
- [ ] 5.6 Implement error resilience cascade: pause agent type (1hr) after 3 consecutive session failures → hourly retry → freeze agent (after 5hr) → relay failure if all frozen. Wait if all paused.
- [ ] 5.7 Implement graceful stop: atomic flag, complete current session then halt
- [ ] 5.8 Implement inbox message consumption: oldest pending message consumed per run (not per session), same message across retries, mark addressed based on SessionResult
- [ ] 5.9 Implement status callbacks (OnStatus) for TUI integration
- [ ] 5.10 Write tests: agent cycling determinism, retry within run, retry exhaustion triggers cascade, graceful stop, message consumption across retries, error resilience state transitions (pause/unfreeze/freeze)

## 6. TUI Dashboard

- [ ] 6.1 Implement root App model with Bubble Tea full-screen program and terminal resize handling
- [ ] 6.2 Implement bordered panel layout using bubbles (gitui-style boxes, responsive to terminal size)
- [ ] 6.3 Implement dashboard panel: relay status, progress bar, recent sessions with agent/runtime/git stats
- [ ] 6.4 Implement live session status panel: elapsed runtime counter, git lines +/-, files changed
- [ ] 6.5 Implement inbox panel: message list (pending above addressed), compose mode, reorder, mark addressed
- [ ] 6.6 Implement view navigation: keyboard shortcuts for dashboard/inbox switching, Escape to return
- [ ] 6.7 Implement relay control: start/stop keybindings, relay events channel from runner goroutine
- [ ] 6.8 Wire TUI to Store for reading relay/session/message data

## 7. Migration & Cleanup

- [ ] 7.1 Port rally.toml config loading to new structure (beads mode, agent models)
- [ ] 7.2 Port beads prompt mode and scout prompt mode from rally's prompt package
- [ ] 7.3 Port self-update and release infrastructure (release.go, install.sh, goreleaser)
- [ ] 7.4 Remove old internal/rally/ package tree (runner, state, messages, progress, prompt, tui)
- [ ] 7.5 Update AGENTS.md and README for v0.2.0
- [ ] 7.6 Bump VERSION to 0.2.0

## 8. Test Infrastructure

- [ ] 8.1 Port gry's testdata/ directory: fixture projects, diffs, and output JSON files
- [ ] 8.2 Port gry's test helpers: fixture seeding, copyFixtureProject (adapted for run-centric model, no task/phase/sprint DB)
- [ ] 8.3 Create e2e test: full relay workflow with fixture executor (session, record, verify store)
- [ ] 8.4 Create e2e test: relay with inbox message consumption — message consumed per run, same message across retries
- [ ] 8.5 Create e2e test: retry exhaustion triggers error cascade (not relay halt), agent paused then frozen
- [ ] 8.6 Create e2e test: graceful stop completes current session then halts
