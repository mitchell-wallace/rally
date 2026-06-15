## ADDED Requirements

### Requirement: RECOVERY role prompt snippet
The system SHALL embed a `recovery` role prompt snippet (`roles/recovery.md`) resolvable by the same case-insensitive `Role(name)` loader and overridable by an on-disk `.rally/agents/recovery.md`, as for every other role. The embedded RECOVERY snippet SHALL state that the role reconciles incomplete or failed dirty state and continues the task; SHALL contain the five-way classification contract (`continue`, `discard`, `course_correct`, `repair_plan`, `needs_user`) with each option's meaning; SHALL instruct the agent to classify first and then act (never stop at diagnosis unless `needs_user`); SHALL permit adding follow-up laps as a containment strategy without using them to avoid recovery; and SHALL instruct the agent to record its classification and finalize with `laps done`/`laps handoff` + `laps wrapup`. The snippet SHALL remain OpenSpec-agnostic.

#### Scenario: Recovery role resolves like other roles
- **WHEN** the `recovery` role prompt is requested
- **THEN** the loader SHALL return the embedded `roles/recovery.md` content, or the on-disk `.rally/agents/recovery.md` override when present

#### Scenario: Recovery snippet carries the classification contract
- **WHEN** the embedded RECOVERY snippet is composed into a prompt
- **THEN** it SHALL include all five classifications with their meanings and direct the agent to act on the classification unless it is `needs_user`

#### Scenario: Recovery snippet stays OpenSpec-agnostic
- **WHEN** the generic RECOVERY role document is composed
- **THEN** it SHALL NOT contain OpenSpec-specific instructions (those are injected per-lap by `prepare-laps` only when a lap has a related change)

#### Scenario: Classification instruction appears only on RECOVERY runs
- **WHEN** a prompt is composed for a run whose assignee resolves to `recovery`
- **THEN** the composed prompt SHALL reference recording the recovery classification (the `continue`/`discard`/`course_correct`/`repair_plan`/`needs_user` field)

#### Scenario: Non-recovery prompts omit the classification instruction
- **WHEN** a prompt is composed for any non-recovery role (JUNIOR, SENIOR, UI, VERIFY)
- **THEN** the composed prompt SHALL NOT reference the recovery classification field, because that instruction lives only in the role-scoped `roles/recovery.md` snippet and not in shared/general snippets

### Requirement: Voluntary handoff guidance for implementation roles
The composed prompt for implementation roles (e.g. JUNIOR, SENIOR, UI) SHALL include explicit guidance that an agent stuck on the same bug or failing test, after five serious debugging iterations without real progress, SHALL stop grinding and use `laps handoff` then `laps wrapup`, explaining the blocker, the hypotheses tried, the evidence gathered, the changed files, and what a fresh agent should decide next. The guidance SHALL define a "debugging iteration" as one loop of: (1) form a hypothesis, (2) inspect/change/run a check, (3) observe the failure, (4) choose the next hypothesis. The guidance SHALL frame the trigger as the agent's own judgment of being honestly stuck (stubborn issue, cascading failures, or symptom-patching without root-cause progress), not as a Rally-measured signal such as lack of diff movement.

#### Scenario: Implementation prompt includes the five-iteration rule
- **WHEN** a prompt is composed for an implementation role
- **THEN** it SHALL include the five-iteration voluntary-handoff guidance and the definition of a debugging iteration

#### Scenario: Handoff guidance is judgment-based, not metric-based
- **WHEN** the voluntary-handoff guidance is composed
- **THEN** it SHALL frame the trigger as the agent's own honest assessment of being stuck and SHALL NOT instruct the agent that Rally measures diff movement or transcript activity to enforce it

### Requirement: Handoff-only prompt snippet
The system SHALL provide a handoff-only prompt (a shared `general/` snippet) used solely for the bounded handoff-only resume after a per-try timeout. The snippet SHALL forbid continuing implementation and SHALL direct the agent to summarize the blocker, hypotheses tried, evidence gathered, changed files, and the next decision, then call `laps handoff` followed by `laps wrapup`.

#### Scenario: Handoff-only prompt forbids implementation
- **WHEN** the handoff-only prompt is used for a bounded resume
- **THEN** it SHALL instruct the agent not to continue implementation and to finalize via `laps handoff` + `laps wrapup`
