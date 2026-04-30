## ADDED Requirements

### Requirement: Two distinct prompt-template modes
The system SHALL maintain two prompt-template variants selected by the bead-tracker mode determined at startup. The microbeads-backed variant SHALL teach `mb done <id>` and `mb handoff` as the only initial run-exit conditions; `mb wrapup` SHALL NOT appear in the initial prompt. The no-backend variant SHALL teach `rally progress --summary "..." --followup "..."` as the run-end action.

#### Scenario: Microbeads-backed initial instructions
- **WHEN** rally builds the prompt template in microbeads-backed mode
- **THEN** the prompt SHALL list `mb done <id>` (when the bead is finished) and `mb handoff` (when blocked) as the run-exit conditions, and SHALL NOT mention `mb wrapup`

#### Scenario: No-backend initial instructions
- **WHEN** rally builds the prompt template in no-backend mode
- **THEN** the prompt SHALL document `rally progress --summary "..." --followup "..."` as the run-end action and SHALL explicitly note that calling `rally` from the agent is the documented exception in this mode

### Requirement: `mb wrapup` taught contextually via `mb done` passback
In microbeads-backed mode, the agent SHALL learn `mb wrapup` from the passback output of the `mb done` after-hook (per `mb-hook-translator` capability), not from the initial prompt template. The hook's stdout SHALL include the exact `mb wrapup --summary "..." --followup "..."` syntax.

#### Scenario: Wrapup syntax appears only after first mb done
- **WHEN** the agent has not yet called `mb done` in a microbeads-backed run
- **THEN** the agent's prompt context SHALL NOT contain the string `mb wrapup`

#### Scenario: Wrapup syntax delivered after first mb done
- **WHEN** the agent calls `mb done` for the first time in a microbeads-backed run
- **THEN** the after-hook passback output (visible to the agent) SHALL include the `mb wrapup` syntax

### Requirement: `rally progress` visibility follows mode
The system SHALL gate `rally progress` visibility by mode. In microbeads-backed mode, the subcommand SHALL be invokable only by hook scripts (the `--record-bead`, `--finalise`, `--handoff` flag forms) and SHALL NOT appear in the public CLI help shown to agents. In no-backend mode, `rally progress --summary --followup` SHALL be exposed as a top-level public subcommand.

#### Scenario: Microbeads-backed help text
- **WHEN** the agent runs `rally --help` (or rally's prompt presents available commands) in microbeads-backed mode
- **THEN** `rally progress` SHALL NOT be listed among the documented subcommands

#### Scenario: No-backend help text
- **WHEN** the agent runs `rally --help` in no-backend mode
- **THEN** `rally progress` SHALL be listed and documented with `--summary` and `--followup` flags
