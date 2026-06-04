## MODIFIED Requirements

### Requirement: Incomplete failure class
The system SHALL classify a try as "incomplete" rather than "failed" when file changes
produced **by that try** were left uncommitted (dirty working tree) and the agent
neither finalized the lap (`laps done`) nor handed off (`laps handoff`). To attribute
changes to the try, the system SHALL snapshot the set of already-dirty paths at try
start and SHALL consider only the working-tree delta against that snapshot. Uncommitted
leftovers inherited from a prior failed try and left untouched SHALL NOT, on their own,
make a later try incomplete. An incomplete try SHALL have its auto-commit suppressed,
leaving changes uncommitted. The retry run SHALL inherit the uncommitted changes and
SHALL receive prompt guidance: "The last run was incomplete. Check any current git
changes, finish anything not done, verify correctness, commit when good, then run `laps
done`." An incomplete try SHALL be retried but SHALL NOT count toward the pause/freeze
resilience cascade.

#### Scenario: Agent produces file changes without finalizing
- **WHEN** a try produces file changes in the working tree but the agent does not call `laps done` or `laps handoff`
- **THEN** the system SHALL classify the try as incomplete, suppress auto-commit, retry the run with prompt guidance, and SHALL NOT call `PauseAgent` or `RecordHourlyFailure`

#### Scenario: No file changes and no finalization
- **WHEN** a try produces no file changes and the agent does not finalize
- **THEN** the system SHALL classify as a normal agent-class failure (retry-eligible, does not escalate)

#### Scenario: Inherited leftover changes do not trigger incomplete
- **WHEN** a try begins with a dirty working tree inherited from a prior failed try, and the try produces no new changes of its own and does not finalize
- **THEN** the system SHALL NOT classify the try as incomplete on the basis of those untouched leftovers

#### Scenario: Touching an inherited leftover attributes it to this try
- **WHEN** a try modifies, reverts, or commits a path that was already dirty at try start
- **THEN** that path SHALL count as a change produced by this try when classifying incompleteness
