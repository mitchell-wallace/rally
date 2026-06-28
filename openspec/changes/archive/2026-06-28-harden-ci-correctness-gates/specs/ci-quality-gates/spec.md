## ADDED Requirements

### Requirement: Race detector gate

CI SHALL run the full Go test suite under the race detector and test shuffling
as a job distinct from the plain test job. The race job SHALL run
`go test -race -shuffle=on -count=1 ./...` and SHALL NOT set
`RALLY_TEST_REAL_AGENTS`. A detected data race SHALL fail the job.

#### Scenario: Race-clean tree passes

- **WHEN** CI runs the race job against a tree with no data races
- **THEN** the `race` job succeeds and reports a green status check

#### Scenario: Data race fails the build

- **WHEN** a commit introduces a data race reachable by the test suite
- **THEN** the `race` job fails with the race detector report and the status
  check is red

#### Scenario: Race job is independent of the plain test job

- **WHEN** CI runs for a commit
- **THEN** the plain `test` job and the `race` job run as separate jobs, so the
  fast plain-test signal is not blocked by the slower race run

### Requirement: vet and gofmt gate

CI SHALL run `go vet ./...` and SHALL assert that `gofmt -l .` produces no
output. Either a vet finding or any unformatted file SHALL fail the gate, and
the failure message SHALL name the offending files.

#### Scenario: Clean tree passes

- **WHEN** CI runs the lint gate against a vet-clean, gofmt-correct tree
- **THEN** the `lint` job succeeds

#### Scenario: Unformatted file fails

- **WHEN** a committed `.go` file is not gofmt-formatted
- **THEN** the `lint` job fails and lists the unformatted file paths

#### Scenario: copylocks violation fails

- **WHEN** a commit copies a lock-bearing value (e.g. the `sync.Mutex`-bearing
  `Store`) by value
- **THEN** `go vet`'s copylocks analyzer reports it and the `lint` job fails

### Requirement: Vulnerability scan gate

CI SHALL run `govulncheck ./...` against the module. On first rollout the
vulnerability scan SHALL be advisory: its result SHALL be visible in CI but
SHALL NOT block the pipeline, and it SHALL NOT be a required status check. The
posture SHALL be flippable to blocking by removing the advisory setting and
adding the job to the required checks, without other structural change.

#### Scenario: Reachable vulnerability is reported but advisory

- **WHEN** `govulncheck` reports a vulnerability in reachable code on first
  rollout
- **THEN** the finding is visible in the `audit` job output but the overall CI
  result is not failed by it and `main` is not blocked

#### Scenario: No reachable vulnerability

- **WHEN** `govulncheck` finds no vulnerability in reachable code
- **THEN** the `audit` job reports clean

### Requirement: Module tidiness gate

CI SHALL verify `go.mod` and `go.sum` are tidy by running `go mod tidy` and
failing if either file changes. A non-tidy module state SHALL fail the gate.

#### Scenario: Tidy module passes

- **WHEN** CI runs the tidy gate and `go mod tidy` produces no diff
- **THEN** the `tidy` job succeeds

#### Scenario: Misclassified or stale dependency fails

- **WHEN** a dependency is used directly but recorded `// indirect`, or an
  unused require remains, so `go mod tidy` changes `go.mod`/`go.sum`
- **THEN** the `tidy` job fails and shows the diff

### Requirement: Gate trigger surface

The correctness gates SHALL run on pushes to `dev` and `main` and on pull
requests, so that a commit which the release flow fast-forwards onto `main`
already carries green required status checks from its `dev` push.

#### Scenario: Push to dev runs the gates

- **WHEN** a commit is pushed to `dev`
- **THEN** the plain test, race, lint, tidy, and audit jobs run against that
  commit

#### Scenario: Fast-forwarded main commit is already checked

- **WHEN** `main` is fast-forwarded to a `dev` commit whose required checks
  passed
- **THEN** branch protection on `main` is satisfied by that commit's existing
  check runs without re-review

### Requirement: Branch protection on main

`main` SHALL be protected by required status checks comprising the blocking
gates: plain tests, race, vet+gofmt, and module tidiness. The advisory
vulnerability scan SHALL NOT be a required check on first rollout. "Include
administrators" SHALL be off on first rollout so a wedged or in-flight gate
cannot block a release, with the intended hardened end state being to enable it
once the gates are stable.

#### Scenario: Red required check blocks main

- **WHEN** a commit would advance `main` but one of its required checks (test,
  race, lint, tidy) is red or missing
- **THEN** the push/advance of `main` is rejected by branch protection

#### Scenario: Advisory scan does not block main

- **WHEN** the advisory `audit` (govulncheck) job is red but all required checks
  are green
- **THEN** `main` is allowed to advance

### Requirement: Local and release-flow mirroring

Every CI gate SHALL have a reproducing `just` recipe so `local == CI`: a
`test-race` recipe, an `audit` recipe (govulncheck), and a `tidy-check` recipe,
with the existing `check` recipe retained for vet+gofmt. The `rally-release`
flow SHALL run these gates locally before pushing and SHALL verify that the
`dev` commit's required CI checks are green before fast-forwarding `main`.

#### Scenario: Contributor reproduces CI locally

- **WHEN** a contributor runs `just check`, `just test-race`, `just tidy-check`,
  and `just audit`
- **THEN** they exercise the same gates CI enforces, before pushing

#### Scenario: Release flow stops on a non-green dev

- **WHEN** `rally-release` is asked to fast-forward `main` but the `dev`
  commit's required checks are not green
- **THEN** the release flow stops and surfaces the failing checks instead of
  pushing `main`
