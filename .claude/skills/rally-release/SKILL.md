---
name: rally-release
description: Lightweight ship-it workflow for Rally — push the current feature branch to dev and main, run CI, and (if VERSION was bumped) publish a new auto-tagged release. Use when the user asks to ship, release, merge to main, or run the standard land-and-release flow. Stops on non-trivial merge conflicts, local test/lint failures, CI failures, or when main is already at the current branch tip. Do not use for OpenSpec changes (use openspec-* skills), preparing laps (use prepare-laps), or post-relay forensics (use post-relay-review).
license: MIT
metadata:
  author: rally
  version: "0.1"
---

# Rally Release

Ship the current feature branch to main, run CI, and (if `internal/buildinfo/VERSION` was bumped) publish a new auto-tagged release.

This is a lightweight, ship-fast workflow. It is intentionally low-ceremony and reflects Rally's current "ship fast" stage. Tighten it as the release process matures — keep the same shape and just upgrade the steps in place.

## When to use

- You are on a feature branch whose tip is not on `main`.
- The user has asked to ship, release, merge to main, or "do the standard land-and-release flow".
- `main` is **not** already at the current branch tip (the "main already up-to-date" stop condition will trip otherwise).

Do **not** use this skill for:

- OpenSpec-driven changes — use `openspec-*` skills.
- Preparing or advancing a laps queue — use `prepare-laps`.
- Investigating past Rally runs — use `post-relay-review`.

## Standard flow

The order matters: CI is configured to run on push to `main` only. Pushing to `dev` does **not** trigger CI. The `auto-tag` and `release` workflows fire as a result of the main push.

1. **Sanity check** — `git status --short --branch` and `git branch -vv`. Must be on a non-`main`, non-`dev` branch. Working tree must be clean.
2. **Local checks** — `just test` and `just check` (which runs `go vet` + a `gofmt` check). Both must be clean. (Fallback: `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .`.)
3. **Push the branch** — `git push origin <branch>`.
4. **Merge to dev** — `git checkout dev && git merge --ff-only <branch> && git push origin dev`.
5. **Merge to main** — `git checkout main && git merge --ff-only dev && git push origin main`. (This push is what triggers CI.)
6. **Watch CI** — `gh run list --branch main --limit 1 --json databaseId,name,status,conclusion` to find the test, auto-tag, and release run IDs, then `gh run watch <id> --exit-status` for each in order (test → auto-tag → release).
7. **Report** — final SHAs for `main`/`dev`/`<branch>`, CI outcomes, and the new release tag (if auto-tag fired).

## Stop conditions

Halt and report (do **not** attempt to recover, rebase, or force-push) when any of these hit:

- **Working tree dirty at start.** Stash, commit, or split before resuming.
- **`just test` or `just check` fails locally.** Fix on the feature branch, push, and restart from step 2.
- **Non-trivial merge conflict** during the dev or main merge. "Non-trivial" means real code/line conflicts. A failed fast-forward is also a stop — it means the branch is not strictly ahead of the target, and forcing it would rewrite history.
- **`main` is already at the current branch tip** (or the dev/main fast-forwards would be no-ops). Nothing to ship. Report and exit.
- **CI test workflow fails.** Read the job log with `gh run view <id> --log`, fix on the feature branch, push, and restart from step 2.
- **CI auto-tag or release workflow fails.** Same: read the job log, fix on the feature branch, push, and restart from step 2. Successful test is required before auto-tag fires; successful auto-tag is required before release fires.

## VERSION and the release build

`internal/buildinfo/VERSION` controls the auto-tag. The skill does **not** bump VERSION — that is a separate, user-driven decision (default: patch bump per the project's releasing convention in `AGENTS.md`).

If VERSION was bumped on the feature branch, the main push will fire `auto-tag` (creating `vX.Y.Z`) and then `release` (publishing the GitHub release). If VERSION was **not** bumped, the auto-tag step detects "no change" and exits 0 — the merge still lands, but no new release is published. That outcome is fine; the skill still succeeded at landing code.

## Conventions

- Use plain `git push` (no `--force`, no `--no-verify`) for all pushes.
- Use `git merge --ff-only` for the dev and main merges. If a fast-forward is not possible, that is a stop condition.
- Use `gh run watch <id> --exit-status` for CI; do not poll manually.
- Report SHAs (`git rev-parse origin/main origin/dev <branch>`) and CI run IDs at the end so the outcome is auditable.
- Do not delete or rewrite the feature branch or any commits during a successful run. Leave history intact.

## Evolution

This skill is intentionally short. As Rally's release process matures, expand it in place — add sections, do not split into files — and bump the metadata `version` field. Likely future additions include: PR-based reviews, branch protection checks, sign-off requirements, release branches, hotfix flows, post-release announcements. Encode them here when they land.
