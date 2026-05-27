## Context

Three things write to git in a rally workspace today, and all three are untidy:

1. **Setup writes** — `rally init` and laps-hook install create/modify tracked files
   (`config.toml`, `agents/`, `README.md`, `.gitignore`, `summary.jsonl`,
   `.laps/hooks.json`, `.laps/laps.json`) but leave them unstaged.
2. **Agent work** — agents commit ad-hoc (or not), and the `laps done`/`laps handoff`
   wrapup prompt never tells them to commit, so lap boundaries are not reliable
   commit points.
3. **Rally state** — `CommitRallyState` (`gitx/git.go`, stages `.rally/*.jsonl`,
   message `rally: update state`), plus the window truncate/archive commits
   (`store/window.go`), interleave with the real work commit
   (`rally: run N attempt M`, `runner.go:1146`).

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

**2. Lap-boundary commit via the wrapup prompt.**
The `laps done` / `laps handoff` wrapup prompt gains a commit instruction:
`<lap-description>: done` on done, `<lap-description>: in progress (handoff)` on
handoff. This rides the existing prompt-injection path rather than adding a rally-side
git call, keeping the agent's working tree and rally's view consistent (the agent
commits its own work in its own process). Storage-independent; unchanged from the
original draft.

**3. Fold state into the work commit; amend-fallback only.**
Eliminate the separate state commit rather than squash it. The run's work commit
already runs `git add -A` at finalization (`runner.go:1129`), which stages the
`summary.jsonl` append, so there is no standalone `rally: update state` commit in the
common path. Keep one cheap insurance path: if a state-only commit is ever emitted
(e.g. a run that produced no code) and HEAD is already a rally state commit by the
same author, amend instead of stacking. This replaces the original elaborate
auto-squash. Alternative considered: keep `CommitRallyState` as-is and squash streaks
post-hoc — rejected; after #2 there are no streaks to squash, and folding is simpler
and leaves linear history.

**4. Retire or repurpose `CommitRallyState`.**
After #2's relocation, `CommitRallyState`'s `.rally/*.jsonl` glob matches only
`summary.jsonl`, which the fold (Decision 3) already stages. The implementer SHALL
either remove `CommitRallyState` or repurpose it explicitly (e.g. as the
amend-fallback helper) rather than leave a glob that silently does almost nothing.

## Risks / Trade-offs

- **Setup auto-commit surprises a user who wanted to stage manually** → default
  always-on (preserves the original assumption); a config toggle can be added later
  only if requested. `--no-verify` + staged-set guard limit blast radius.
- **Agent ignores the commit instruction** → the boundary-commit relies on agent
  compliance like the rest of the wrapup flow; the work is still captured by rally's
  finalization `git add -A`, so worst case is a less granular history, not lost work.
- **Fold changes when `summary.jsonl` lands in history** → it now appears in the work
  commit instead of a dedicated state commit; documented, and acceptable since it is
  the same content under a clearer message.
- **`CommitRallyState` callers elsewhere** → audit call sites (`runner.go:919`) when
  retiring/repurposing so nothing depends on the old standalone commit.
