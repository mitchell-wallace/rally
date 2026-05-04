## ADDED Requirements

### Requirement: Two distinct prompt-template modes
The system SHALL maintain two prompt-template variants selected by the `MicrobeadsEnabled` bool determined at startup. The microbeads-enabled variant SHALL teach `mb done <id>` and `mb handoff` as the only initial run-exit conditions; `mb wrapup` SHALL NOT appear in the initial prompt. The no-backend variant SHALL teach `rally progress --summary "..." --followup "..."` as the run-end action.

#### Scenario: Microbeads-enabled initial instructions
- **WHEN** rally builds the prompt template with microbeads enabled
- **THEN** the prompt SHALL list `mb done <id>` (when the microbead is finished) and `mb handoff` (when blocked) as the run-exit conditions, and SHALL NOT mention `mb wrapup`

#### Scenario: No-backend initial instructions
- **WHEN** rally builds the prompt template with microbeads disabled
- **THEN** the prompt SHALL document `rally progress --summary "..." --followup "..."` as the run-end action and SHALL explicitly note that calling `rally` from the agent is the documented exception in this mode

### Requirement: `mb wrapup` taught contextually via `mb done` passback
When microbeads is enabled, the agent SHALL learn `mb wrapup` from the passback output of the `mb done` after-hook (per `mb-hook-translator` capability), not from the initial prompt template. The hook's stdout SHALL include the exact `mb wrapup --summary "..." --followup "..."` syntax.

#### Scenario: Wrapup syntax appears only after first mb done
- **WHEN** the agent has not yet called `mb done` in a microbeads-enabled run
- **THEN** the agent's prompt context SHALL NOT contain the string `mb wrapup`

#### Scenario: Wrapup syntax delivered after first mb done
- **WHEN** the agent calls `mb done` for the first time in a microbeads-enabled run
- **THEN** the after-hook passback output (visible to the agent) SHALL include the `mb wrapup` syntax

### Requirement: `mb handoff` teaches wrapup with handoff context
When microbeads is enabled, the `mb handoff` hook's stdout SHALL teach the agent to call `mb wrapup` with a summary describing why the handoff is needed and followups that will become new microbeads at the queue head. The agent learns the handoff-specific wrapup instructions from this passback, not from the initial prompt.

#### Scenario: Handoff passback teaches wrapup
- **WHEN** the agent calls `mb handoff`
- **THEN** the hook's passback output SHALL instruct the agent to call `mb wrapup --summary "..." --followup "..."` and SHALL explain that each followup will be created as a new microbead at the head of the queue

### Requirement: `rally progress` visibility follows mode
The system SHALL gate `rally progress` visibility by whether microbeads is enabled. When enabled, the subcommand SHALL be invokable only by hook scripts (the `--record-microbead`, `--complete`, `--handoff` flag forms) and SHALL NOT appear in the public CLI help shown to agents. When disabled, `rally progress --summary --followup` SHALL be exposed as a top-level public subcommand.

#### Scenario: Microbeads-enabled help text
- **WHEN** the agent runs `rally --help` (or rally's prompt presents available commands) with microbeads enabled
- **THEN** `rally progress` SHALL NOT be listed among the documented subcommands

#### Scenario: No-backend help text
- **WHEN** the agent runs `rally --help` with microbeads disabled
- **THEN** `rally progress` SHALL be listed and documented with `--summary` and `--followup` flags
