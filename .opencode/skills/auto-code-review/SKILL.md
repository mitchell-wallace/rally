---
name: auto-code-review
description: Review implementation diffs, feature branches, or completed Rally relays with a subagent-backed, findings-first workflow. Use when the user asks for code review, auto-review, review-and-fix, post-implementation review, or review code from a completed relay/branch.
---

# Auto Code Review

Review code changes like a senior engineer: find real defects first, fix low-risk issues directly when the user asks for auto-fix, and surface product or architecture calls instead of silently deciding them.

## Core Rules

- Start from the codebase and git state, not assumptions.
- Identify the intended review scope and base before diffing. Use branch tracking, PR metadata, OpenSpec artifacts, laps/relay commits, `git merge-base`, or explicit user instructions.
- Preserve unrelated worktree changes. Do not revert, reset, rebase, squash, amend, or force-push unless explicitly requested.
- Findings come first. Prioritize bugs, regressions, data loss, security, concurrency, lifecycle, migration, and missing-test risks over style.
- Auto-fix only low-risk, local issues: missing regression tests, stale assertions, obvious nil/error handling, incorrect imports, simple behavior-preserving corrections, or documentation/spec drift.
- Do not silently resolve product semantics, architecture boundaries, persistence format, public API surface, migration policy, user-facing naming, or scope splits. Report the call with a default recommendation.
- Commit along the way only when the user asks for commits. Keep commits focused by review phase or fix category.

## Workflow

### 1. Scope The Review

- Capture `git status --short --branch`, branch tracking, and recent log.
- Determine the base and target. For Rally relay work, identify the first relay/lap commit and the completed commit range.
- Read relevant plans: OpenSpec `proposal.md`/`design.md`/`tasks.md`/`specs/**/spec.md`, `.laps/laps.json`, PR notes, or user-provided requirements.
- Get the changed-file list and stats for the scoped diff. Exclude later unrelated commits unless they are needed to understand current state.
- If another local repo has relevant review/merge skills or docs, skim them as reference only; do not assume they apply directly.

### 2. First Pass With Subagents

Use subagents for non-trivial reviews. Run them in parallel when possible, read-only, with explicit scope and output format.

Useful reviewer lanes:

- Architecture/import/API boundaries: package direction, public surface, layering, persistence contracts.
- Behavior preservation: control flow, retries, cancellation, sync/laps/git/telemetry semantics, CLI output.
- Tests and verification: dropped/duplicated tests, coverage gaps, brittle fixtures, missing regression checks.
- Domain-specific risk: auth, migrations, data safety, concurrency, external APIs, release/CI.

Subagent output format:

```text
Findings
- [severity] file:line - Concrete bug/risk and why it matters.
  Recommendation: exact fix or product/architecture decision needed.

What's Solid
- Brief bullets.

Product/Architecture Calls
- Questions with tradeoff and default recommendation, or `None`.
```

Severity labels:

- `Critical`: likely data loss, security issue, broken build, wrong architecture direction, or major behavioral regression.
- `Major`: likely implementation bug, lifecycle regression, stale assumption, or significant missing test.
- `Minor`: low-risk correctness, maintainability, test placement, or clarity issue.

### 3. Triage Findings

- Verify each finding against primary source code before editing.
- Reject false positives explicitly in your notes.
- Auto-fix clear low-risk issues when requested.
- For product/architecture calls, stop before changing that behavior unless the user already gave a decision.
- If a finding is valid but out of scope, route it to a follow-up instead of expanding the patch silently.

### 4. Apply Fixes

- Make the smallest correct change.
- Keep fixes focused and behavior-preserving unless the user approved a behavior change.
- Add or move regression tests with the code they exercise.
- Run focused tests after each fix cluster.
- Commit each coherent fix cluster when the user asked to commit along the way.

### 5. Second Pass

- Run a fresh read-only subagent after fixes for substantial reviews.
- Tell it which first-pass findings were fixed, rejected, or intentionally left as product/architecture calls.
- Ask it to report only new issues, remaining concrete defects, or issues introduced by the fixes.
- Apply any remaining low-risk fixes and validate again.

### 6. Final Validation

- Run relevant focused tests and broader checks appropriate to the change.
- Run `git diff --check`.
- Inspect `git status --short` and ensure only intended files remain changed.
- If OpenSpec artifacts are in scope, run `openspec validate <change> --strict`.
- If release behavior or build tags are in scope, include a build check.

### 7. Report

Report in this order:

1. Product/architecture calls, with tradeoff and default recommendation.
2. Findings fixed, grouped by category.
3. Commits created.
4. Validation results.
5. Residual risks or testing gaps.

If no findings remain, say that explicitly and mention any unresolved calls.
