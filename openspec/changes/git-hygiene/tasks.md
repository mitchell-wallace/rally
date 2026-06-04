## 1. Auto-commit on init and hook install

- [x] 1.1 After `rally init` (`cmd/rally/main.go` `runInit`), stage the tracked setup files and commit `rally: initialize workspace` with `--no-verify`, only if something is staged
- [x] 1.2 After laps-hook install (`cmd/rally/main.go`, near the `laps.InstallHooks` call in `rally start`), stage `.laps/hooks.json` / `.laps/laps.json` (and any modified tracked files) and commit `rally: install laps hooks` with `--no-verify`, only if something is staged
- [x] 1.3 Stage against the file set / gitignore that `tidy-rally-runtime-data-storage` declares tracked — do not redefine the gitignore or file list here
- [x] 1.4 Tests: init in a clean repo produces exactly one setup commit; re-running init is a no-op (nothing staged → no commit); concurrent `rally start` instances do not produce duplicate commits

## 2. Agent commit at lap boundary

- [x] 2.1 Add a commit instruction to the `laps done` hook script (`internal/laps/laps-done-hook.sh`) with message `<lap-description>: done`
- [x] 2.2 Add a commit instruction to the `laps handoff` hook script (`internal/laps/laps-handoff-hook.sh`) with message `<lap-description>: in progress (handoff)`
- [x] 2.3 Tests/fixtures: assert each hook script's wrapup output contains the commit instruction with the correct message form
- [x] 2.4 Leftover-work detection: at run start in `internal/relay/runner.go`, check for uncommitted non-rally changes via `IsWorkspaceDirty` (excludes `.rally/`); when dirty, inject an advisory commit-first instruction into the initial prompt (code changes must be committed; docs/config-only may be left); when clean, omit it
- [x] 2.5 Tests: dirty tree outside `.rally/` → prompt contains leftover-work commit guidance; clean tree → prompt does not contain it; only `.rally/` files dirty → no guidance

## 3. Fold state into the work commit

- [ ] 3.1 Confirm `runner.autoCommit`'s `git add -A` stages the `summary.jsonl` append, so no standalone state commit is needed in the common path
- [ ] 3.2 Remove the `CommitRallyState` call from the common finalization path in `runner.go` (run-attempt loop)
- [ ] 3.3 Add the amend-fallback for no-code runs: at finalization, if HEAD's commit message has the `rally:` prefix, amend HEAD appending ` [+state]` to its message; otherwise create a single `rally: update state` commit. Also handle the no-changes case: skip both amend and new commit if nothing is staged
- [ ] 3.4 Remove `CommitRallyState` and `rallyTrackedStatePaths` from `internal/gitx/git.go` — the variable has no other callers and the amend-fallback handles the remaining case inline
- [ ] 3.5 Tests: a code-producing run yields one work commit containing the `summary.jsonl` line and no separate state commit; a no-code run with rally-authored HEAD amends; a no-code run with non-rally HEAD creates a single `rally: update state` commit

## 4. Docs, tests & coordination

- [ ] 4.1 Document the commit conventions (setup, lap-boundary, folded state, leftover-work guidance) in `AGENTS.md`/`README.md`
- [ ] 4.2 Confirm sequencing: this change lands after `tidy-rally-runtime-data-storage` so the gitignore/state layout it commits against exists. Verify the tracked-file set in the working tree matches the intended set — `.rally/.gitignore`, `.rally/README.md`, `.rally/config.toml`, `.rally/agents/` (`*.md`), and `.rally/summary.jsonl` (the only churning tracked data file) — with no missing, extra, or legacy state files. (`.rally/agent_status.jsonl`, a tracked-churn artifact from an older rally version, was untracked during batch prep; confirm it stays gitignored.)
- [x] 4.3 Tests: init in a dirty working tree (user has unstaged changes) does not accidentally commit them
- [x] 4.4 Tests: hook install re-run is idempotent (no duplicate commit)
- [ ] 4.5 Tests: commit messages with special characters (quotes, newlines) in lap descriptions are handled safely
- [ ] 4.6 Tests: `git` unavailable or repo-corrupted scenarios log a warning and skip commits gracefully
- [ ] 4.7 Bump `internal/buildinfo/VERSION` (patch bump per release process) as part of the change
