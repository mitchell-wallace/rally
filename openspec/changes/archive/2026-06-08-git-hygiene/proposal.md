## Why

Rally writes to a repo from two directions — its own setup/state and the agents'
work — but leaves git in an untidy state on both. `rally init` and laps-hook
installation create or modify tracked files (`config.toml`, `agents/`,
`README.md`, `.gitignore`, `summary.jsonl`, `.laps/hooks.json`, `.laps/laps.json`)
yet leave them unstaged, so they clutter `git status` during a run and are easy to
forget. At the other end, when an agent calls `laps done` / `laps handoff` the
wrapup prompt tells it what to record but not to commit, so lap boundaries are not
reliable commit points for review, revert, or cherry-pick. And rally itself emits
a separate `rally: update state` commit (plus, before
`tidy-rally-runtime-data-storage`, a stream of window truncate/archive commits)
that interleaves with real work commits and bloats history.

This change makes rally's git footprint intentional: setup is committed once, every
lap boundary is a clean commit, and rally's own state no longer earns standalone
commits.

**Depends on `tidy-rally-runtime-data-storage` (#2).** After #2, all
machine-churned JSONL lives in gitignored `.rally/state/`, the window
truncate/archive git commits are gone (replaced by in-place local truncation), and
`.rally/summary.jsonl` is the only churning tracked data file. This change is scoped
to that world; several items from the original draft (a `.gitattributes` rule for a
non-existent `.rally/logs/`, an elaborate auto-squash of `rally: update state`
streaks) are dropped as moot.

## What Changes

- **Auto-commit on init and hook install.** After `rally init` and laps-hook
  installation, stage and commit the new/modified tracked files with
  `rally: initialize workspace` / `rally: install laps hooks`, using `--no-verify`
  and committing only when something is actually staged. This commits against the
  file set #2 declares tracked; it does not redefine the gitignore or that set.
- **Agent commit at every lap boundary.** The `laps done` / `laps handoff` hook
  scripts (`internal/laps/laps-done-hook.sh` and `internal/laps/laps-handoff-hook.sh`)
  instruct the agent to commit its work with `<lap-description>: done` on done or
  `<lap-description>: in progress (handoff)` on handoff. Every lap boundary becomes
  a reviewable/revertable commit point.
- **Leftover-work commit guidance at run start.** When a run begins and the working
  tree has uncommitted changes (likely leftovers from a previous agent that did not
  finish a run), the initial prompt dynamically instructs the agent to commit those
  changes first before beginning its assigned work. This instruction is omitted when
  the tree is clean.
- **Fold state into the work commit (no standalone state commit).** Eliminate the
  separate `rally: update state` commit in the common path by folding the
  `summary.jsonl` append into the run's work commit (the `autoCommit` function's
  `git add -A` at finalization already stages it). Keep a minimal amend-fallback
  for no-code runs: if HEAD's commit message has the `rally:` prefix, amend HEAD
  with `[+state]` appended to the message; otherwise create a single
  `rally: update state` commit (no stacking).

## Capabilities

### Added Capabilities
- `git-hygiene`: rally's own git write behavior — setup auto-commit, lap-boundary
  agent commits, and folding rally state into the work commit instead of separate
  state commits.

## Impact

- **Code**: `cmd/rally/main.go` (post-init commit at `runInit`, post-hook-install
  commit after `laps.InstallHooks`), `internal/laps/laps-done-hook.sh` and
  `internal/laps/laps-handoff-hook.sh` (wrapup commit instruction),
  `internal/relay/runner.go` (leftover-work detection at run start,
  `autoCommit` at finalization, `CommitRallyState` call-site removal),
  and `internal/gitx/git.go` (`CommitRallyState` retirement with amend-fallback).
- **Behavior**: cleaner `git status` during runs; one commit per lap boundary; no
  standalone `rally: update state` commits in the common path.
- **Coordination with `tidy-rally-runtime-data-storage` (#2)**: #2 owns the
  `.rally/.gitignore` template (`state/`), removal of the stray `.laps/.gitignore`,
  and tracking `.laps/laps.json`. After #2's relocation, `CommitRallyState`'s
  tracked-path set contains only `summary.jsonl` among churning data files — the implementer SHALL retire
  `CommitRallyState` entirely (its only remaining purpose, the standalone
  `rally: update state` commit, is superseded by the fold + amend-fallback).
- **Out of scope**: `.gitattributes` diff-suppression (there is no tracked log
  directory; verbose logs live in `dataDir`), and the elaborate auto-squash of
  consecutive state commits (superseded by #2 + the fold).
