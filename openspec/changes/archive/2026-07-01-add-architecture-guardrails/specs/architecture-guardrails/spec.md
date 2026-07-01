## ADDED Requirements

### Requirement: Architecture checker tool

The repository SHALL provide a dependency-free Go architecture checker at
`tools/archguard` (`package main` in the existing module) that parses each `.go`
file with `parser.ImportsOnly`, counts physical lines, and applies a policy
engine. The checker SHALL import only the Go standard library, so it adds no
third-party dependency and `go mod tidy` over the module SHALL remain a no-op
because of it. The checker SHALL skip files beginning with `// Code generated`,
and SHALL skip `testdata`, `vendor`, build output, and hidden bookkeeping
directories (`.git`, `.rally`, `.laps`). The policy engine SHALL be unit-testable
independently of the file walk and output. The checker SHALL provide a `--report`
mode that prints the current over-budget files as a regeneratable grandfather
baseline and a `--ci` mode whose exit code reflects only hard violations.

#### Scenario: Checker adds no dependency

- **WHEN** `go mod tidy` runs on the module after `tools/archguard` is added
- **THEN** `go.mod` and `go.sum` are unchanged, because the checker imports only
  the Go standard library

#### Scenario: Generated and bookkeeping paths are skipped

- **WHEN** the checker walks the repository
- **THEN** files beginning with `// Code generated` are exempt from size budgets,
  and `testdata`, `vendor`, `.git`, `.rally`, and `.laps` are not scanned

#### Scenario: Report mode regenerates the baseline

- **WHEN** `archguard --report` runs against the current tree
- **THEN** it prints every file over its hard-size budget as a paste-ready
  grandfather map and prints any import-boundary or dependency-confinement
  violation, without changing its exit behaviour into a hard failure on warnings

### Requirement: File-size budgets with grandfathered caps

The checker SHALL enforce file-size budgets: production `.go` files warn at 500
lines and hard-fail at 800 lines; `_test.go` files warn at 700 lines and
hard-fail at 1,000 lines; generated files are exempt but SHALL carry the
`// Code generated` marker. The hard-fail budgets SHALL apply only to files not
listed in a grandfather map. The grandfather map SHALL record each currently
over-budget file at its actual line count, and a grandfathered file SHALL hard-
fail if it grows above its recorded cap. The grandfather map SHALL be generated
from the tree at implementation time (via `--report`), not hard-coded to any
earlier snapshot. Warnings (over 500 / over 700) SHALL NOT set a non-zero exit
code; only hard violations SHALL.

#### Scenario: New oversize file fails

- **WHEN** a new production `.go` file of 900 lines (not in the grandfather map)
  is added
- **THEN** `archguard --ci` exits non-zero and names the file with its line count
  and the 800-line production budget

#### Scenario: Grandfathered file may not grow

- **WHEN** a grandfathered file is edited to exceed its recorded cap
- **THEN** `archguard --ci` exits non-zero and reports the new size against the
  cap, instructing that the cap ratchets down, never up

#### Scenario: Warning does not fail the build

- **WHEN** a production file is between 500 and 800 lines and is not grandfathered
- **THEN** the default checker prints a warning but `archguard --ci` exits zero
  for that file

#### Scenario: Clean baseline passes

- **WHEN** `archguard --ci` runs against the tree at landing, with the grandfather
  map generated from that same tree
- **THEN** it exits zero, because every over-budget file is grandfathered at its
  current size and no other file exceeds its hard budget

### Requirement: Internal import-boundary rules

The checker SHALL enforce production-file internal import boundaries that match
the current production dependency graph, keeping the edges established by
`relay-module-structure` (#1) and `composition-root-structure` (#2) one-way. In
particular: `internal/relay` SHALL NOT import `internal/relay/runner`;
`internal/relay` and `internal/relay/runner` SHALL NOT import `internal/config`
or `internal/cli`; `internal/release` SHALL NOT import `internal/app`;
`internal/app` SHALL NOT import `internal/cli`, `internal/user_prompt`, or
`internal/laps`; and no `internal/*` package SHALL import `internal/cli`. Lower-
level production packages SHALL be held to tight internal allow-lists matching
the current tree, while `internal/cli` and `cmd/rally` are the
composition/presentation layers permitted broad internal imports. `_test.go`
files are not boundary violations for these internal allow-lists in v1, but they
remain subject to third-party dependency confinement.

#### Scenario: Flagship runner → relay edge stays one-way

- **WHEN** a change makes a production file in `internal/relay` import
  `internal/relay/runner`
- **THEN** `archguard --ci` exits non-zero with a message explaining the relay
  primitives must not depend on the orchestrator

#### Scenario: App seam stays presentation-neutral

- **WHEN** a change makes a production file in `internal/app` import `internal/cli`,
  `internal/user_prompt`, or `internal/laps`
- **THEN** `archguard --ci` exits non-zero, preserving the #2 seam where `app`
  reaches `laps` only transitively through `internal/relay/runner`

#### Scenario: Release metadata does not cycle through app

- **WHEN** a change makes a production file in `internal/release` import
  `internal/app`
- **THEN** `archguard --ci` exits non-zero, because that would recreate the
  `app → runner → laps → release → app` cycle #2 broke

#### Scenario: Current graph passes unchanged

- **WHEN** `archguard --ci` runs against the unmodified current tree
- **THEN** it exits zero for all import-boundary rules, because the rules are
  generated to match that production graph

### Requirement: Third-party dependency confinement

The checker SHALL confine third-party dependencies to their owning packages,
applied to production and test files alike:
`github.com/newrelic/go-agent` only under `internal/telemetry`;
`github.com/pelletier/go-toml` only under `internal/config`;
`github.com/spf13/cobra` only under command-shaped packages (`cmd/rally`,
`internal/cli`, `internal/progress`); `github.com/charmbracelet/huh` only under
interactive-prompt packages (`internal/cli`, `internal/user_prompt`); and
`github.com/charmbracelet/lipgloss` only under `internal/style` and
`internal/cli`. The first pass SHALL confine these obvious owners and SHALL NOT
attempt to encode every incidental import or broader terminal dependency.

#### Scenario: New Relic leak outside telemetry fails

- **WHEN** any file outside `internal/telemetry` imports
  `github.com/newrelic/go-agent`
- **THEN** `archguard --ci` exits non-zero with a message that New Relic is owned
  by `internal/telemetry`

#### Scenario: Command-shaped Cobra usage is allowed

- **WHEN** `internal/progress` imports `github.com/spf13/cobra` for its
  command-shaped CLI
- **THEN** `archguard --ci` exits zero, because `internal/progress` is on the
  Cobra allow-list alongside `cmd/rally` and `internal/cli`

### Requirement: Test-helper confinement

The checker SHALL fail if any non-test (`.go`, not `_test.go`) file imports
`internal/testutil`, so test helpers cannot leak into production code.

#### Scenario: Production import of testutil fails

- **WHEN** a non-test file imports `internal/testutil`
- **THEN** `archguard --ci` exits non-zero and names the offending file

### Requirement: Human-readable diagnostics

Every hard violation the checker reports SHALL name the offending file
(repo-relative) and explain the architectural reason for the rule, not merely the
rule name, so a CI failure is actionable without reading the policy source.

#### Scenario: Diagnostic explains the boundary

- **WHEN** an import-boundary or dependency-confinement rule is violated
- **THEN** the printed diagnostic includes the file path, the offending import,
  and a one-line reason describing the architectural intent the rule protects

### Requirement: Local and CI wiring

The checker SHALL be wired into both local and CI flows. A `just arch-check`
recipe SHALL run the checker in advisory mode (warnings plus hard violations),
and the existing `just check` recipe SHALL invoke `just arch-check` after its
formatting assertion so local checks mirror CI while preserving the current
`check: vet` ordering. The `lint` job in `.github/workflows/test.yml` SHALL run
the checker in `--ci` mode, hard-failing only on disallowed imports, new oversize
files, grandfathered-file growth, dependency-confinement breaches, and production
`testutil` imports — never on advisory size warnings. No new CI job or required
status check beyond the existing `lint` gate SHALL be introduced, and the CI
trigger surface SHALL be unchanged.

#### Scenario: Local check mirrors CI

- **WHEN** a developer runs `just check`
- **THEN** `arch-check` runs as part of it and a hard violation fails the local
  check the same way it fails the CI `lint` job

#### Scenario: CI lint job gates on hard violations only

- **WHEN** CI runs the `lint` job on a commit with a disallowed import or an
  oversize new file
- **THEN** the `lint` job fails on the architecture step, while a commit whose
  only finding is an advisory size warning still passes

### Requirement: Tooling-only, no runtime or release impact

This change SHALL be tooling-and-CI only. It SHALL NOT change any Rally runtime
behaviour, command, flag, config schema, telemetry, store shape, or released
binary; `tools/archguard` SHALL NOT be compiled into `cmd/rally` or shipped by
GoReleaser. The change SHALL NOT bump `internal/buildinfo/VERSION` and SHALL NOT
require a release.

#### Scenario: No runtime or version change

- **WHEN** the diff of the change is reviewed
- **THEN** it changes no Rally runtime files, touches no release packaging, leaves
  `internal/buildinfo/VERSION` unchanged, and the released binary's behaviour is
  unaffected
