## Context

Three things write to git in a rally workspace today, and all three are untidy:

1. **Setup writes** — `rally init` and laps-hook install create/modify tracked files
   (`config.toml`, `agents/`, `README.md`, `.gitignore`, `summary.jsonl`,
   `.laps/hooks.json`, `.laps/laps.json`) but leave them unstaged.
2. **Agent work** — agents commit ad-hoc (or not), and the `laps done`/`laps handoff`
   wrapup prompt never tells them to commit, so lap boundaries are not reliable
   commit points.
3. **Rally state** — `CommitRallyState` (`gitx/git.go`, stages explicit paths from
   message `rally: update state`), plus the window truncate/archive commits
   (`store/window.go`), interleave with the real work commit
   (`rally: run N attempt M`, `runner.autoCommit`).

The original draft's auto-squash targeted streaks of `rally: update state` commits,
but most of that churn actually came from the window commits and per-record-type
JSONL writes — both of which `tidy-rally-runtime-data-storage` (#2) removes. After
#2, machine JSONL lives in gitignored `.rally/state/`, the window commits are gone,
and the only tracked data churn is one appended `summary.jsonl` line per finalized
run/handoff. This design is written to that post-#2 world.

## Goals / Non-Goals

**Goals:**
- Setup (init + hook install) lands as a single intentional commit, not unstaged drift.
- Every lap boundary is a clean, reviewable commit point.
- No standalone `rally: update state` commit in the common path; rally state rides
  the work commit.
- No regression in `git status` cleanliness during a run.

**Non-Goals:**
- Redefining the gitignore or the tracked-file set (owned by #2).
- Squashing or rewriting historical commits (the elaborate auto-squash is dropped).
- `.gitattributes` diff handling (no tracked log directory exists).
- Forcing agents into a particular commit granularity mid-lap — only the boundary
  commit is mandated.

## Decisions

**1. Auto-commit setup, only when something is staged.**
After `rally init` and after laps-hook install, stage the affected paths and commit
with `rally: initialize workspace` / `rally: install laps hooks`, using `--no-verify`
to avoid tripping repo hooks during tooling setup. Guard on a non-empty staged set so
re-running init in an already-clean repo is a no-op. This commits against whatever #2
declares tracked — it does not own the gitignore or the file list. Alternative
considered: leave setup unstaged and document it — rejected; it reliably clutters
`git status` and gets accidentally swept into unrelated commits.

**2. Lap-boundary commit via hook scripts.**
The `laps done` / `laps handoff` hook scripts (`internal/laps/laps-done-hook.sh`
and `internal/laps/laps-handoff-hook.sh`) gain a commit instruction in their wrapup
output: `<lap-description>: done` on done, `<lap-description>: in progress (handoff)`
on handoff. This rides the existing hook-script path rather than adding a rally-side
git call, keeping the agent's working tree and rally's view consistent (the agent
commits its own work in its own process).

**3. Leftover-work commit guidance at run start.**
When `internal/relay/runner.go` starts a run, it shall check for uncommitted changes
using `IsWorkspaceDirty` (which excludes rally-tracked files under `.rally/`). If the
working tree is dirty outside of rally's own files (leftovers from a previous agent
that did not finish its run), the initial prompt shall dynamically instruct the agent
to commit those changes first before beginning its assigned work. This guidance is
omitted when the tree is clean or only rally-tracked files are dirty.

**4. Fold state into the work commit; amend-fallback only.**
Eliminate the separate state commit rather than squash it. The run's work commit
(`runner.autoCommit`) already runs `git add -A` at finalization, which stages the
`summary.jsonl` append, so there is no standalone `rally: update state` commit in the
common path. Keep one cheap insurance path for no-code runs where only state changes
remain: check HEAD's commit message. If HEAD's message has the `rally:` prefix,
amend HEAD with the new state and append ` [+state]` to the commit message (e.g.
`rally: run 3 attempt 1 (claude) [+state]`). If HEAD is not a `rally:`-prefixed
commit, create a single `rally: update state` commit (no stacking of consecutive
state commits). This replaces the original elaborate auto-squash. Using the
`rally:` message prefix is simpler and more reliable than author-identity matching
(because `GitUserFallbackConfig` is a fallback — in repos with configured
`user.name`/`user.email`, rally-authored commits use the user's identity and would
not match the fallback). Alternative considered: keep `CommitRallyState` as-is and
squash streaks post-hoc — rejected; after #2 there are no streaks to squash, and
folding is simpler and leaves linear history.

**5. Retire `CommitRallyState`.**
After #2's relocation, `CommitRallyState`'s tracked-path set contains only
`summary.jsonl` among churning data files — which the fold (Decision 4) already
stages. The implementer SHALL remove `CommitRallyState` entirely — the amend-fallback
is a simpler conditional at
the single call site (`runner.go` in the run-attempt loop) and does not need a
separate function. Do not leave a glob that silently does almost nothing.

## Risks / Trade-offs

- **Setup auto-commit surprises a user who wanted to stage manually** → default
  always-on (preserves the original assumption); a config toggle can be added later
  only if requested. `--no-verify` + staged-set guard limit blast radius.
- **Agent ignores the commit instruction** → the boundary-commit is advisory (like all
  wrapup instructions); the work is still captured by rally's finalization
  `git add -A` in `autoCommit`, so worst case is a less granular history, not lost work.
  Downstream tooling that expects per-lap commits must tolerate gaps.
- **Leftover-work detection false positives** → a dirty tree at run start may be the
  user's own intentional changes, not agent leftovers. The prompt gives advisory
  guidance: code changes must be committed (upfront or folded into the end-of-run
  commit); docs/config-only changes are optional. Using `IsWorkspaceDirty` excludes
  rally's own tracked files from triggering the guidance.
- **Fold changes when `summary.jsonl` lands in history** → it now appears in the work
  commit instead of a dedicated state commit; documented, and acceptable since it is
  the same content under a clearer message.
- **`CommitRallyState` callers elsewhere** → audit call sites (`runner.go` in the
  run-attempt loop) when retiring so nothing depends on the old standalone commit.
