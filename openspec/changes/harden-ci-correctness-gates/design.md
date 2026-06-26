## Context

CI is a single workflow, `.github/workflows/test.yml`, with one job that runs
`go test -count=1 ./...` under `RALLY_TEST_REAL_AGENTS=1`. It triggers on
`push` to `main` and on `pull_request`. The local `just check` recipe runs
`go vet ./...` + `gofmt -l .`, but CI never invokes `just` — so vet/gofmt are
unenforced on the shared history. There is no race detector, no `govulncheck`,
and no `go mod tidy` drift check.

Two constraints shape every decision here:

1. **`main` is directly shippable.** `auto-tag.yml` watches
   `internal/buildinfo/VERSION` on `main` and creates `vX.Y.Z`, which dispatches
   `release.yml` to publish a GitHub release. A red or unsafe `main` ships.
2. **The release flow pushes `main` directly.** The `rally-release` skill
   fast-forwards `dev` → `main` and pushes, rather than merging a PR. Any
   `main` gating must be satisfiable by a commit that already exists and passed
   on `dev`, or it will wedge the release flow.

Everything proposed is either already-passing (vet, gofmt) or near-zero
false-positive (race, govulncheck, mod-tidy). There is no findings backlog to
triage — that is the deliberate line between this change and its sibling
`adopt-lint-and-fuzz-gates`.

## Goals / Non-Goals

**Goals:**

- CI enforces: race-freedom, vet-cleanliness, gofmt-correctness, module-file
  tidiness, and (advisory) known-CVE-freedom — in addition to the existing test
  pass.
- `local == CI`: every gate is reproducible via a `just` recipe, and the
  `rally-release` flow runs them before pushing.
- `main` is protected by required status checks, in a way that is compatible
  with the direct fast-forward release flow.
- No new check produces a findings backlog or a judgment call.

**Non-Goals:**

- `staticcheck` / `golangci-lint`, `errcheck`, `gosec`, fuzzing, `gofumpt`,
  coverage thresholds — all deferred to `adopt-lint-and-fuzz-gates`.
- Any change to binary behaviour, runtime code, or the release/versioning
  mechanism itself (no version bump; CI/tooling only).
- Speeding up the existing test suite or reworking the real-agent harness.

## Decisions

**1. Race runs as a separate parallel job over the non-real-agent suite.**
Add a `race` job running `go test -race -shuffle=on -count=1 ./...` with
`RALLY_TEST_REAL_AGENTS` unset. `-race` is the headline gate (the relay E2E
suite drives `Run()` concurrently with store reads — exactly where the 0.11.5
race lived); `-shuffle=on` is ~free and catches order-dependent or
global-state-leaking tests. Keeping it a *separate* job preserves the fast
plain-`go test` job as the quick signal and isolates the 2–20× race slowdown so
it does not gate fast feedback. Dropping `RALLY_TEST_REAL_AGENTS` is deliberate:
real-agent runs are slow and flaky, and races hide in Rally's own orchestration
(store, runner, relay) — which the non-real-agent suite already exercises — not
in the external CLIs. The `race` job mirrors the plain job's tool installs
(laps, opencode) so the same tests compile and run; it just omits the real-agent
env and auth. *Alternatives considered:* one combined job with `-race` (simpler,
but one slow job and inherited real-agent flake); a race job that keeps real
agents (slowest, flakiest, no extra race coverage where it matters).

**2. `govulncheck` is advisory on first rollout.** Add an `audit` job running
`govulncheck ./...` (installed via `golang.org/x/vuln/cmd/govulncheck`). It is
call-graph-aware — it reports only vulnerabilities in reachable code — so it is
inherently low-noise. It runs with `continue-on-error: true` (advisory) for the
first rollout, and is **not** in the required-status-check set, so a newly
disclosed transitive CVE cannot suddenly wedge an auto-publishing `main`. The
flip to blocking (remove `continue-on-error`, add to required checks) is a
one-line follow-up once the gate has proven quiet; the spec records this as a
deliberate, reversible posture rather than a permanent advisory. *Alternative
considered:* blocking from day one — strongest posture, but couples release
availability to upstream disclosure timing, which is the wrong trade for a repo
where `main` publishes on push.

**3. A `go mod tidy` drift gate.** Add a `tidy` job that runs `go mod tidy`,
then fails if `git diff --exit-code go.mod go.sum` is non-empty. This catches
the indirect→direct misclassification from the cancelreader episode plus
stale/unused requires. It is deterministic and finds nothing on a tidy tree.

**4. vet + gofmt enforced as a single `lint` job.** Promote the existing
`just check` into CI as a `lint` job: `go vet ./...` then assert `gofmt -l .` is
empty (fail with the offending files listed). The code already passes both, so
this is status-quo enforcement. `vet`'s `copylocks` analyzer is newly load-
bearing since 0.11.5 added a `sync.Mutex` to `Store` — it now guards against
anyone copying the store by value.

**5. Trigger the gate suite on `dev` and `main` pushes (and PRs) — this is
what makes branch protection compatible with the direct-push release flow.**
Today `test.yml` only triggers `push` on `main`. Add `dev` to the push branches
so every commit that lands on `dev` runs the full gate suite. Because
`rally-release` fast-forwards `main` to an existing `dev` SHA, GitHub's required
status checks on `main` are evaluated against that same commit — which already
has green check runs from its `dev` push. Without the `dev` trigger, the
fast-forwarded commit would have no check runs on it and branch protection would
reject the push. PRs continue to trigger via `pull_request`.

**6. Branch protection on `main`, administrators excluded initially.** Configure
GitHub branch protection on `main` requiring the blocking status checks (plain
`test`, `race`, `lint`, `tidy`; `audit`/govulncheck is advisory and **not**
required). Leave **"Include administrators" off** for the first rollout: the
maintainer who runs `rally-release` can still fast-forward `main` even if a gate
is mid-flight or a non-required job is degraded, so a wedged gate never blocks a
release. The protection's real job at this stage is to make an accidental red or
unsafe direct push to `main` impossible for the normal path. Turning
"Include administrators" on (and/or flipping govulncheck to required) is the
documented hardening follow-up once the gates are stable. *Alternative
considered:* administrators included from day one — strongest, but it can lock
the sole maintainer out of shipping if a required gate flakes, with no PR-merge
escape hatch in the direct-push model.

**7. `rally-release` verifies `dev` CI is green before fast-forwarding.** Add a
step to the `rally-release` skill's flow that, before the `dev` → `main`
fast-forward, confirms the latest `dev` commit's required checks are green (e.g.
via `gh run list`/`gh pr checks` on the `dev` SHA). This turns branch protection
from a late "push rejected" surprise into an early, explicit pre-flight check,
and keeps the skill's stop-on-failure contract honest. The skill's local-checks
step additionally runs `just check`, `just test-race`, `just tidy-check`, and
`just audit` so a contributor reproduces CI before pushing.

**8. Mirror every gate in `just`.** Add `test-race`
(`go test -race -shuffle=on -count=1 ./...`), `audit` (`govulncheck ./...`), and
`tidy-check` (`go mod tidy` + `git diff --exit-code go.mod go.sum`). Keep
`check` as the existing vet+gofmt recipe. This is the `local == CI` contract:
every CI gate has a one-word local equivalent.

## Risks / Trade-offs

- **Race job CI time** (the relay E2E suite, ~25 s plain, becomes the long pole
  under `-race`) → Mitigation: it is a *separate parallel* job, so it does not
  slow the fast plain-test signal; dropping real-agents trims the heaviest,
  flakiest tests; `ubuntu-latest` has CGO for `-race`.
- **`-race` surfaces a real race in in-flight feature work** (e.g. the
  concurrent `improve-harness-consistency` runner/telemetry edits) → Mitigation:
  this is the gate working as intended, not a regression of this change. Landing
  the gates first actively protects that merge; a surfaced race is a real latent
  bug to fix, not gate noise.
- **Branch protection wedges the direct-push release flow** → Mitigation:
  Decision 5 (gate the `dev` trigger so the FF'd SHA is pre-green) + Decision 6
  ("Include administrators" off) + Decision 7 (`rally-release` pre-flight green
  check). The three together keep the release flow unblocked while still
  forbidding an accidental red/unsafe `main`.
- **`go mod tidy` behaviour can drift across Go toolchain versions** →
  Mitigation: CI pins the toolchain via `go-version-file: go.mod` (already the
  case), so local and CI tidy agree as long as the `go` directive is respected.
- **govulncheck advisory means a reachable CVE can land** → Mitigation:
  accepted for the first rollout; it is visible in the job summary, and the flip
  to blocking is a one-line follow-up recorded in the spec.

## Migration Plan

1. **Instant tier together** (`lint`, `tidy`, `audit`-advisory) — they find
   nothing on today's tree and need no config choices. Add the `dev` push
   trigger in the same step so the branch-protection prerequisite is in place.
2. **Race job second** — highest value, but the one with real CI-time cost; land
   it as a deliberate separate job once the instant tier is green.
3. **`just` recipes + `rally-release` wiring** — mirror the gates locally and in
   the release pre-flight.
4. **Branch protection on `main` last** — once all required jobs are green on
   `dev` and `main`, enable required status checks (administrators excluded),
   and document the flip-on hardening (administrators included; govulncheck
   required).
5. **Rollback**: every gate is independently revertible (delete the job /
   recipe / protection rule). No code or release-mechanism change is involved,
   so there is no data or version rollback to consider.

## Open Questions

- **When to flip govulncheck to blocking and "Include administrators" on?**
  Both are deliberately deferred to a follow-up after a stabilisation window
  (suggested: one or two quiet weeks). Recorded as a posture decision, not an
  unknown — the spec captures the initial state and the intended end state.
- **Does any non-real-agent test transitively require the `laps` binary at
  runtime?** The `race` job installs `laps`/`opencode` to be safe; if the
  non-real-agent subset proves not to need them, the installs can be trimmed
  later for speed. Resolve during implementation by observing the race job.
