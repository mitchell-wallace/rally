## 1. Auto-commit on init and hook install

- [ ] 1.1 After `rally init` (`cmd/rally/main.go` `runInit`), stage the tracked setup files and commit `rally: initialize workspace` with `--no-verify`, only if something is staged
- [ ] 1.2 After laps-hook install (`internal/cli/hooks.go`), stage `.laps/hooks.json` / `.laps/laps.json` (and any modified tracked files) and commit `rally: install laps hooks` with `--no-verify`, only if something is staged
- [ ] 1.3 Stage against the file set / gitignore that `tidy-rally-runtime-data-storage` declares tracked — do not redefine the gitignore or file list here
- [ ] 1.4 Tests: init in a clean repo produces exactly one setup commit; re-running init is a no-op (nothing staged → no commit)

## 2. Agent commit at lap boundary

- [ ] 2.1 Add a commit instruction to the `laps done` / `laps handoff` wrapup prompt (`internal/relay/` prompt construction)
- [ ] 2.2 Use `<lap-description>: done` for done and `<lap-description>: in progress (handoff)` for handoff
- [ ] 2.3 Tests/fixtures: assert the wrapup prompt contains the commit instruction with the correct message form for each branch

## 3. Fold state into the work commit

- [ ] 3.1 Confirm the run's work commit `git add -A` (`runner.go:1129`) stages the `summary.jsonl` append, so no standalone state commit is needed in the common path
- [ ] 3.2 Remove the standalone `rally: update state` commit from the common finalization path (`runner.go:919` call site)
- [ ] 3.3 Add the amend-fallback: if a state-only commit is emitted (run produced no code) and HEAD is already a rally state commit by the same author, amend instead of stacking
- [ ] 3.4 Retire or repurpose `CommitRallyState` (`internal/gitx/git.go`) — its `.rally/*.jsonl` glob matches only `summary.jsonl` after #2; do not leave a near-no-op glob
- [ ] 3.5 Tests: a code-producing run yields one work commit containing the `summary.jsonl` line and no separate state commit; a no-code run amends rather than stacks state commits

## 4. Docs & coordination

- [ ] 4.1 Document the commit conventions (setup, lap-boundary, folded state) in `AGENTS.md`/`README.md`
- [ ] 4.2 Confirm sequencing: this change lands after `tidy-rally-runtime-data-storage` so the gitignore/state layout it commits against exists
- [ ] 4.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
