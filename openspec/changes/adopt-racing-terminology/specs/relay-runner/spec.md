## MODIFIED Requirements

### Requirement: Naming - try, outing, relay
The system SHALL use three-tier naming for execution units:
- **Try**: one invocation of an agent CLI, regardless of outcome. The atomic unit. Each try produces a `TryResult`.
- **Outing**: one driver assigned to one lap or free-run unit. An outing consumes distinct outing-level context and owns its retry budget. If the driver cannot finish and the scheduler changes to another driver, the follow-up work is a new outing.
- **Relay**: a campaign of outings processing a queue of laps or free-run iterations.

A **driver** is the harness+model route entry selected to perform an outing. The orchestrator package `internal/relay/runner` and its `Runner` type SHALL keep their names.

#### Scenario: Try fails within an outing
- **WHEN** a try fails within an outing and retry budget remains
- **THEN** the system SHALL retry within the same outing

#### Scenario: Driver change creates a new outing
- **WHEN** the current driver cannot finish and routing falls back to a different driver
- **THEN** the system SHALL represent the follow-up work as a new outing

#### Scenario: Orchestrator runner names remain
- **WHEN** code refers to `internal/relay/runner` or the orchestrator `Runner` type
- **THEN** those names SHALL remain valid and SHALL NOT be renamed by this change

### Requirement: Try execution
The system SHALL execute each try by writing `.rally/current_task.md`, recording HEAD before the try, invoking the selected driver's executor, tracking commit hash, recording the `TryResult`, and auto-committing if needed. Go verb names such as `Run()`, cobra `RunE`, and verb-sense prose MAY continue to use "run" where they do not name the outing entity.

#### Scenario: Try result recorded
- **WHEN** a try completes
- **THEN** the result SHALL be recorded under the current outing identity
- **AND** the selected harness+model SHALL be described internally as the driver

#### Scenario: Go Run methods remain idiomatic
- **WHEN** a type exposes a Go-idiom `Run` method or a cobra `RunE` callback
- **THEN** the method name MAY remain unchanged
