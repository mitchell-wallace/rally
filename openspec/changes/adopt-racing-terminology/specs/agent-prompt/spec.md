## MODIFIED Requirements

### Requirement: Composed agent prompt
The system SHALL compose a full agent prompt from shared `general/` snippets, a role-specific snippet slot, and the existing task context. Agent-facing prompt terminology SHALL describe the hierarchy as `relay > outing > try` and SHALL call the selected harness+model a driver. The prompt SHALL preserve the existing executor prompt contract and SHALL NOT introduce role-taxonomy changes owned by `rename-rally-roles`.

#### Scenario: Prompt uses outing and driver terminology
- **WHEN** a prompt is composed for a lap or free-run outing
- **THEN** hierarchy prose SHALL use `outing` for the middle execution entity
- **AND** harness+model route-entry prose SHALL use `driver`

#### Scenario: Role taxonomy remains out of scope
- **WHEN** prompt text refers to assignees such as junior, senior, ui, verify, or pending renamed roles
- **THEN** this change SHALL NOT rename those roles

### Requirement: Handoff-only prompt snippet
The system SHALL provide a handoff-only prompt used solely for the bounded handoff-only resume after outing-budget exhaustion. The snippet SHALL forbid continuing implementation and SHALL direct the agent to summarize the blocker, hypotheses tried, evidence gathered, changed files, and the next decision, then call `laps handoff` followed by `laps wrapup`.

#### Scenario: Handoff-only prompt forbids implementation
- **WHEN** an outing enters handoff-only resume
- **THEN** the handoff-only prompt SHALL tell the agent not to continue implementation
