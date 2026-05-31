---
name: post-relay-review
description: Investigate the aftermath of a Rally relay or laps run, especially when git history, branch scope, generated Rally state, or agent logs may be inconsistent. Use for post-relay forensics, recovery planning, branch-target-aware diff review, lap/try audit, and process lessons after Rally-orchestrated agent work.
license: MIT
metadata:
  author: rally
  version: "1.0"
---

# Post Relay Review

Use this after a Rally relay when the repo state, git history, or agent outcomes need auditing before recovery, PR, or archival.

## Core Rules

- Do not rewrite history during the review. No `reset`, `rebase`, squash, amend-away, or force-push unless the user explicitly approves a recovery plan.
- Preserve Rally state where it was generated. Do not delete local `.rally/` or `.laps/` artifacts during review.
- Treat `.laps/` as the structured planning system, not runtime noise. It is normally git-tracked.
- Treat stable Rally configuration as source: `.rally/config.toml` and `.rally/agents/` are normally git-tracked.
- Treat high-churn Rally runtime/debug files separately: `state/tries.jsonl`, `state/relays.jsonl`, `summary.jsonl`, `state/agent_status.jsonl`, `state/hook-audit.jsonl`, relay logs, and harness logs may need pruning or export instead of indefinite git history.
- Observability tools such as Sentry are appropriate for preserving historical debug logs and harness/runtime errors, especially for container-based runs. They are not the operational source of truth for current lap scheduling; current planning/config still lives in `.laps/` and stable `.rally/` files.
- Identify the intended target branch before diffing. Do not assume `main`; use repo docs, PR metadata, branch names, `git branch -vv`, `git merge-base`, or user input.
- Treat work before the first lap/try in the current batch as baseline context. It may be out of the current request scope, but it is not automatically disposable.
- Report findings before repairs. If recovery is needed, propose options with tradeoffs and make a new branch unless the user asks to repair in place.

## Artifact Checklist

Read these first when present:

- `.laps/laps.json` — task queue, assignees, completed state, follow-up laps. This is planning state and is usually tracked.
- `.rally/config.toml` and `.rally/agents/` — routing and role instructions. These are usually tracked.
- `.rally/summary.jsonl` — run summaries and lap completions (tracked).
- `.rally/state/relays.jsonl` — relay batches and first/last try ids (untracked).
- `.rally/state/tries.jsonl` — one attempt per line, including harness, assignee, summary, changed files, commit history, and log path (untracked).

Use `jq` or small Python helpers for JSONL; runtime files can be verbose and may be pruned or exported after review.

## Git Audit

1. Capture current state: `git status --short --branch`, `git branch -vv`, recent `git log --graph --decorate --all`.
2. Find base/target: inspect branch tracking, PR metadata, repo references, and `git merge-base <target> HEAD`.
3. Compare:
   - Current branch vs target.
   - Current branch vs rescue tags/reflog if history was changed.
   - First relay/lap commit vs current state, so pre-batch work is not misclassified as drift.
4. Prefer additive recovery:
   - create a recovery branch;
   - cherry-pick/apply patches from known-good refs;
   - use revert commits for unwanted changes;
   - keep rescue tags and reflog references.

## Harness Logs

`tries.jsonl` usually lists `log_path`. In Dune-created agent sandboxes, logs are often inside the container under paths like:

- `/home/agent/.local/share/rally/tries/<repo-id>/try-<n>.log`
- `/persist/agent/.claude/projects/.../*.jsonl`
- `/home/agent/.gemini/tmp/.../*.jsonl`
- `/home/agent/.cache/claude-cli-nodejs/.../*.jsonl`

Use `docker exec <container> ...` when the container still exists. For large logs, use Python or `jq` to extract commands, assistant summaries, tool calls, and errors rather than reading the whole file.

## Terminology

- **Relay**: a Rally execution batch, recorded in `state/relays.jsonl`.
- **Try**: one agent/harness attempt, recorded in `state/tries.jsonl`.
- **Lap**: one unit of queued work in `.laps/laps.json`.
- **VERIFY lap**: a review/checking lap. It may make tiny safe fixes, but substantive repair should become a new lap or explicit recovery plan.
- **Dune**: the CLI commonly used to create agent-ready containers/sandboxes for Rally work.

## Repo References

Load a repo-specific reference if one matches the current repository:

- `references/prayer-app-2.md` for Prayer-app-2.
