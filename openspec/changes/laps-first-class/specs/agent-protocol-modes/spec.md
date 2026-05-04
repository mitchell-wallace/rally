## ADDED Requirements

### Requirement: Two distinct prompt-template modes
The system SHALL maintain two prompt-template variants selected by the `LapsEnabled` bool determined at startup. The laps-enabled variant SHALL teach `laps done` and `laps handoff` as the only initial run-exit conditions; `laps wrapup` SHALL NOT appear in the initial prompt. The no-backend variant SHALL teach `rally progress --summary "..." --followup "..."` as the run-end action.

#### Scenario: Laps-enabled initial instructions
- **WHEN** rally builds the prompt template with laps enabled
- **THEN** the prompt SHALL list `laps done` (when the lap is finished) and `laps handoff` (when blocked) as the run-exit conditions, and SHALL NOT mention `laps wrapup`

#### Scenario: No-backend initial instructions
- **WHEN** rally builds the prompt template with laps disabled
- **THEN** the prompt SHALL document `rally progress --summary "..." --followup "..."` as the run-end action and SHALL explicitly note that calling `rally` from the agent is the documented exception in this mode

### Requirement: `laps wrapup` taught contextually via `laps done` passback
When laps is enabled, the agent SHALL learn `laps wrapup` from the passback output of the `laps done` after-hook (per `laps-hook-translator` capability), not from the initial prompt template. The hook's stdout SHALL include the exact `laps wrapup --summary "..." --followup "..."` syntax.

#### Scenario: Wrapup syntax appears only after first laps done
- **WHEN** the agent has not yet called `laps done` in a laps-enabled run
- **THEN** the agent's prompt context SHALL NOT contain the string `laps wrapup`

#### Scenario: Wrapup syntax delivered after first laps done
- **WHEN** the agent calls `laps done` for the first time in a laps-enabled run
- **THEN** the after-hook passback output (visible to the agent) SHALL include the `laps wrapup` syntax

### Requirement: `laps handoff` teaches wrapup with handoff context
When laps is enabled, the `laps handoff` hook's stdout SHALL teach the agent to call `laps wrapup` with a summary describing why the handoff is needed and followups that will become new laps at the queue head. The agent learns the handoff-specific wrapup instructions from this passback, not from the initial prompt.

#### Scenario: Handoff passback teaches wrapup
- **WHEN** the agent calls `laps handoff`
- **THEN** the hook's passback output SHALL instruct the agent to call `laps wrapup --summary "..." --followup "..."` and SHALL explain that each followup will be created as a new lap at the head of the queue

### Requirement: `rally progress` visibility follows mode
The system SHALL gate `rally progress` visibility by whether laps is enabled. When enabled, the subcommand SHALL be invokable only by hook scripts (the `--record-lap`, `--complete`, `--handoff` flag forms) and SHALL NOT appear in the public CLI help shown to agents. When disabled, `rally progress --summary --followup` SHALL be exposed as a top-level public subcommand.

#### Scenario: Laps-enabled help text
- **WHEN** the agent runs `rally --help` (or rally's prompt presents available commands) with laps enabled
- **THEN** `rally progress` SHALL NOT be listed among the documented subcommands

#### Scenario: No-backend help text
- **WHEN** the agent runs `rally --help` with laps disabled
- **THEN** `rally progress` SHALL be listed and documented with `--summary` and `--followup` flags
