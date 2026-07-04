## ADDED Requirements

### Requirement: Oversized test suites split along production lines

Every `_test.go` file in the `add-architecture-guardrails` test grandfather map SHALL be split —
at baseline: `run_one_test.go`, `runner_failure_telemetry_test.go`,
`relay_steps_test.go`, `config_v2_test.go`, `route_runtime_test.go`,
`store_test.go`, `resilience_test.go`, `runner_outcome_test.go` —
into responsibility-named `_test.go` files in the same package that mirror the
package's production file layout where practical, so the file name answers
"where is X tested" before the file is opened. Each resulting `_test.go` file
SHALL be under the 1,000-line hard budget without a grandfather entry. A test
function that spans multiple production files SHALL live with the behaviour it
primarily pins, and no single test function SHALL be split across files.

#### Scenario: Test files mirror the production index

- **WHEN** the split packages are read after #6/#7's production layouts are
  final
- **THEN** each split test file's name corresponds to a production file or a
  named behaviour family in that package, and no `_test.go` file in those
  packages exceeds 1,000 lines

#### Scenario: Test grandfather map empties

- **WHEN** `go run ./tools/archguard --report` regenerates the baseline after
  the change and `go run ./tools/archguard --ci` runs
- **THEN** no `_test.go` grandfather entry remains and `--ci` exits 0

### Requirement: Shared test setup lives in per-package helper files

Fixtures, builders, and table-driven scaffolding shared across split test files SHALL
move into the owning package's existing helper test file
(e.g. `internal/relay/runner/helpers_test.go`) or a single new helper test
file where none exists — never duplicated per case file and never into a new
parallel helper file where one already exists. Helper extraction SHALL be a
verbatim relocation of existing setup code with no fixture-semantics change,
and helper test files are subject to the same 1,000-line cap.

#### Scenario: One helper home per package

- **WHEN** a split package's test files are inventoried
- **THEN** shared setup appears in exactly one helper test file per package and
  each case file contains only its cases and case-local setup

### Requirement: Reorganization preserves every test exactly once

The test reorganization SHALL be arrangement-only. A name-level inventory of
every `Test*`/`Benchmark*`/`Fuzz*` function taken before the change SHALL match
the post-change inventory exactly (same multiset, each declared exactly once).
Assertions, fixtures' meaning, and coverage SHALL be unchanged; no production
file SHALL be edited; the change SHALL NOT bump `internal/buildinfo/VERSION`
and SHALL NOT require a release.

#### Scenario: Inventory and suites green

- **WHEN** `go test -count=1 ./...` and `go test -race -shuffle=on -count=1
  ./...` run after the reorganization
- **THEN** both pass with identical assertions, and the pre/post test-function
  inventories match exactly

#### Scenario: No production edits

- **WHEN** the diff of the change is reviewed
- **THEN** it touches only `_test.go` files (plus the regenerated archguard
  baseline), and leaves `internal/buildinfo/VERSION` untouched
