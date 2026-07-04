# relay-module-structure Specification

## Purpose
TBD - created by archiving change decompose-relay-runner. Update Purpose after archive.
## Requirements
### Requirement: Runner package boundary

The relay orchestrator SHALL live in its own package, `internal/relay/runner`
(`package runner`), distinct from the relay primitives in `internal/relay`. The
dependency SHALL be one-way: `internal/relay/runner` MAY import `internal/relay`,
and `internal/relay` SHALL NOT import `internal/relay/runner`. The exported API
SHALL be relocated without signature change: `Config`, `Runner`, `NewRunner`,
`Runner.Run`, `Runner.SetTelemetry`, `Runner.RequestStop`, and `CancellationSource`
(with its constants) SHALL be exported from `package runner`; `Resilience`,
`NewResilience`, `ResilienceKey`, `KeyFromAgent`, `AgentState`/`State*`,
`AgentMix`, `ParseAgentMix`, `Resolver`, `FormatMixLabel`, `CreateRelay`,
`ResumeRelay`, `CompleteRelay`, and the resilience-timing constants SHALL remain
exported from `package relay`.

#### Scenario: One-way dependency, no cycle

- **WHEN** the package import graph is inspected after the change
- **THEN** `internal/relay/runner` imports `internal/relay`, `internal/relay` does
  not import `internal/relay/runner`, and the build has no import cycle

#### Scenario: Exported API relocated, not redesigned

- **WHEN** the exported identifiers of `internal/relay` and `internal/relay/runner`
  are compared against the pre-change `internal/relay` surface
- **THEN** every former identifier appears in exactly one of the two packages with
  an unchanged signature — none is added, removed, or renamed — with the runner
  type/constructor/control symbols under `runner` and the relay/resilience/mix
  primitives under `relay`

#### Scenario: Callers compile with only import/qualifier updates

- **WHEN** `go build ./...` runs over every caller (notably `cmd/rally`)
- **THEN** it compiles with no change beyond importing `internal/relay/runner` and
  qualifying the relocated symbols (`runner.NewRunner`, `runner.Config`)

### Requirement: Behaviour, telemetry, and persistence preservation

The decomposition SHALL be behaviour-preserving. Runtime behaviour, CLI output,
telemetry event and field names (e.g. `RallyTry`, `RallyFailure`,
`RallyDiagnostic`), persisted store shape, laps claim/finalization semantics, and
agent-authored git commit messages SHALL be unchanged. The change SHALL NOT bump
`internal/buildinfo/VERSION` and SHALL NOT require a release.

#### Scenario: Test suite passes with only relocated tests

- **WHEN** `go test -count=1 ./...` and `go test -race -shuffle=on -count=1
  ./internal/relay/...` run after the decomposition
- **THEN** both pass, every pre-change test and benchmark function appears exactly
  once across the two packages according to the inventory, and any new test
  functions are limited to the explicit route-label regression coverage needed for
  the package split

#### Scenario: No behaviour-surface edits

- **WHEN** the diff of the change is reviewed
- **THEN** it contains no telemetry-field, CLI-string, store-shape, laps-semantic,
  or git-message edit, and leaves `internal/buildinfo/VERSION` untouched

#### Scenario: Coverage does not regress

- **WHEN** the `go tool cover -func` total for `./internal/relay/...` is compared
  before and after the package split
- **THEN** coverage does not decrease, because behaviour is unchanged

#### Scenario: Route label display survives package split

- **WHEN** route runtime stores the configured-route marker `__routes__` or an
  override marker beginning with `__override__:` in a relay's agent-mix label
- **THEN** `relay.FormatMixLabel` renders the same operator-facing text as before
  (`configured routes`, the override specs, or `(override)` for an empty override),
  the stored marker values are unchanged, and no exported route-label constants or
  helpers are added

### Requirement: Responsibility-named decomposition

Within `internal/relay/runner`, the former `runner.go` SHALL be decomposed so each
file answers one architectural question and every symbol has exactly one home,
with no catch-all/`misc` file. `runner.go` SHALL retain only the top-level relay
flow (the `Runner` type, its construction, and the relay-loop skeleton). The two
largest functions (`Run`, `runOne`) SHALL be decomposed into named private steps.
Files SHALL use bare responsibility names. Enforcement of file-size and
import-boundary budgets is out of scope here and is owned by the
`add-architecture-guardrails` change.

#### Scenario: runner.go no longer mixes concerns

- **WHEN** `internal/relay/runner/runner.go` is read after the change
- **THEN** it explains the top-level relay flow and no longer contains the
  terminal/display, telemetry-assembly, action-loop, liveness, task/prompt, git,
  final-snippet, laps/progress-validation, or handoff-only helper clusters

#### Scenario: No single function remains a hazard

- **WHEN** the relay/run lifecycle is inspected after the change
- **THEN** `Run` and `runOne` each delegate to named private step-methods, and no
  single function approaches the pre-change scale of `runOne` (~1,200 lines)

#### Scenario: Every symbol has one home

- **WHEN** the `internal/relay/runner` files are inventoried
- **THEN** each former `runner.go` symbol lives in exactly one responsibility-named
  file, and no `misc`/`helpers` catch-all production file exists

### Requirement: Run/try loop decomposed into per-phase files

The run/try orchestration core in `internal/relay/runner` SHALL be decomposed
into responsibility-named files in the same package, one per run/try phase,
behind short index functions. `run_one.go` SHALL retain only `Runner.runOne`
and the run-state constructors, with each named phase
(prepare, monitored attempt, reconcile, classify, record, cancelled-attempt
record, retry-decide, handoff-continuation, finalize) living in its own file.
`route_runtime.go` SHALL retain only the `routeRuntime` type, its accessors,
and its error type, with construction/entry-resolution, selection/wait,
recovery-signal sync, and bench/probation helpers in their own named files.
`relay_steps.go` SHALL retain only the relay-level start/resume, span, and
scoped-message index, with route-wait/fallback, run-progress/resilience
application, and summary/tally in their own named files. Every symbol SHALL
have exactly one home and no `misc`/`helpers` catch-all production file SHALL
exist. Functions SHALL move verbatim, except that a phase body deep enough to
hide multiple sub-steps (at baseline, `classifyAttemptOutcome` and
`recordAttemptOutcome`) SHALL be decomposed into named unexported
package-local helpers within its phase file, preserving statement order, error
strings, telemetry fields, and control flow.

#### Scenario: run_one.go is an index, not a body

- **WHEN** `internal/relay/runner/run_one.go` is read after the change
- **THEN** it contains `Runner.runOne` and the run-state constructors only,
  each run/try phase lives in a responsibility-named sibling file, and no
  phase body inlined in the index file remains

#### Scenario: Deep phase bodies expose named sub-steps

- **WHEN** the classify and record phase files are read after the change
- **THEN** `classifyAttemptOutcome` and `recordAttemptOutcome` each delegate to
  named unexported helpers in the same file, and no single function in the
  package approaches the pre-change ~260-line phase-body scale

#### Scenario: Verbatim relocation preserved

- **WHEN** the diff of the change is reviewed
- **THEN** every relocated function is moved whole with unchanged signature,
  error strings, telemetry fields, and control flow, and the only new
  functions are the named unexported sub-step helpers extracted from the deep
  phase bodies

### Requirement: Runner grandfather cap ratcheted away

After the decomposition lands, the `tools/archguard` grandfather baseline SHALL
be regenerated so the `internal/relay/runner/run_one.go` production entry is
removed, and no production file in `internal/relay/runner` SHALL require a
grandfather entry. Runner `_test.go` grandfather entries SHALL remain unchanged
(owned by `decompose-large-test-files`).

#### Scenario: Flagship production cap removed

- **WHEN** `go run ./tools/archguard --report` regenerates the baseline after
  the split and `go run ./tools/archguard --ci` runs
- **THEN** no `internal/relay/runner` production file appears in the
  grandfather map, the runner test-file entries are untouched, and `--ci`
  exits 0

