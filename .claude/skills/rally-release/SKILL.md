---
name: rally-release
description: >-
  Lightweight ship-it workflow for Rally — land code on main and (if VERSION was
  bumped) publish a new auto-tagged release. Use proactively whenever the user
  wants to push a release, merge to main, ship, cut a version, or do anything
  that results in a new rally binary being published. Also use when the user is
  on dev or staging and asks to push to main. Walks the branch pipeline
  feature → dev → staging → main, starting from whichever stage the user is on
  (dev → staging additionally requires a test-driving-rally pass).
  Stops on non-trivial merge conflicts, local test/lint failures, CI failures,
  a missing test-drive pass, or when main is already at the current branch tip. Do not use for OpenSpec
  changes (use openspec-* skills), preparing laps (use prepare-laps), or
  post-relay forensics (use post-relay-review).
license: MIT
metadata:
  author: rally
  version: "0.4"
---

# Rally Release

Ship code down the branch pipeline **feature → dev → staging → main**, run CI at each stage, (if `internal/buildinfo/VERSION` was bumped) publish a new auto-tagged release, and smoke-test the published binary.

Pipeline stages (see "Branch pipeline" in AGENTS.md):

- **feature → dev** — local gates + dev CI green.
- **dev → staging** — additionally requires a `test-driving-rally` pass that
  cleared the exact dev SHA being promoted. If no pass is on record for that
  SHA, run the test-driving-rally skill first (or stop and report if the user
  has not scoped that in); the user may explicitly waive the test drive.
- **staging → main** — ff-only with staging CI green on the exact SHA. The
  main push fires auto-tag/release.

This is a lightweight, ship-fast workflow. It is intentionally low-ceremony and reflects Rally's current "ship fast" stage. Tighten it as the release process matures — keep the same shape and just upgrade the steps in place.

## When to use

Use this skill proactively whenever the user's intent is to land code on `main` and/or publish a release. Typical triggers:

- "push to main", "merge to main", "ship it", "cut a release", "publish a new version"
- "bump version and release", "do the standard land-and-release flow"
- The user is on `dev` with accumulated commits and wants them on `main`
- The user is on a feature branch ready to land

Starting points:

- **Feature branch** (full flow): branch → dev → staging → main. Requires clean working tree on a non-pipeline branch.
- **`dev` branch**: dev → staging → main. Skips the branch-to-dev merge and branch push. Requires clean working tree on `dev`.
- **`staging` branch**: staging → main only. Use when dev was already promoted and cleared; requires clean working tree on `staging`.

Do **not** use this skill for:

- OpenSpec-driven changes — use `openspec-*` skills.
- Preparing or advancing a laps queue — use `prepare-laps`.
- Investigating past Rally runs — use `post-relay-review`.

## Standard flow

The order matters: the test workflow runs on `dev`, `staging`, and `main` pushes. Before advancing a stage, verify the exact source SHA has green required checks (`test`, `race`, `lint`, `tidy`) — and, for dev → staging, a test-driving-rally pass. The `auto-tag` and `release` workflows fire as a result of the main push.

### Starting from a feature branch

1. **Sanity check** — `git status --short --branch` and `git branch -vv`. Must be on a non-pipeline (not `main`/`staging`/`dev`) branch. Working tree must be clean.
2. **Local checks** — run `just test`, `just check`, `just test-race`, `just tidy-check`, and `just audit`. All must be clean before pushing. `just test` runs the deterministic suite — the CI `test` job minus the opt-in real-backend tests; to reproduce the *full* CI `test` command (including real-agent coverage) run `just test-real` (`RALLY_TEST_REAL_AGENTS=1 go test -count=1 ./...`), but it needs agent CLIs + auth and is slow/flaky, so it is NOT part of this local gate — CI is authoritative for real-agent coverage. (Fallback: `go test -count=1 ./...`, `go vet ./...` plus `gofmt -l .`, `go test -race -shuffle=on -count=1 ./...`, `go mod tidy && git diff --exit-code go.mod go.sum`, and `govulncheck ./...` after installing `golang.org/x/vuln/cmd/govulncheck@latest`.)
3. **Push the branch** — `git push origin <branch>`.
4. **Merge to dev** — `git checkout dev && git merge --ff-only <branch> && git push origin dev`.
5. Continue from step 3 of **Starting from dev**.

### Starting from dev

1. **Sanity check** — `git status --short --branch` and `git branch -vv`. Must be on `dev`. Working tree must be clean.
2. **Local checks** — run `just test`, `just check`, `just test-race`, `just tidy-check`, and `just audit`. All must be clean before pushing. `just test` runs the deterministic suite — the CI `test` job minus the opt-in real-backend tests; to reproduce the *full* CI `test` command (including real-agent coverage) run `just test-real` (`RALLY_TEST_REAL_AGENTS=1 go test -count=1 ./...`), but it needs agent CLIs + auth and is slow/flaky, so it is NOT part of this local gate — CI is authoritative for real-agent coverage. (Fallback: `go test -count=1 ./...`, `go vet ./...` plus `gofmt -l .`, `go test -race -shuffle=on -count=1 ./...`, `go mod tidy && git diff --exit-code go.mod go.sum`, and `govulncheck ./...` after installing `golang.org/x/vuln/cmd/govulncheck@latest`.)
3. **Verify dev CI** — wait for the `test.yml` workflow run on the exact `dev` SHA and confirm required jobs `test`, `race`, `lint`, and `tidy` are green (use the **Verify stage CI** script with `branch=dev`). Stop and report missing, pending, or failing checks before touching `staging`.
4. **Test-drive gate** — confirm a `test-driving-rally` pass cleared this exact dev SHA (the user saying it passed counts; so does a pass you just ran). No pass and no explicit user waiver → stop and report; do not promote untested code to `staging`.
5. **Merge to staging** — `git checkout staging && git merge --ff-only dev && git push origin staging`.
6. **Verify staging CI** — same required checks on the exact `staging` SHA (`branch=staging`). Stop on anything not green.
7. **Merge to main** — `git checkout main && git merge --ff-only staging && git push origin main`. (This push triggers the main test workflow plus any auto-tag/release workflows.)
8. Continue to **Watch CI** below.

### Starting from staging

1. **Sanity check** — must be on `staging` with a clean tree; `staging` must be ahead of `main` (else nothing to ship).
2. **Verify staging CI** — required checks green on the exact `staging` SHA (`branch=staging`).
3. **Merge to main** — `git checkout main && git merge --ff-only staging && git push origin main`, then continue to **Watch CI** below.

### Verify stage CI

Run this before each fast-forward, with `branch` set to the stage being promoted (`dev` before dev → staging, `staging` before staging → main):

```sh
branch=dev  # or staging
stage_sha="$(git rev-parse "$branch")"
run_id="$(
  gh run list --workflow test.yml --branch "$branch" --limit 50 \
    --json databaseId,headSha,status,conclusion,url \
    --jq ".[] | select(.headSha == \"$stage_sha\") | .databaseId" |
    head -n 1
)"

if [ -z "$run_id" ]; then
  echo "No test.yml workflow run found for $branch SHA $stage_sha" >&2
  exit 1
fi

gh run watch "$run_id" --exit-status || true

bad_required="$(
  gh run view "$run_id" --json jobs --jq '
    ["test","race","lint","tidy"] as $required |
    [ .jobs[] | select(.name as $name | $required | index($name)) ] as $seen |
    [
      $required[] as $name |
      (($seen[] | select(.name == $name)) // {name:$name,status:"missing",conclusion:"missing",url:""}) |
      select(.status != "completed" or .conclusion != "success") |
      "\(.name)\t\(.status)\t\(.conclusion)\t\(.url)"
    ] | .[]
  '
)"

if [ -n "$bad_required" ]; then
  echo "Required $branch checks are not green for $stage_sha:" >&2
  printf '%s\n' "$bad_required" >&2
  exit 1
fi
```

`audit`/govulncheck is advisory in CI and is not part of this required-check preflight. If any required check is missing, pending after the watch, cancelled, skipped, or failed, stop and surface the table above plus `gh run view "$run_id" --log-failed` output for the failing job.

### Watch CI

7. **Watch CI** — `gh run list --branch main --limit 10 --json databaseId,name,status,conclusion,headSha` to find the `test`, `auto-tag`, and `release` run IDs for the main SHA, then `gh run watch <id> --exit-status` for each in order (test -> auto-tag -> release).
8. **Install latest binary and smoke test** — after CI passes and the release is published:
   - Run `rally update` locally to fetch the latest published binary.
   - Create a throwaway git repo in `/tmp/rally-smoke-<tag>/` with a trivial prompt (e.g. "Create a file called smoke-test.txt").
   - Run a single-iteration relay with an ongoing free smoke-test model (prefer `op:opencode/big-pickle`; `zai-coding-plan/glm-5.1` is a fallback).
   - Verify: exit 0, file created, try record in `.rally/state/tries.jsonl` shows `"completed": true`.
   - This confirms the published binary actually works end-to-end before declaring the release done.
9. **Report** — final SHAs for `main`/`staging`/`dev` (and the feature branch), CI outcomes, the new release tag (if auto-tag fired), and smoke-test result.

## Stop conditions

Halt and report (do **not** attempt to recover, rebase, or force-push) when any of these hit:

- **Working tree dirty at start.** Stash, commit, or split before resuming.
- **Any local check fails.** If `just test`, `just check`, `just test-race`, `just tidy-check`, or `just audit` fails locally, fix on the feature branch, push, and restart from the local-checks step.
- **Required stage CI is not green.** If the exact `dev` (or `staging`) SHA is missing the `test.yml` run, or any required job (`test`, `race`, `lint`, `tidy`) is missing, pending, skipped, cancelled, or failed, stop before the next fast-forward and report the bad checks and job URLs. Do not rely on branch protection to reject the push.
- **No test-driving-rally pass for the dev SHA being promoted to `staging`**, and the user has not explicitly waived it. Run the test drive or stop and report.
- **Non-trivial merge conflict** during the dev, staging, or main merge. "Non-trivial" means real code/line conflicts. A failed fast-forward is also a stop — it means the branch is not strictly ahead of the target, and forcing it would rewrite history.
- **`main` is already at the current branch tip** (or the pipeline fast-forwards would all be no-ops). Nothing to ship. Report and exit.
- **CI test workflow fails.** Read the job log with `gh run view <id> --log` or `gh run view <id> --log-failed`, fix on the feature branch, push, and restart from step 2.
- **CI auto-tag or release workflow fails.** Same: read the job log, fix on the feature branch, push, and restart from step 2. Successful test is required before auto-tag fires; successful auto-tag is required before release fires.
- **Smoke test fails.** After `rally update`, if the local relay fails (agent unavailable is OK — report it; but a rally crash or missing file is a real regression), investigate. Do not declare the release done until the smoke test passes or the failure is understood to be an agent-side issue (rate limit, auth) rather than a rally regression.

## VERSION and the release build

`internal/buildinfo/VERSION` controls the auto-tag. The skill does **not** bump VERSION — that is a separate, user-driven decision (default: patch bump per the project's releasing convention in `AGENTS.md`).

If VERSION was bumped on the feature branch, the main push will fire `auto-tag` (creating `vX.Y.Z`) and then `release` (publishing the GitHub release). If VERSION was **not** bumped, the auto-tag step detects "no change" and exits 0 — the merge still lands, but no new release is published. That outcome is fine; the skill still succeeded at landing code.

## Conventions

- Use plain `git push` (no `--force`, no `--no-verify`) for all pushes.
- Use `git merge --ff-only` for the dev, staging, and main merges. If a fast-forward is not possible, that is a stop condition.
- Use `gh run watch <id> --exit-status` for CI; do not poll manually.
- Report SHAs (`git rev-parse origin/main origin/dev <branch>`) and CI run IDs at the end so the outcome is auditable.
- Do not delete or rewrite the feature branch or any commits during a successful run. Leave history intact.
- For the smoke test, prefer the ongoing free OpenCode model `op:opencode/big-pickle` to avoid cost. Use `--iterations 1` for speed. Clean up `/tmp/rally-smoke-*` after confirmation.

## Evolution

This skill is intentionally short. As Rally's release process matures, expand it in place — add sections, do not split into files — and bump the metadata `version` field. Likely future additions include: PR-based reviews, branch protection checks, sign-off requirements, release branches, hotfix flows, post-release announcements. Encode them here when they land.
