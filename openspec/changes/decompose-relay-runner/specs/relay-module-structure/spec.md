## ADDED Requirements

### Requirement: Preserved public surface

The `internal/relay` package SHALL remain a single Go package named `relay`; this
change SHALL NOT introduce a new subpackage. The exported surface SHALL be
unchanged in name and signature, including (non-exhaustively) `Config`, `Runner`,
`NewRunner`, `Runner.Run`, `Runner.SetTelemetry`, `Runner.RequestStop`,
`Resilience`, `NewResilience`, `ResilienceKey`, `KeyFromAgent`, `AgentMix`,
`ParseAgentMix`, `Resolver`, `CancellationSource` (and its exported constants),
`FormatMixLabel`, `CreateRelay`, `ResumeRelay`, `CompleteRelay`, the `AgentState`
type and its `State*` constants, and the package's exported test helpers.

#### Scenario: Exported identifier set is unchanged

- **WHEN** the exported identifiers of `internal/relay` are compared before and
  after the decomposition
- **THEN** the set is identical — no identifier is added, removed, renamed, or
  has its signature changed

#### Scenario: Callers compile unchanged

- **WHEN** `go build ./...` runs over every package that imports `internal/relay`
- **THEN** it compiles with no source change required in any caller

#### Scenario: Package stays single, no subpackage

- **WHEN** the package layout is inspected after the change
- **THEN** all relay code is in `package relay` under `internal/relay/`, and no
  `internal/relay/<subdir>` package has been created

### Requirement: Behaviour, telemetry, and persistence preservation

The decomposition SHALL be behaviour-preserving. Runtime behaviour, CLI output,
telemetry event and field names (e.g. `RallyTry`, `RallyFailure`,
`RallyDiagnostic`), persisted store shape, laps claim/finalization semantics, and
agent-authored git commit messages SHALL be unchanged. The change SHALL NOT bump
`internal/buildinfo/VERSION` and SHALL NOT require a release.

#### Scenario: Test suite passes with only relocated tests

- **WHEN** `go test -count=1 ./...` and `go test -race -shuffle=on -count=1
  ./internal/relay` run after the decomposition
- **THEN** both pass, with the same set of test and benchmark functions as before
  (only relocated across files, none added, dropped, or rewritten)

#### Scenario: No behaviour-surface edits

- **WHEN** the diff of the change is reviewed
- **THEN** it is confined to `internal/relay/`, contains no telemetry-field, CLI-
  string, store-shape, laps-semantic, or git-message edit, and leaves
  `internal/buildinfo/VERSION` untouched

#### Scenario: Coverage does not regress

- **WHEN** `internal/relay` test coverage is compared before and after
- **THEN** coverage does not decrease, because behaviour is unchanged

### Requirement: Responsibility-named decomposition

`internal/relay/runner.go` SHALL be decomposed so that each file answers one
architectural question and every symbol has exactly one home, with no
catch-all/`misc` file. `runner.go` SHALL retain only the top-level relay flow
(the `Runner` type, its construction, and the relay-loop skeleton). The two
largest functions (`Run`, `runOne`) SHALL be decomposed into named private steps.
New production files SHALL use bare responsibility names consistent with existing
package siblings, qualified with a `runner_` prefix only where a bare name would
collide with an imported package. Enforcement of file-size and import-boundary
budgets is out of scope here and is owned by the `add-architecture-guardrails`
change.

#### Scenario: runner.go no longer mixes concerns

- **WHEN** `runner.go` is read after the change
- **THEN** it explains the top-level relay flow and no longer contains the
  terminal/display, telemetry-assembly, action-loop, liveness, task/prompt, git,
  final-snippet, laps/progress-validation, or handoff-only helper clusters

#### Scenario: No single function remains a hazard

- **WHEN** the relay/run lifecycle is inspected after the change
- **THEN** `Run` and `runOne` each delegate to named private step-methods, and no
  single function approaches the pre-change scale of `runOne` (~1,200 lines)

#### Scenario: Every symbol has one home

- **WHEN** the package files are inventoried
- **THEN** each former `runner.go` symbol lives in exactly one responsibility-
  named file (or an existing sibling for the two relocations), and no
  `misc`/`helpers` catch-all production file exists
