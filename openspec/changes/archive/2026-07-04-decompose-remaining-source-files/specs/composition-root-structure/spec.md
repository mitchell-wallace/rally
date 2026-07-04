## MODIFIED Requirements

### Requirement: Config module structure

`internal/config/config_v2.go` SHALL be split into responsibility-named files in
the same `package config` (`types.go`, `load.go`, `decode.go`, `validate.go`,
`resolve.go`, `save.go`). `internal/config/providers.go` SHALL likewise be
split into responsibility-named files in the same package, separating provider
parsing, provider resolution, and wildcard/model-filter matching. Each split
SHALL be a file-only move: no exported identifier SHALL be added, removed,
renamed, or have its signature changed, and no config error string or
deprecation message SHALL change unless a test proves a move forces it.

#### Scenario: Exported surface unchanged across the split

- **WHEN** the exported identifiers of `internal/config` are compared before and
  after the change
- **THEN** the sets are identical and differ only in declaring source file

#### Scenario: Config behaviour preserved

- **WHEN** `go test -count=1 ./internal/config ./cmd/rally` runs after the split
- **THEN** it passes with no assertion changes beyond test relocations, and
  deprecation notes and validation errors surface through the CLI exactly as
  before

#### Scenario: Provider concerns live apart

- **WHEN** `internal/config` is read after the providers split
- **THEN** provider parsing, provider resolution, and wildcard matching live in
  separate responsibility-named files with every symbol in exactly one home

## ADDED Requirements

### Requirement: Routes command module structure

The routes-check command surface in `internal/cli` SHALL be decomposed into
responsibility-named files in the same package, separating Cobra command
wiring, the route/role check core, result rendering, and reasoning/alias
validation. The split SHALL be a file-only move: no exported identifier,
command name, flag, help text, or operator-facing output string SHALL change.
Cobra usage SHALL remain confined to `internal/cli` per the third-party
dependency-confinement rules.

#### Scenario: Routes-check concerns live apart

- **WHEN** the routes-check code in `internal/cli` is read after the change
- **THEN** command wiring, check core, rendering, and validation live in
  separate responsibility-named files, and `rally routes check` output is
  byte-identical for the same inputs

#### Scenario: CLI behaviour preserved

- **WHEN** `go test -count=1 ./internal/cli ./cmd/rally` runs after the split
- **THEN** it passes with no assertion changes beyond test relocations
