## 1. Instant-tier CI gates + trigger surface

- [ ] 1.1 Add a `dev` entry to the `push.branches` trigger in `.github/workflows/test.yml` (currently `main` only); leave the `pull_request` trigger as-is. This is the prerequisite for branch protection (a fast-forwarded `main` SHA must already carry green checks from its `dev` push — design Decision 5).
- [ ] 1.2 Add a `lint` job to the CI workflow: `actions/checkout` + `actions/setup-go` (`go-version-file: go.mod`), run `go vet ./...`, then assert `gofmt -l .` is empty and fail listing the offending files if not. No `laps`/`opencode`/real-agent setup needed (it does not run tests). Mirrors `just check`.
- [ ] 1.3 Add a `tidy` job: checkout + setup-go, run `go mod tidy`, then `git diff --exit-code go.mod go.sum` (fail with the diff if either changed).
- [ ] 1.4 Add an `audit` job: checkout + setup-go, `go install golang.org/x/vuln/cmd/govulncheck@latest`, run `govulncheck ./...` with `continue-on-error: true` (advisory — design Decision 2). Do NOT add it to the required-checks set in task 5.x.
- [ ] 1.5 Confirm the three instant-tier jobs are green on the current tree (they should find nothing): push to a branch / open a PR and verify `lint`, `tidy` pass and `audit` reports (advisory).

## 2. Race detector job

- [ ] 2.1 Add a `race` job to the CI workflow that mirrors the existing `test` job's tool installs (laps via `go install …/laps@<pinned>`, opencode install) but OMITS the `RALLY_TEST_REAL_AGENTS` env and the opencode-auth step, then runs `go test -race -shuffle=on -count=1 ./...` (design Decision 1).
- [ ] 2.2 Verify the `race` job passes on the current tree (race-clean as of 0.11.5). If it surfaces a real race, that is a genuine latent bug — add a focused fix task at the head of the queue rather than weakening the gate.
- [ ] 2.3 (Optional, post-observation) If the non-real-agent subset proves not to need `laps`/`opencode` at runtime, trim those installs from the `race` job for speed (design Open Question 2).

## 3. Local `just` mirroring

- [ ] 3.1 Add a `test-race` recipe to `justfile`: `go test -race -shuffle=on -count=1 ./...`.
- [ ] 3.2 Add an `audit` recipe: `govulncheck ./...` (document the one-time `go install golang.org/x/vuln/cmd/govulncheck@latest`).
- [ ] 3.3 Add a `tidy-check` recipe: `go mod tidy` then `git diff --exit-code go.mod go.sum`.
- [ ] 3.4 Leave the existing `check` recipe (vet + gofmt) unchanged; confirm all four recipes (`check`, `test-race`, `audit`, `tidy-check`) run green locally.

## 4. Release-flow wiring (`rally-release`)

- [ ] 4.1 Update the `rally-release` skill's local-checks step (`.claude/skills/rally-release/`) to run `just check`, `just test-race`, `just tidy-check`, and `just audit` before pushing, consistent with the skill's stop-on-failure contract.
- [ ] 4.2 Add a pre-fast-forward step to `rally-release` that verifies the `dev` commit's required CI checks are green (e.g. `gh run list`/`gh pr checks` on the `dev` SHA) and STOPS with the failing checks surfaced if not, before the `dev` → `main` fast-forward (design Decision 7).

## 5. Branch protection on `main`

- [ ] 5.1 Once the required jobs (`test`, `race`, `lint`, `tidy`) are green on both `dev` and `main`, enable GitHub branch protection on `main` requiring those four status checks. Do NOT include `audit` (advisory). Leave "Include administrators" OFF for first rollout (design Decision 6).
- [ ] 5.2 Smoke-test compatibility with the direct-push release flow: confirm `rally-release` can still fast-forward `main` to a green `dev` SHA, and that a hypothetical red required check would block the advance.
- [ ] 5.3 Document the protection setup and the hardened end state in the repo docs (README "Releasing"/CI section or AGENTS.md): the required checks, "Include administrators" off-now / on-later, and flipping govulncheck from advisory to required — so the follow-up is a recorded, deliberate step.

## 6. Verification

- [ ] 6.1 Verify `local == CI`: each of the five CI jobs (`test`, `race`, `lint`, `tidy`, `audit`) has a reproducing local path (`just test`, `just test-race`, `just check`, `just tidy-check`, `just audit`).
- [ ] 6.2 Confirm no binary-behaviour change and no version bump occurred: `internal/buildinfo/VERSION` is untouched and the diff is confined to `.github/workflows/`, `justfile`, `.claude/skills/rally-release/`, and docs.
- [ ] 6.3 Confirm the full gate suite is green on `dev` and `main`, branch protection is active with administrators excluded, and the advisory `audit` job is non-blocking.
