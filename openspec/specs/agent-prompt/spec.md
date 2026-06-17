# agent-prompt Specification

## Purpose
TBD - created by archiving change rally-083-polish. Update Purpose after archive.
## Requirements
### Requirement: Embedded agent prompt sources
The system SHALL store agent-facing prompt content as embedded `.md` files compiled into the binary via `go:embed`, organized under an `agent_prompt` package with a `general/` subfolder for shared snippets and a `roles/` subfolder for per-role snippets. Prompt content authored for the *user* (as opposed to the agent) SHALL live in a separate `user_prompt` package; the package naming distinguishes who is being prompted.

#### Scenario: Role snippet embedded
- **WHEN** the system needs the default instructions for a role (e.g. `junior`, `senior`, `ui`, `verify`)
- **THEN** it SHALL read them from the embedded `roles/<role>.md` content without requiring any on-disk file

#### Scenario: Shared snippet embedded
- **WHEN** the system composes an agent prompt
- **THEN** it SHALL include the embedded `general/` snippets applicable to all roles

### Requirement: Composed agent prompt
The system SHALL compose a full agent prompt from shared `general/` snippets, a role-specific snippet slot, and the existing task context. Shared finalize and headless guidance SHALL come from `general/` so that embedded role snippets contain only role-specific guidance and are not required to repeat shared instructions. The composition SHALL preserve the existing executor prompt contract, including explicit `RunOptions.Prompt` override semantics and the normal task-context sections for project instructions, role/persona guidance, task name/requirements, inbox or relay messages, previous summary, recent try context, and other existing RunOptions-derived prompt content.

#### Scenario: Prompt composition order
- **WHEN** an agent prompt is built for an assigned role
- **THEN** the prompt SHALL combine the shared `general/` guidance, the role snippet, and the task context into a single prompt

#### Scenario: Prompt composition template
- **WHEN** an agent prompt is composed
- **THEN** the shared `general/` snippets SHALL always be included
- **AND** the role slot SHALL be filled by the on-disk role override when present or by the embedded role default otherwise
- **AND** the existing RunOptions-derived task context SHALL be appended after the reusable prompt snippets

#### Scenario: Explicit prompt override preserved
- **WHEN** `RunOptions.Prompt` is explicitly provided
- **THEN** the system SHALL preserve the existing override semantics and SHALL NOT accidentally prepend or append the reusable agent-prompt template unless the existing executor prompt contract already does so

#### Scenario: Role snippets are role-specific only
- **WHEN** a role snippet is authored
- **THEN** it SHALL contain only role-specific guidance and SHALL rely on `general/` for the shared finalize and headless guidance

### Requirement: Role defaults with on-disk override
Embedded role snippets SHALL serve as the default role instructions. When an on-disk `.rally/agents/<role>.md` file exists, it SHALL override only the embedded default for that role slot. Repositories without `.rally/agents/` files SHALL receive the embedded defaults. On-disk custom role prompts are operator-owned and SHALL NOT be rewritten or migrated automatically.

#### Scenario: On-disk override present
- **WHEN** `.rally/agents/<role>.md` exists for the assigned role
- **THEN** the system SHALL use the on-disk content instead of the embedded default for that role slot
- **AND** the composed prompt SHALL still include shared `general/` finalize and headless snippets

#### Scenario: No on-disk file
- **WHEN** no `.rally/agents/<role>.md` exists for the assigned role
- **THEN** the system SHALL use the embedded default role snippet

### Requirement: Finalize guidance snippet
The system SHALL provide a shared `general/finalize.md` snippet instructing agents to commit their work, call `laps done` when the full scope is complete or `laps handoff` when blocked, and call `laps wrapup` to record progress. The guidance to call `laps wrapup` SHALL be present up front in the composed prompt (in addition to any hook-triggered reminder after `laps done`/`laps handoff`).

#### Scenario: Finalize guidance present in every role prompt
- **WHEN** an agent prompt is composed for any role
- **THEN** it SHALL include the shared finalize guidance covering commit, `laps done`/`laps handoff`, and `laps wrapup`

### Requirement: Headless operation guidance
The system SHALL provide a shared `general/headless.md` snippet informing agents that Rally runs in a headless / non-interactive mode that does not support inline confirmations with the user, and that the best reference for the user's intent is the planning documents the laps reference (e.g. an OpenSpec change).

#### Scenario: Headless guidance present in every role prompt
- **WHEN** an agent prompt is composed for any role
- **THEN** it SHALL include guidance that the session is non-interactive and that intent should be taken from referenced planning documents rather than by asking the user

### Requirement: Role prompt diagnostics
The system SHALL extend `rally routes check` with role-prompt diagnostics that help operators compare custom role prompts with embedded shared guidance without modifying operator-owned files.

#### Scenario: Detected roles listed
- **WHEN** `rally routes check` runs
- **THEN** it SHALL list detected roles and an approximate token count for each role prompt

#### Scenario: Custom prompt may overlap shared guidance
- **WHEN** an on-disk `.rally/agents/<role>.md` file references `laps done`, `laps handoff`, `laps wrapup`, or `headless`
- **THEN** `rally routes check` SHALL flag the potential overlap as an advisory diagnostic
- **AND** it SHALL print the embedded `general/finalize.md` and `general/headless.md` snippets for comparison
- **AND** it SHALL leave the custom role prompt unchanged
- **AND** the advisory diagnostic SHALL NOT make an otherwise valid routes check fail

### Requirement: RECOVERY role prompt snippet
The system SHALL embed a `recovery` role prompt snippet (`roles/recovery.md`) resolvable by the same case-insensitive `Role(name)` loader and overridable by an on-disk `.rally/agents/recovery.md`, as for every other role. The embedded RECOVERY snippet SHALL state that the role reconciles incomplete or failed dirty state and continues the task; SHALL contain the five-way classification contract (`continue`, `discard`, `course_correct`, `repair_plan`, `needs_user`) with each option's meaning; SHALL instruct the agent to classify first and then act (never stop at diagnosis unless `needs_user`); SHALL permit adding follow-up laps as a containment strategy without using them to avoid recovery; and SHALL instruct the agent to record its classification with `laps wrapup --classification <value>` and finalize with `laps done`/`laps handoff` + `laps wrapup`. The snippet SHALL remain OpenSpec-agnostic.

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
- **WHEN** a prompt is composed for a run whose effective assignee/prompt role is `recovery`
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
The system SHALL provide a handoff-only prompt (a shared `general/` snippet) used solely for the bounded handoff-only resume after run-budget exhaustion. The snippet SHALL forbid continuing implementation and SHALL direct the agent to summarize the blocker, hypotheses tried, evidence gathered, changed files, and the next decision, then call `laps handoff` followed by `laps wrapup`.

#### Scenario: Handoff-only prompt forbids implementation
- **WHEN** the handoff-only prompt is used for a bounded resume
- **THEN** it SHALL instruct the agent not to continue implementation and to finalize via `laps handoff` + `laps wrapup`

