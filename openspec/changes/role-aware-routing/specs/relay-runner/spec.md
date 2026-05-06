## MODIFIED Requirements

### Requirement: Agent mix cycling
The system SHALL select the agent for each iteration via the `quota-scheduler` capability operating on the active route. The active route is selected per the `role-routing` capability (priority: `--agent` override > lap `assignee` > `default`). The legacy v0.2.x `--mix` flag SHALL continue to work as a synonym for `--agent` (a single override roster); when only `--mix` is supplied, rally constructs an override roster from the mix entries and treats it as the active route for every iteration. The configured `[providers]` shortcuts (v0.5.0) and quota syntax (`quota-scheduler`) apply uniformly to mix and routes.

#### Scenario: Iteration uses scheduler against active route
- **WHEN** a run is about to start within a relay
- **THEN** the system SHALL determine the active route per `role-routing` and SHALL ask `quota-scheduler` for the next agent selection from that route

#### Scenario: Legacy --mix maps onto override roster
- **WHEN** `rally relay --mix "claude,codex,op:z"` is supplied (no `--agent`, no `[routes]`)
- **THEN** rally SHALL construct an override roster from the mix entries and SHALL use it as the active route for every iteration; per-lap routing SHALL be skipped

#### Scenario: Both --agent and --mix supplied
- **WHEN** both `--agent` and `--mix` are present on the same `rally relay` invocation
- **THEN** `--agent` SHALL take precedence and `--mix` SHALL be ignored with a warning

## ADDED Requirements

### Requirement: Prompt assembly appends role-instruction file
The system SHALL extend the prompt-building path to invoke the `role-instruction-loader` capability when an active lap carries an `assignee`. The loaded content SHALL be inserted between the base rally instructions and the lap body. When no `assignee` is set or no matching file exists, the prompt SHALL be assembled without role-specific instructions.

#### Scenario: Prompt with role-specific instructions
- **WHEN** the active lap has `assignee: SENIOR` and `.rally/agents/SENIOR.md` exists
- **THEN** the assembled prompt SHALL contain the rally base instructions, then `.rally/agents/SENIOR.md` contents, then the lap body, in that order

#### Scenario: Prompt without role-specific instructions
- **WHEN** the active lap has no `assignee` (or the assignee has no matching file)
- **THEN** the assembled prompt SHALL contain the rally base instructions and the lap body with no role-specific section

### Requirement: Per-iteration scheduler hooks for agent state
The system SHALL emit `onAgentFailed(entry, reason)` and `onAgentRecovered(entry)` events from the relay-runner to the scheduler whenever the executor reports a failure or a recovery signal. These hooks SHALL be the only mechanism by which the scheduler updates its exhausted/frozen flags. v0.7.0 layers freeze detection on top by emitting these same hooks from active monitoring rather than only from the retry-budget-exhausted path.

#### Scenario: Retry-budget exhaustion fires onAgentFailed
- **WHEN** a try fails its full retry budget
- **THEN** the relay-runner SHALL invoke `onAgentFailed(currentEntry, "retry-budget-exhausted")` before advancing to the next iteration

#### Scenario: Cooldown signal fires onAgentFailed
- **WHEN** the executor returns a known rate-limit / cooldown error pattern (matched per the v0.7.0 error-classification table when present, or a hardcoded set in v0.6.0)
- **THEN** the relay-runner SHALL invoke `onAgentFailed(currentEntry, "rate-limit")` and the scheduler SHALL mark the entry frozen for this cycle
