## 1. Codebase cleanup

- [ ] 1.1 Search the entire codebase for `beads_rust`, `BeadsRust`, `beads-rust` and remove every reference (code, tests, docs, prompt fragments)
- [ ] 1.2 Search for `beads` (Go backend) auto-detection logic and remove; verify no remaining `if backend == "beads"` style branches
- [ ] 1.3 Rename the `Beads string` field on `V2Config` to a clearly-microbeads-scoped name (e.g. `MicrobeadsInstructions string` at root, or move into a `[microbeads]` table)
- [ ] 1.4 Update CLI help text and any prompt-template fragments that mention multiple bead variants
- [ ] 1.5 Run `grep -r "beads_rust\|backend.*beads\b" --include="*.go" --include="*.md" --include="*.toml"` and confirm zero hits outside archived openspec changes

## 2. Mode detection

- [ ] 2.1 Add `internal/beads/microbeads/detect.go` with a `DetectMode(workspaceDir string) Mode` function that walks up from cwd looking for `.beads/mb.json`
- [ ] 2.2 Define `Mode` enum (`MicrobeadsBacked`, `NoBackend`)
- [ ] 2.3 Wire mode detection into rally startup (cache the result for the duration of the rally invocation)
- [ ] 2.4 Unit tests: workspace with `.beads/mb.json` returns `MicrobeadsBacked`; workspace with bare `.beads/` returns `NoBackend`; workspace with neither returns `NoBackend`; workspace where `.beads/mb.json` is in an ancestor returns `MicrobeadsBacked`

## 3. Microbeads adapter

- [ ] 3.1 Add `internal/beads/microbeads/adapter.go` with a `HeadPull(ctx) (Bead, error)` method that shells out to `mb get head` and parses JSON
- [ ] 3.2 Define `Bead` struct with fields `ID`, `Title`, `Description`, `Assignee` (the latter optional)
- [ ] 3.3 Handle the "no head task" case (mb exits non-zero with that literal message) by returning a no-bead sentinel without erroring
- [ ] 3.4 Unit tests with a fake `mb` script under `testdata/`: head present, queue empty, malformed JSON

## 4. Hook installer

- [ ] 4.1 Add `internal/beads/microbeads/hooks.go` with `InstallHooks(beadsDir string) error` that idempotently maintains rally-keyed entries in `.beads/mb-hooks.json`
- [ ] 4.2 Use the `rally:` prefix on all hook `name` fields rally owns; never modify entries with non-`rally:` names
- [ ] 4.3 Embed the hook script bodies via `go:embed` so rally is a single binary
- [ ] 4.4 Write the three hook scripts to a stable path (e.g. `~/.local/share/rally/hooks/`) and reference them from `mb-hooks.json` by absolute path
- [ ] 4.5 Unit tests: first install adds three entries; re-install is a no-op; install alongside user-edited entries preserves the user entries

## 5. Hook scripts

- [ ] 5.1 Write `mb-done-hook.sh`: invokes `rally progress --record-bead "$id"`, then prints the `mb wrapup` reminder to stdout (passback)
- [ ] 5.2 Write `mb-wrapup-hook.sh`: forwards `$@` to `rally progress --finalise`; prints `Progress recorded.` on success, propagates non-zero on error
- [ ] 5.3 Write `mb-handoff-hook.sh`: detects first call (no/empty `--reason`) vs second call; first call sets the flag and prints handoff-tuned instructions; second call invokes `mb add head` per `--followup`, then `rally progress --handoff`, then clears the flag, then prints the confirmation
- [ ] 5.4 Each script forwards `$@` to rally rather than parsing flags itself
- [ ] 5.5 Integration tests: spawn each script with representative args, verify the rally subcommand call shape and stdout contents

## 6. Internal `rally progress` subcommand

- [ ] 6.1 Add `internal/progress/cli.go` with the `rally progress` cobra subcommand
- [ ] 6.2 Implement `--record-bead <id>` (repeatable) — accumulates into `.rally/run-state.json` keyed by current `RUN_ID` env var
- [ ] 6.3 Implement `--finalise --summary <s> --followup <f>...` — flushes accumulated `record-bead` IDs, writes a finalised entry to `.rally/progress.yaml`, clears the run state
- [ ] 6.4 Implement `--handoff --reason <r> --followup <f>...` — writes a handoff entry, clears the handoff flag and run state
- [ ] 6.5 In microbeads-backed mode, hide the subcommand from public CLI help; in no-backend mode, expose `rally progress --summary --followup` as the public form
- [ ] 6.6 Unit tests for each flag combo

## 7. Progress log writer

- [ ] 7.1 Add `internal/progress/store.go` with read/write functions for `.rally/progress.yaml`
- [ ] 7.2 On first read post-upgrade, copy `docs/orchestration/rally-progress.yaml` to `.rally/progress.yaml` if the destination doesn't exist; leave the legacy file untouched
- [ ] 7.3 On first write post-upgrade, rewrite `recent_sessions` → `recent_runs` and `session_id` → `run_id`
- [ ] 7.4 Implement entry append, history-window pruning (existing semantics), and atomic write
- [ ] 7.5 Implement the `beads_completed` field rule: present-with-IDs, present-as-`"none"`, or omitted — gated by mode
- [ ] 7.6 Implement the `handoff` field: written when `--handoff` is the finalisation path
- [ ] 7.7 Unit tests: fresh write, copy-from-legacy, key renames, beads_completed all three states, handoff present/absent

## 8. Stub entries on incomplete runs

- [ ] 8.1 In the relay loop, capture the agent's final console-printed output line(s) per run
- [ ] 8.2 At run-end, if `.rally/run-state.json` shows the run was not finalised (no `--finalise` or `--handoff` call this run), write a stub entry whose `summary` is the first 160 characters of that captured output
- [ ] 8.3 Stub entries still include `beads_completed` accumulated by the `mb done` hook
- [ ] 8.4 Integration test: run a fake agent that exits without `mb wrapup`, verify a stub entry is written with the truncated final message

## 9. Run-state file

- [ ] 9.1 Define `.rally/run-state.json` schema: `{ run_id, handoff_flag, recorded_beads }`
- [ ] 9.2 Add to `.rally/.gitignore` (this file is ephemeral)
- [ ] 9.3 Cleared by the relay loop at the start of each run (after stub-entry decision is made for the previous run)
- [ ] 9.4 Unit tests: flag set/cleared, recorded_beads accumulation, file resets between runs

## 10. Prompt template

- [ ] 10.1 Remove the `Header Context` block from the relay prompt template
- [ ] 10.2 Add a mode-aware section: microbeads-backed mode includes `mb done <id>` and `mb handoff` exit-condition instructions; no-backend mode includes `rally progress --summary --followup` instructions
- [ ] 10.3 Verify no executor (claude/codex/gemini/opencode) re-injects the Header Context
- [ ] 10.4 Test: build the prompt in each mode, snapshot-compare against expected output

## 11. Documentation and migration

- [ ] 11.1 Update README and CLI help to reflect microbeads-only support
- [ ] 11.2 Document the file move (`docs/orchestration/rally-progress.yaml` → `.rally/progress.yaml`) in the v0.4.0 release notes
- [ ] 11.3 Document the `Beads string` config field rename in release notes; note that auto-detection is gone
- [ ] 11.4 Add a brief operator-facing section explaining `mb done` / `mb wrapup` / `mb handoff` flow in microbeads-backed mode

## 12. Verification

- [ ] 12.1 End-to-end: microbeads-backed mode, agent calls `mb done` then `mb wrapup` — verify progress entry has `summary`, `followups`, `beads_completed: [id]`
- [ ] 12.2 End-to-end: microbeads-backed mode, agent calls `mb handoff` twice — verify handoff entry, queue-head bead created, original bead still open
- [ ] 12.3 End-to-end: microbeads-backed mode, agent exits without finalising — verify stub entry with 160-char summary and accumulated `beads_completed`
- [ ] 12.4 End-to-end: no-backend mode, agent calls `rally progress --summary --followup` — verify entry written, no `beads_completed` field
- [ ] 12.5 Confirm `grep beads_rust` returns zero hits outside archived openspec changes
