## 1. Codebase cleanup

- [x] 1.1 Search the entire codebase for `beads_rust`, `BeadsRust`, `beads-rust` and remove every reference (code, tests, docs, prompt fragments)
- [x] 1.2 Search for the current `beads` auto-detection logic in `internal/config/config_v2.go` and remove it; verify no remaining `if backend == "beads"` style branches
- [x] 1.3 Rename the `Beads` string field on `V2Config` to `LapsInstructions` string (at the struct root). Update the default config block in `cmd/rally/main.go`'s `initCmd` to match.
- [x] 1.4 Update CLI help text and any prompt-template fragments that mention multiple bead variants
- [x] 1.5 Ensure that `grep -riw "beads" --include="*.go" --include="*.md" --include="*.toml"` returns zero hits outside archived openspec changes

## 2. Mode detection

- [x] 2.1 Add `internal/laps/detect.go` with a `Detect(workspaceDir string) bool` function that checks for `.laps/laps.json` discoverable from cwd AND `laps` binary available on PATH
- [x] 2.2 Wire detection into rally startup in `cmd/rally/main.go` (set `runnerCfg.LapsEnabled bool`, replacing the old `BeadsEnabled` bool in `relay.Config`)
- [x] 2.3 Unit tests: workspace with `.laps/laps.json` and `laps` on PATH returns `true`; workspace with `.laps/laps.json` but no `laps` returns `false`; workspace with bare `.laps/` and `laps` available returns `false`; workspace with no `.laps/` and no `laps` returns `false`; workspace where `laps` is available but no `.laps/laps.json` returns `false`

## 3. Laps adapter

- [x] 3.1 Add `internal/laps/adapter.go` with a `HeadPull(ctx context.Context) (Lap, error)` method that shells out to `laps get head` and parses command output
- [x] 3.2 Define a `Lap` struct with fields `ID`, `Title`, `Description`, and `Assignee` (the latter optional and not yet used)
- [x] 3.3 Handle the "no head task" case (where `laps` exits non-zero with that literal message) by returning a no-lap sentinel without crashing
- [x] 3.4 Also handle the case where `laps` returns a non-0 exit code by returning the no-lap sentinel
- [x] 3.5 Source real `laps` binary from `lib/laps/` (github.com/mitchell-wallace/laps repo). Expand the `testdata/` folder to include a `fixture-laps` folder for integration testing
- [x] 3.6 Add tests against real `laps` binary: test scenarios where head is present and where queue is empty

## 4. Hook scripts

- [x] 4.1 Create `internal/scripts/laps/laps-done-hook.sh`: invokes `rally progress --record-lap "$id"`, then prints the `laps wrapup` reminder to stdout (passback)
- [x] 4.2 Create `internal/scripts/laps/laps-handoff-hook.sh`: sets RALLY_HANDOFF_STATE=1 in `.rally/run-state.json` and prints handoff-tuned instructions directing agent to call `laps wrapup --summary "..." --followup "..."`
- [x] 4.3 Create `internal/scripts/laps/laps-wrapup-hook.sh`: checks RALLY_HANDOFF_STATE in `.rally/run-state.json`; if `0` or missing, forwards `$@` to `rally progress --complete`; if `1`, resets to `0`, then forwards `$@` to `rally progress --handoff`. Prints `Progress recorded.` on success, propagates non-zero on error.
- [x] 4.4 Ensure each script forwards `$@` to rally rather than parsing flags itself
- [x] 4.5 Write tests spawning each script with representative args, verifying the rally subcommand call shape and stdout contents

## 5. Hook installer

- [x] 5.1 Add `internal/laps/hooks.go` with `InstallHooks(lapsDir string) error` that idempotently maintains rally-keyed entries in `.laps/hooks.json`
- [x] 5.2 Use the `rally:` prefix on all hook `name` fields rally owns; never modify entries with non-`rally:` names
- [x] 5.3 Embed the hook script bodies using `//go:embed` so `rally` is built as a single binary
- [x] 5.4 Write the three hook scripts to `.laps/hooks/rally/` and reference them from `.laps/hooks.json` by relative path
- [x] 5.5 Add unit tests: first install adds three entries; re-install is a no-op; install alongside user-edited entries preserves the user entries

## 6. Internal `rally progress` subcommand

- [x] 6.1 Add `internal/progress/cli.go` with the `rally progress` cobra subcommand. Register it in `cmd/rally/main.go`'s `init()`
- [x] 6.2 Implement `--record-lap <id>` (repeatable flag) — store in `.rally/run-state.json` and consume in next progress log write
- [x] 6.3 Implement `--complete --summary <s> --followup <f>` — flushes accumulated `record-lap` IDs, writes a finalised entry to `.rally/progress.yaml`, clears the run state
- [x] 6.4 Implement `--handoff --summary <s> --followup <f>` — creates a lap per `--followup` via `laps add head` (using first 30 chars of followup with ellipsis as title, full text as description), flushes accumulated `record-lap` IDs, writes a handoff entry to `.rally/progress.yaml` with `created_lap_ids`, clears the run state
- [x] 6.5 Add unit tests for each flag combo

## 7. Progress log writer

- [x] 7.1 Add `internal/progress/store.go` with read/write functions for `.rally/progress.yaml`
- [x] 7.2 Implement the `laps_completed` field rule: present-with-IDs, present-as-`"none"`, or omitted — gated by `LapsEnabled`
- [x] 7.3 Implement the `handoff` field: written with summary, followups, and created lap IDs when `--handoff` is the finalisation path
- [x] 7.4 Add unit tests: fresh write, laps_completed all three states, handoff present/absent

## 8. Stub entries on incomplete runs

- [x] 8.1 In the relay loop, capture the agent's final console-printed output line(s) per run
- [x] 8.2 At run-end, if `.rally/run-state.json` shows the run was not finalised (no `--complete` or `--handoff` call this run), write a stub entry whose `summary` is the first 160 characters of that captured output
- [x] 8.3 Stub entries still include `laps_completed` accumulated by the `laps done` hook
- [x] 8.4 Integration test: run a fake agent that exits without `laps wrapup`, verify a stub entry is written with the truncated final message

## 9. Run-state file

- [x] 9.1 Define `.rally/run-state.json` schema: `{ run_id, handoff_state, recorded_laps }`
- [x] 9.2 Add to `.rally/.gitignore` (this file is ephemeral)
- [x] 9.3 Cleared by the relay loop at the start of each run (after stub-entry decision is made for the previous run)
- [x] 9.4 Unit tests: handoff state set/cleared, recorded_laps accumulation, file resets between runs

## 10. Prompt template

- [x] 10.1 Remove the `Header Context` block from the relay prompt template
- [x] 10.2 Add a mode-aware section: laps-enabled includes `laps done <id>` and `laps handoff` exit-condition instructions; no-backend includes `rally progress --summary --followup` instructions
- [x] 10.3 Verify no executor (claude/codex/gemini/opencode) re-injects the Header Context
- [x] 10.4 Test: build the prompt in each mode, snapshot-compare against expected output

## 11. Verification

- [x] 11.1 End-to-end: laps-enabled, agent calls `laps done` then `laps wrapup` — verify progress entry has `summary`, `followups`, `laps_completed: [id]`
- [x] 11.2 End-to-end: laps-enabled, agent calls `laps handoff` then `laps wrapup` — verify progress entry has `handoff` field, queue-head lap(s) created, original lap still open
- [x] 11.3 End-to-end: laps-enabled, agent exits without finalising — verify stub entry with 160-char summary and accumulated `laps_completed`
- [x] 11.4 End-to-end: no-backend mode, agent calls `rally progress --complete --summary --followup` — verify entry written, no `laps_completed` field
- [x] 11.5 Confirm `grep beads_rust` returns zero hits outside archived openspec changes
