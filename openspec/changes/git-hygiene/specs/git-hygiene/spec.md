## ADDED Requirements

### Requirement: Setup auto-commit
The system SHALL commit the files created or modified by workspace setup. After
`rally init`, the system SHALL stage the tracked setup files and commit them with the
message `rally: initialize workspace`. After laps-hook installation, the system SHALL
stage the affected files and commit them with the message `rally: install laps hooks`.
Both commits SHALL use `--no-verify` and SHALL be made only when something is actually
staged. The system SHALL commit against the tracked-file set and gitignore declared by
the runtime data storage layout; it SHALL NOT redefine them.

#### Scenario: Init commits setup files
- **WHEN** `rally init` runs in a repository and creates or modifies tracked setup files
- **THEN** the system SHALL stage those files and create a single `rally: initialize workspace` commit

#### Scenario: Hook install commits hook files
- **WHEN** laps-hook installation creates or modifies tracked files
- **THEN** the system SHALL stage them and create a `rally: install laps hooks` commit

#### Scenario: Nothing to commit is a no-op
- **WHEN** init or hook install runs and no tracked files are created or modified
- **THEN** the system SHALL NOT create a commit

### Requirement: Lap-boundary commit
The system SHALL instruct the agent to commit its work at every lap boundary via the
`laps done` / `laps handoff` wrapup prompt. The instructed commit message SHALL be
`<lap-description>: done` when the lap is completed and
`<lap-description>: in progress (handoff)` when the lap is handed off.

#### Scenario: Done boundary commit instruction
- **WHEN** the wrapup prompt is built for a `laps done`
- **THEN** the prompt SHALL instruct the agent to commit with `<lap-description>: done`

#### Scenario: Handoff boundary commit instruction
- **WHEN** the wrapup prompt is built for a `laps handoff`
- **THEN** the prompt SHALL instruct the agent to commit with `<lap-description>: in progress (handoff)`

### Requirement: Folded state commit
The system SHALL NOT emit a standalone `rally: update state` commit in the common path.
The `summary.jsonl` append SHALL be folded into the run's work commit, which already
stages the working tree. If a state-only commit is unavoidable (e.g. a run that
produced no code), the system SHALL amend it onto an immediately preceding rally state
commit by the same author rather than stacking a new commit.

#### Scenario: State rides the work commit
- **WHEN** a run produces code and finalizes
- **THEN** the resulting work commit SHALL include the `summary.jsonl` append and the system SHALL NOT create a separate `rally: update state` commit

#### Scenario: No-code run amends rather than stacks
- **WHEN** a run produces no code, a state-only commit would be emitted, and HEAD is already a rally state commit by the same author
- **THEN** the system SHALL amend the existing commit instead of stacking a new one
