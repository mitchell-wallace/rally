## Why

CI runs a single quality command — `go test -count=1 ./...` — so the `vet` and
`gofmt` checks that exist locally in `just check` never run in CI, and there is
no race detector, no vulnerability scan, and no `go mod tidy` drift check. This
gap let two real defects ship undetected in the 0.11.x line: the `Store` data
race (fixed in 0.11.5; the relay E2E suite reads the store concurrently with
`Run()`, which `go test -race` catches instantly) and a `muesli/cancelreader`
import that stayed `// indirect` in `go.mod` until a manual tidy. Because a push
to `main` auto-tags and publishes a release (`auto-tag.yml` → `release.yml`), a
red or unsafe `main` is directly shippable — so gating `main` matters more here
than in a repo where `main` is just an integration branch.

## What Changes

- Add a CI job running the suite under the race detector and shuffle
  (`go test -race -shuffle=on -count=1 ./...`) as a **separate parallel job**
  over the non-real-agent suite (no `RALLY_TEST_REAL_AGENTS`), so the fast
  plain-`go test` job stays the quick signal and the 2–20× race slowdown is
  isolated.
- Promote the existing local `just check` into CI as a required gate:
  `go vet ./...` (includes `copylocks`, newly relevant since 0.11.5 put a
  `sync.Mutex` in `Store`) and `gofmt -l .` must be empty.
- Add a `govulncheck ./...` job, **advisory (non-blocking) on first rollout**,
  with a documented path to flip it to blocking once it proves quiet.
- Add a `go mod tidy` drift gate that fails if `go.mod`/`go.sum` change —
  catching exactly the indirect→direct drift from the cancelreader episode.
- Run the gate suite on `dev` and `main` pushes (and PRs), so a `main` that the
  release flow fast-forwards to an already-tested `dev` commit carries green
  required checks on that same SHA.
- Mirror every gate in `just` (`test-race`, `audit`, `tidy-check`; keep `check`
  as the vet+gofmt recipe) and in the `rally-release` local-checks step, so
  `local == CI` and the ship-it flow validates every gate before pushing.
- Enable **branch protection on `main`** requiring the blocking gates (plain
  tests, race, vet+gofmt, tidy), with "include administrators" left off
  initially so a wedged gate cannot lock out a release, and add a `rally-release`
  step that verifies `dev` CI is green before the fast-forward to `main`.

This change is **CI/tooling only** — no binary behaviour changes, so it needs no
version bump and no release, and it can land independently of any feature work.

## Capabilities

### New Capabilities

- `ci-quality-gates`: the automated correctness gates CI enforces (race+shuffle,
  vet, gofmt, govulncheck, mod-tidy), each gate's blocking-vs-advisory posture
  and trigger surface, the `local == CI` mirroring contract across `just` and
  the release flow, and branch protection on `main`.

### Modified Capabilities

<!-- None. This change adds CI/tooling enforcement; it changes no runtime
     behaviour requirement, so no existing capability spec is modified. -->

## Impact

- **CI** (`.github/workflows/`): the existing `test.yml` gains a `dev` push
  trigger; new jobs for race+shuffle, vet+gofmt, govulncheck (advisory), and
  mod-tidy drift. These become named status checks consumed by branch
  protection.
- **Tooling** (`justfile`): new `test-race`, `audit` (govulncheck), and
  `tidy-check` recipes; `check` stays the vet+gofmt recipe it already is.
- **Release flow** (`.claude/skills/rally-release`): the local-checks step runs
  the new recipes; a new step verifies `dev` CI is green before fast-forwarding
  `main`. Branch protection on `main` is configured in GitHub settings
  (documented; "include administrators" off initially, with a flip-on path).
- **Code**: none expected — the tree already passes vet/gofmt and is race-clean
  as of 0.11.5, and govulncheck reports nothing reachable today. Any finding the
  gates surface is a real latent bug, not churn.
- **Out of scope** (sibling draft `adopt-lint-and-fuzz-gates`): staticcheck /
  golangci-lint and any linter with a findings backlog, fuzz testing, `gofumpt`,
  and coverage thresholds.
