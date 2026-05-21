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

**Problem**: When an agent is sitting on retries, Rally creates a state commit
after each attempt. This leads to long runs of identical-looking commits:

```
rally: run 3 attempt 1 (claude)
rally: run 3 attempt 2 (claude)
rally: run 3 attempt 3 (claude)
rally: run 3 attempt 4 (claude)
```

These add noise to `git log`.

**Change**: Before creating a new auto-commit, check if the previous commit
message matches the `rally: run N attempt M` pattern for the same run. If so,
amend the previous commit (`git commit --amend`) instead of creating a new one.

This keeps exactly one commit per run (with the latest attempt number), instead
of one per attempt.

### Edge cases
- Only squash if HEAD commit author matches Rally's auto-commit identity
- Don't squash across different runs (run 2 → run 3)
- Don't squash if there are commits from other sources in between

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
