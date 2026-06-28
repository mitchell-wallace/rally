## Draft: Harden CI Correctness Gates (Tiers 1–3)

Status: drafted 2026-06-23 — intent capture for later formalisation. This is
the first of a pair; the noisier static-analysis/property work lives in the
sibling draft `adopt-lint-and-fuzz-gates`.

This change is **CI/tooling only** — no binary behaviour changes, so it does
not need a version bump or a release. It can land independently of any feature
release.

## Why

CI today runs exactly one quality command (`.github/workflows/test.yml`):

```
go test -count=1 ./...
```

The `vet` + `gofmt` checks that exist locally (`just check`) are **never run in
CI** — CI calls `go test` directly, not `just check`. There is no race
detector, no vulnerability scan, and no `go mod tidy` drift check.

This gap bit us concretely in the 0.11.x line:

- **The store data race (fixed in 0.11.5) shipped undetected for a long time.**
  `internal/store` had no synchronization; the relay E2E tests read the store
  from the test goroutine while `Run()` wrote from another. `go test -race`
  catches it instantly — but CI never runs `-race`, so it was only visible on a
  manual local race run.
- **A dependency landed mis-classified.** `muesli/cancelreader` was added as a
  direct import but `go.mod` kept it `// indirect` until a manual `go mod tidy`.
  A tidy-drift gate catches that whole class.
- **The stakes are higher than usual here.** A push to `main` auto-tags and
  *publishes a release* (`auto-tag.yml` → `release.yml`). A red or unsafe
  `main` is therefore directly shippable. Gating `main` matters more than in a
  repo where `main` is just an integration branch.

Everything proposed here is either something the code **already passes** (vet,
gofmt) or a check with **near-zero false positives** (race, govulncheck,
mod-tidy). No new judgment calls, no backlog of findings to triage — that is
deliberately what separates this change from `adopt-lint-and-fuzz-gates`.

## Intent

Turn the CI gate from "the tests pass" into "the tests pass **and** the code is
race-free, free of known-reachable CVEs, correctly formatted, vet-clean, and
its module files are tidy" — without introducing any check that produces a
findings backlog. Make `local == CI` so contributors (and the `rally-release`
skill) can reproduce every gate before pushing.

## Candidate work

### A. Run the suite under the race detector + shuffle

Add a CI step/job running:

```
go test -race -shuffle=on -count=1 ./...
```

- `-race` is the headline gate — the relay E2E suite is exactly where races
  hide (it drives `Run()` concurrently with store reads).
- `-shuffle=on` is ~free and catches tests that secretly depend on order or
  leak global state.

Caveats / decisions to make when formalising:

- `-race` needs CGO (the `ubuntu-latest` runner has it) and runs ~2–20× slower.
  The relay E2E suite (~25 s plain) becomes the long pole.
- The current job sets `RALLY_TEST_REAL_AGENTS=1`. Race + real-agent runs could
  be slow/flaky. Options: run race over the **non-real-agent** subset for
  speed, run a **separate parallel `race` job** alongside the existing fast
  plain-`go test` job, or accept one slower combined job.

### B. Enforce `vet` + `gofmt` in CI

Promote the existing local `just check` into the pipeline as its own step/job:

- `go vet ./...` — already passes; notably includes the `copylocks` analyzer,
  newly relevant since 0.11.5 added a `sync.Mutex` to `Store` (catches anyone
  copying it by value).
- `gofmt -l .` must be empty (fail otherwise).

The code already passes both, so this is pure enforcement of the status quo.

### C. Add `govulncheck`

Add a job running `govulncheck ./...` (install
`golang.org/x/vuln/cmd/govulncheck`). It is call-graph-aware — it only reports
vulnerabilities in code paths actually reached — so it is low-noise. Decide
blocking vs advisory for the first rollout.

### D. Add a `go mod tidy` drift check

Run `go mod tidy` in CI and fail if `go.mod`/`go.sum` changed. Catches exactly
the indirect→direct drift from the cancelreader episode, plus stale/unused
requires.

### E. Mirror every gate in `just` + the release flow

- Add `just` recipes so local reproduces CI exactly, e.g. `just test-race`,
  `just audit` (govulncheck), `just tidy-check`. Keep `just check` as the
  vet+gofmt recipe it already is.
- Update the `rally-release` skill's "Local checks" step to run the new
  recipes, so the ship-it flow validates them before pushing to `main`.

### F. (Optional, process) Branch protection on `main`

Require these checks green before `main` advances. **Flag, don't assume:** the
repo's `rally-release` flow fast-forwards and pushes `main` directly (dev →
main), and auto-tag fires on that push. Required-status-check protection
interacts with that model and with the direct-push convention, so this is a
decision for formalisation, not a default-on.

## Sequencing

B (vet/gofmt), C (govulncheck), and D (mod-tidy) are instant, find nothing
today, and can land together immediately. A (`-race`) is the highest-value gate
but the one with real CI-time cost and the most config choices, so it can land
as a deliberate second step. F is a process decision that can come last.

## Open questions

- Race job: fold into the existing test job, or a separate parallel job? With
  or without `RALLY_TEST_REAL_AGENTS=1`?
- `govulncheck`: blocking or advisory for the first few weeks?
- Branch protection vs the direct-push-to-`main` release flow — compatible, or
  do we adjust the release flow?

## Out of scope (see sibling draft `adopt-lint-and-fuzz-gates`)

- `staticcheck` / `golangci-lint` and any linter that surfaces a findings
  backlog (`errcheck`, `unused`, `gosec`, …).
- Fuzz testing of parsers/classifiers.
- `gofumpt` (stricter formatting) — kept with the lint change so the formatting
  standard is decided in one place.
- Coverage thresholds.
