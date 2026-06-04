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

