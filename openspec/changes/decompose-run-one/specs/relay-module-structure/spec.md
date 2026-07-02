## ADDED Requirements

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
