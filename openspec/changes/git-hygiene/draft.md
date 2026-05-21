# Git Hygiene — Auto-commits, Agent Commits, and Squashing

## Auto-Commit on Init and Hook Install

**Problem**: When Rally initializes (`rally init`) or installs laps hooks, it
creates/modifies files like `runs.json`, `.laps/hooks.json`, `.rally/` config
files, etc. These are left unstaged — easy to forget to commit, and they
pollute `git status` during runs.

**Change**: After `rally init` and hook installation, automatically stage and
commit the new/modified files:

```
rally: initialize workspace
rally: install laps hooks
```

Use `--no-verify` to skip pre-commit hooks (same pattern as auto-commit after
runs). Only commit if there are actual changes staged.

---

## Agent Commit on Laps Done/Handoff

**Problem**: When `laps done` or `laps handoff` hooks fire, Rally's output
instructs the agent (runner) on what to do next via `laps wrapup`. But it
doesn't tell the agent to commit their work first.

**Change**: When the hook fires, Rally's wrapup prompt should instruct the
agent to commit:

- **On `laps done`**: "Commit your completed work with message:
  `<lap-description>: done`"
- **On `laps handoff`**: "Commit your in-progress work with message:
  `<lap-description>: in progress (handoff)`"

This ensures every lap boundary has a clean commit point, making it easy to
review, revert, or cherry-pick individual laps.

---

## Auto-Squash Consecutive Rally State Commits

**Problem**: Between runs (and during retries/skips), Rally creates
`rally: update state` commits for bookkeeping (runs.json, progress, etc.).
These pile up and dominate the git log. Real example from `Prayer-app`:

```
e073c89 rally: update state
d37d086 rally: run 7 attempt 1 (claude)
b09c1cb rally: update state
fdf9d22 rally: update state
86e3fff rally: update state      ← 5 consecutive state commits
87d6bc6 rally: update state         between run 5 and run 7
664e93d rally: update state
2f2259e rally: run 5 attempt 1 (codex)
```

The `rally: run N attempt M` commits carry actual code changes and should be
kept. The `rally: update state` commits are pure Rally bookkeeping noise.

**Change**: Before creating a new state commit, check if HEAD is already a
`rally: update state` commit. If so, amend it (`git commit --amend`) instead
of creating a new one. This collapses any streak of consecutive state updates
into a single commit.

The `rally: run N attempt M` commits are never squashed — they represent
real agent work.

### Logic
```
if HEAD commit message == "rally: update state":
    git commit --amend --no-verify -m "rally: update state"
else:
    git commit --no-verify -m "rally: update state"
```

### Edge cases
- Only amend if HEAD commit message is exactly `rally: update state`
- Don't amend across different commit authors (someone else committed in between)
- `rally: run N attempt M` commits are never candidates for squashing

---

## .gitattributes for Log Files

**Problem**: Rally try logs (`.rally/logs/`) are large binary-ish text files
that create noisy diffs in commits and PRs.

**Change**: Add/ensure `.gitattributes` entries to suppress diffs for log
files:

```gitattributes
.rally/logs/** -diff
.rally/logs/** linguist-generated
```

This should be added during `rally init` (and included in the auto-commit from
the first item above).

### Open question
Should log files be `.gitignore`d entirely instead? Arguments:
- **Keep in git**: Provides audit trail, enables `rally tail` on past runs
- **Ignore**: They're large, change every run, and aren't meaningful code

Leaning toward keeping them in git but suppressing diffs. Could add a config
option later if people want to ignore them.
