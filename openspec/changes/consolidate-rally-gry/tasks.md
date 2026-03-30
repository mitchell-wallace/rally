## 1. Project Scaffolding

- [ ] 1.1 Add Cobra, GORM, SQLite driver, and bubbles dependencies to go.mod
- [ ] 1.2 Create new package layout: `internal/agent/`, `internal/store/`, `internal/relay/`, `internal/tui/dashboard/`, `internal/tui/inbox/`, `internal/tui/runstatus/`
- [ ] 1.3 Set up `.rally/` directory structure with `.gitignore` (exclude `current_task.md`, `*.db`)
- [ ] 1.4 Migrate CLI from hand-rolled flags to Cobra root command with subcommands (`relay`, `init`, `instructions`, `update`, `version`)
- [ ] 1.5 Make default (no subcommand) launch the full-screen TUI

## 2. Store Layer

- [ ] 2.1 Define JSONL record types: `RunRecord`, `MessageRecord`, `RelayRecord` with JSON tags
- [ ] 2.2 Implement JSONL append/read/truncate functions for a generic record type
- [ ] 2.3 Implement record windowing (truncate to ~100 records on write)
- [ ] 2.4 Define GORM models mirroring JSONL record types for SQLite cache
- [ ] 2.5 Implement SQLite cache rebuild from JSONL replay
- [ ] 2.6 Implement unified Store interface: write (JSONL append + cache update) and read (SQLite query)
- [ ] 2.7 Add startup logic: detect stale/missing cache, trigger rebuild
- [ ] 2.8 Write tests: round-trip JSONL write/read, cache rebuild, windowing truncation

## 3. Executor Interface & Agent Implementations

- [ ] 3.1 Define `Executor` interface and `RunOptions`/`RunResult` types (port from gry)
- [ ] 3.2 Implement prompt builder: persona + task + inbox message + previous summary sections
- [ ] 3.3 Port `ClaudeExecutor` from rally's runner (subprocess spawn, stdout capture, model flag)
- [ ] 3.4 Port `CodexExecutor` from gry (subprocess spawn, full-auto flags, structured output parsing)
- [ ] 3.5 Port `GeminiExecutor` from rally's runner (subprocess spawn, JSON output mode)
- [ ] 3.6 Port `OpenCodeExecutor` from rally's runner (subprocess spawn, stdout capture)
- [ ] 3.7 Port `FixtureExecutor` from gry (diff application, canned output, configurable delay)
- [ ] 3.8 Write tests: fixture executor round-trip, prompt builder with all field combinations

## 4. Agent Hooks

- [ ] 4.1 Define the structured report JSON schema (completed, summary, remaining_work, message_addressed, files_changed)
- [ ] 4.2 Implement hook output parser with fallback for malformed JSON (return Completed: false)
- [ ] 4.3 Implement Claude stop hook registration in ClaudeExecutor
- [ ] 4.4 Implement Gemini AfterAgent hook registration in GeminiExecutor (or JSON output mode fallback)
- [ ] 4.5 Implement Codex hook registration in CodexExecutor
- [ ] 4.6 Implement OpenCode hook registration in OpenCodeExecutor
- [ ] 4.7 Write tests: JSON parsing (valid, malformed, missing optional fields), hook prompt generation

## 5. Relay Runner

- [ ] 5.1 Implement agent mix parsing (`ParseAgentMix`) and deterministic cycling (port from rally)
- [ ] 5.2 Implement relay lifecycle: create, resume, complete relay records via Store
- [ ] 5.3 Implement run execution loop: write current_task.md, build prompt, invoke executor, record result, auto-commit
- [ ] 5.4 Implement retry logic: up to 3 attempts per run, pass previous summary on retry
- [ ] 5.5 Implement error resilience cascade: pause (1hr) → freeze (5hr) → relay failure, per agent type
- [ ] 5.6 Implement graceful stop: atomic flag, complete current run then halt
- [ ] 5.7 Implement inbox message consumption: oldest pending message consumed per run, mark addressed based on RunResult
- [ ] 5.8 Implement status callbacks (OnStatus) for TUI integration
- [ ] 5.9 Write tests: agent cycling determinism, retry exhaustion, graceful stop, message consumption, error resilience state transitions

## 6. TUI Dashboard

- [ ] 6.1 Implement root App model with Bubble Tea full-screen program and terminal resize handling
- [ ] 6.2 Implement bordered panel layout using bubbles (gitui-style boxes, responsive to terminal size)
- [ ] 6.3 Implement dashboard panel: relay status, progress bar, recent runs with agent/runtime/git stats
- [ ] 6.4 Implement live run status panel: elapsed runtime counter, git lines +/-, files changed
- [ ] 6.5 Implement inbox panel: message list (pending above addressed), compose mode, reorder, mark addressed
- [ ] 6.6 Implement view navigation: keyboard shortcuts for dashboard/inbox switching, Escape to return
- [ ] 6.7 Implement relay control: start/stop keybindings, relay events channel from runner goroutine
- [ ] 6.8 Wire TUI to Store for reading relay/run/message data

## 7. Migration & Cleanup

- [ ] 7.1 Port rally.toml config loading to new structure (beads mode, agent models)
- [ ] 7.2 Port beads prompt mode and scout prompt mode from rally's prompt package
- [ ] 7.3 Port self-update and release infrastructure (release.go, install.sh, goreleaser)
- [ ] 7.4 Remove old internal/rally/ package tree (runner, state, messages, progress, prompt, tui)
- [ ] 7.5 Update AGENTS.md and README for v0.2.0
- [ ] 7.6 Bump VERSION to 0.2.0

## 8. Test Infrastructure

- [ ] 8.1 Port gry's testdata/ directory: fixture projects, diffs, and output JSON files
- [ ] 8.2 Port gry's test helpers: SetupTestDB, fixture seeding, copyFixtureProject
- [ ] 8.3 Create e2e test: full relay workflow with fixture executor (run, record, verify store)
- [ ] 8.4 Create e2e test: relay with inbox message consumption and addressed tracking
- [ ] 8.5 Create e2e test: retry exhaustion halts relay after 3 attempts
- [ ] 8.6 Create e2e test: graceful stop completes current run then halts
