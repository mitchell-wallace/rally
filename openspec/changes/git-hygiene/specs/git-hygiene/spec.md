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

### Requirement: Lap-boundary commit instruction
The system SHALL instruct the agent to commit its work at every lap boundary via the
`laps done` / `laps handoff` hook script wrapup output. The instructed commit message
SHALL be `<lap-description>: done` when the lap is completed and
`<lap-description>: in progress (handoff)` when the lap is handed off. This
instruction is advisory and relies on agent compliance; the work is still captured
by rally's finalization auto-commit if the agent does not commit.

#### Scenario: Done boundary commit instruction
- **WHEN** the `laps done` hook script's wrapup output is generated
- **THEN** the output SHALL instruct the agent to commit with `<lap-description>: done`

#### Scenario: Handoff boundary commit instruction
- **WHEN** the `laps handoff` hook script's wrapup output is generated
- **THEN** the output SHALL instruct the agent to commit with `<lap-description>: in progress (handoff)`

### Requirement: Leftover-work commit guidance
The system SHALL detect uncommitted changes at the start of a run and, when such
changes are present, SHALL instruct the agent to commit them first before beginning
its assigned work. When the working tree is clean, the system SHALL omit this
guidance.

#### Scenario: Dirty working tree at run start
- **WHEN** a run begins and `git status --porcelain` reports a dirty working tree
- **THEN** the initial prompt SHALL instruct the agent to commit the uncommitted changes before proceeding with its assigned work

#### Scenario: Clean working tree at run start
- **WHEN** a run begins and `git status --porcelain` reports a clean working tree
- **THEN** the initial prompt SHALL NOT include leftover-work commit guidance

### Requirement: Folded state commit
The system SHALL NOT emit a standalone `rally: update state` commit in the common path.
The `summary.jsonl` append SHALL be folded into the run's work commit, which already
stages the working tree. A "rally-authored commit" is any commit whose author matches
the `GitUserFallbackConfig` identity. If a state-only change is unavoidable (e.g. a
run that produced no code), the following fallback SHALL apply:

- If HEAD is a rally-authored commit: amend HEAD with the new state, reusing the
  existing commit message.
- If HEAD is not rally-authored: create a single `rally: update state` commit.

This ensures no stacking of consecutive rally state commits.

#### Scenario: State rides the work commit
- **WHEN** a run produces code and finalizes
- **THEN** the resulting work commit SHALL include the `summary.jsonl` append and the system SHALL NOT create a separate `rally: update state` commit

#### Scenario: No-code run with rally-authored HEAD amends
- **WHEN** a run produces no code, state-only changes remain, and HEAD is a rally-authored commit
- **THEN** the system SHALL amend HEAD with the new state, reusing the existing commit message

#### Scenario: No-code run with non-rally HEAD creates state commit
- **WHEN** a run produces no code, state-only changes remain, and HEAD is not a rally-authored commit
- **THEN** the system SHALL create a single `rally: update state` commit (not stacking multiple)
