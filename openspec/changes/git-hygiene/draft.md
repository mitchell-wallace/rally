# Git Hygiene — Auto-commits and Agent Commits

> **Depends on `tidy-rally-runtime-data-storage` (#2).** After #2, all
> machine-churned JSONL lives in gitignored `.rally/state/`, the window
> truncate/archive **git commits are gone** (replaced by in-place local
> truncation), and `.rally/summary.jsonl` is the only churning tracked data
> file. This draft is rewritten to that world — several items from the original
> draft are now moot (see "Dropped").

## Auto-Commit on Init and Hook Install

**Problem**: `rally init` and laps-hook installation create/modify tracked files
(`.rally/config.toml`, `.rally/agents/`, `.rally/README.md`, `.rally/.gitignore`,
`.rally/summary.jsonl`, `.laps/hooks.json`, `.laps/laps.json`) but leave them
unstaged — easy to forget, and they clutter `git status` during runs.

**Change**: After init and hook install, stage and commit the new/modified
tracked files:

```
rally: initialize workspace
rally: install laps hooks
```

Use `--no-verify`; only commit if something is actually staged.

**Coordinate with #2**: #2 owns the new `.rally/.gitignore` template (`state/`),
removal of the stray `.laps/.gitignore`, and tracking `.laps/laps.json`. This
item must commit against that layout — it does not redefine the gitignore or the
file set, it just commits whatever #2 says is tracked.

## Agent Commit on Laps Done/Handoff

**Problem**: When `laps done` / `laps handoff` fire, Rally's wrapup prompt tells
the agent what to do next but not to commit first.

**Change**: The wrapup prompt instructs the agent to commit:
- **`laps done`**: `<lap-description>: done`
- **`laps handoff`**: `<lap-description>: in progress (handoff)`

Every lap boundary gets a clean commit point for review/revert/cherry-pick.
Independent of storage layout; unchanged from the original draft.

## State-Commit Hygiene (rewritten)

**Background**: Today three things commit to git — `CommitRallyState`
(`gitx/git.go`, stages `.rally/*.jsonl`, message `rally: update state`),
the window truncate/archive commits (`store/window.go`), and the real work
commit (`rally: run N attempt M`, `runner.go:1146`). The original draft's
auto-squash targeted streaks of `rally: update state` commits — but most of that
churn came from window commits and per-record-type JSONL writes.

**After #2** the window git-commits are removed and the only tracked data churn
is one appended line to `summary.jsonl` per finalized run/handoff. So the
streaks the auto-squash fixed largely cease to exist.

**Change (decided)**: Eliminate the separate state commit rather than squash it
— fold the `summary.jsonl` append into the run's work commit (`git add -A` at
`runner.go:1129` already stages it), so there is no standalone
`rally: update state` commit in the common path. Keep a minimal fallback: if a
state-only commit is ever emitted (e.g. a run that produced no code) and HEAD is
already `rally: update state` by the same author, amend instead of stacking
(cheap insurance, not the main mechanism).

**Coordinate with #2**: `CommitRallyState` currently globs `.rally/*.jsonl`;
after relocation that matches only `summary.jsonl`. Whoever implements the fold
should confirm `CommitRallyState`'s role (or retire it) rather than leave a
glob that silently does almost nothing.

## Dropped from the original draft

- **`.gitattributes` for `.rally/logs/`** — there is no `.rally/logs/` directory
  (verbose logs live in `dataDir`, `~/.local/share/rally/`; `.rally/state/` is
  gitignored). The premise was wrong; nothing log-like is tracked, so no
  diff-suppression is needed.
- **Elaborate auto-squash of consecutive `rally: update state` commits** —
  superseded by #2 removing window commits and by folding `summary.jsonl` into
  the work commit (above). Reduced to the minimal amend-fallback.

## Open questions

- Should `rally init`'s auto-commit be opt-out (some users may want to stage
  manually)? Default: always-on (original assumption); add a config toggle later
  only if requested.
