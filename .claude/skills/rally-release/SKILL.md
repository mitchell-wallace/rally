---
name: rally-release
description: Lightweight ship-it workflow for Rally — land code on main and (if VERSION was bumped) publish a new auto-tagged release. Use proactively whenever the user wants to push a release, merge to main, ship, cut a version, or do anything that results in a new rally binary being published. Also use when the user is on dev and asks to push to main. Starts from either a feature branch (full flow: branch → dev → main) or directly from dev (simplified flow: dev → main). Stops on non-trivial merge conflicts, local test/lint failures, CI failures, or when main is already at the current branch tip. Do not use for OpenSpec changes (use openspec-* skills), preparing laps (use prepare-laps), or post-relay forensics (use post-relay-review).
license: MIT
metadata:
  author: rally
  version: "0.2"
---

# Rally Release

Ship the current feature branch to main, run CI, (if `internal/buildinfo/VERSION` was bumped) publish a new auto-tagged release, and smoke-test the published binary.

This is a lightweight, ship-fast workflow. It is intentionally low-ceremony and reflects Rally's current "ship fast" stage. Tighten it as the release process matures — keep the same shape and just upgrade the steps in place.

## When to use

Use this skill proactively whenever the user's intent is to land code on `main` and/or publish a release. Typical triggers:

- "push to main", "merge to main", "ship it", "cut a release", "publish a new version"
- "bump version and release", "do the standard land-and-release flow"
- The user is on `dev` with accumulated commits and wants them on `main`
- The user is on a feature branch ready to land

Starting points:

- **Feature branch** (full flow): branch → dev → main. Requires clean working tree on a non-`main`, non-`dev` branch.
- **`dev` branch** (simplified flow): dev → main. Skips the branch-to-dev merge and branch push. Requires clean working tree on `dev`.

Do **not** use this skill for:

- OpenSpec-driven changes — use `openspec-*` skills.
- Preparing or advancing a laps queue — use `prepare-laps`.
- Investigating past Rally runs — use `post-relay-review`.

## Standard flow

The order matters: CI is configured to run on push to `main` only. Pushing to `dev` does **not** trigger CI. The `auto-tag` and `release` workflows fire as a result of the main push.

### Starting from a feature branch

1. **Sanity check** — `git status --short --branch` and `git branch -vv`. Must be on a non-`main`, non-`dev` branch. Working tree must be clean.
2. **Local checks** — `just test` and `just check` (which runs `go vet` + a `gofmt` check). Both must be clean. (Fallback: `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .`.)
3. **Push the branch** — `git push origin <branch>`.
4. **Merge to dev** — `git checkout dev && git merge --ff-only <branch> && git push origin dev`.
5. **Merge to main** — `git checkout main && git merge --ff-only dev && git push origin main`. (This push is what triggers CI.)
6. Continue to **Watch CI** below.

### Starting from dev

1. **Sanity check** — `git status --short --branch` and `git branch -vv`. Must be on `dev`. Working tree must be clean.
2. **Local checks** — `just test` and `just check`. Both must be clean. (Fallback: `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .`.)
3. **Merge to main** — `git checkout main && git merge --ff-only dev && git push origin main`. (This push is what triggers CI.)
4. Continue to **Watch CI** below.

### Watch CI

7. **Watch CI** — `gh run list --branch main --limit 1 --json databaseId,name,status,conclusion` to find the test, auto-tag, and release run IDs, then `gh run watch <id> --exit-status` for each in order (test → auto-tag → release).
8. **Install latest binary and smoke test** — after CI passes and the release is published:
   - Run `rally update` locally to fetch the latest published binary.
   - Create a throwaway git repo in `/tmp/rally-smoke-<tag>/` with a trivial prompt (e.g. "Create a file called smoke-test.txt").
   - Run a single-iteration relay with a cheap/free model (e.g. `opencode/minimax-m2.5-free` or `zai-coding-plan/glm-5.1`).
   - Verify: exit 0, file created, try record in `.rally/state/tries.jsonl` shows `"completed": true`.
   - This confirms the published binary actually works end-to-end before declaring the release done.
8. **Report** — final SHAs for `main`/`dev`/`branch>`, CI outcomes, the new release tag (if auto-tag fired), and smoke-test result.

## Stop conditions

Halt and report (do **not** attempt to recover, rebase, or force-push) when any of these hit:

- **Working tree dirty at start.** Stash, commit, or split before resuming.
- **`just test` or `just check` fails locally.** Fix on the feature branch, push, and restart from step 2.
- **Non-trivial merge conflict** during the dev or main merge. "Non-trivial" means real code/line conflicts. A failed fast-forward is also a stop — it means the branch is not strictly ahead of the target, and forcing it would rewrite history.
- **`main` is already at the current branch tip** (or the dev/main fast-forwards would be no-ops). Nothing to ship. Report and exit.
- **CI test workflow fails.** Read the job log with `gh run view <id> --log`, fix on the feature branch, push, and restart from step 2.
- **CI auto-tag or release workflow fails.** Same: read the job log, fix on the feature branch, push, and restart from step 2. Successful test is required before auto-tag fires; successful auto-tag is required before release fires.
- **Smoke test fails.** After `rally update`, if the local relay fails (agent unavailable is OK — report it; but a rally crash or missing file is a real regression), investigate. Do not declare the release done until the smoke test passes or the failure is understood to be an agent-side issue (rate limit, auth) rather than a rally regression.

## VERSION and the release build

`internal/buildinfo/VERSION` controls the auto-tag. The skill does **not** bump VERSION — that is a separate, user-driven decision (default: patch bump per the project's releasing convention in `AGENTS.md`).

If VERSION was bumped on the feature branch, the main push will fire `auto-tag` (creating `vX.Y.Z`) and then `release` (publishing the GitHub release). If VERSION was **not** bumped, the auto-tag step detects "no change" and exits 0 — the merge still lands, but no new release is published. That outcome is fine; the skill still succeeded at landing code.

## Conventions

- Use plain `git push` (no `--force`, no `--no-verify`) for all pushes.
- Use `git merge --ff-only` for the dev and main merges. If a fast-forward is not possible, that is a stop condition.
- Use `gh run watch <id> --exit-status` for CI; do not poll manually.
- Report SHAs (`git rev-parse origin/main origin/dev <branch>`) and CI run IDs at the end so the outcome is auditable.
- Do not delete or rewrite the feature branch or any commits during a successful run. Leave history intact.
- For the smoke test, prefer free-tier models (`opencode/minimax-m2.5-free`, `zai-coding-plan/glm-5.1`) to avoid cost. Use `--iterations 1` for speed. Clean up `/tmp/rally-smoke-*` after confirmation.

## Evolution

This skill is intentionally short. As Rally's release process matures, expand it in place — add sections, do not split into files — and bump the metadata `version` field. Likely future additions include: PR-based reviews, branch protection checks, sign-off requirements, release branches, hotfix flows, post-release announcements. Encode them here when they land.
