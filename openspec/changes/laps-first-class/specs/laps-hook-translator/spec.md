## ADDED Requirements

### Requirement: `laps done` after-hook records lap and prompts wrapup
The system SHALL register a `passback: true` after-hook on `laps done` that records the just-closed lap ID against the active run's progress entry and prints next-step `laps wrapup` instructions back to the agent via stdout. The instructions SHALL include the exact `laps wrapup --summary "..." --followup "..."` syntax.

#### Scenario: Agent calls laps done
- **WHEN** the agent invokes `laps done` during a laps-enabled run
- **THEN** the after-hook SHALL invoke `rally progress --record-lap <id>` internally to accumulate the ID and SHALL print to stdout (passback) the line:

  `Marked done. Wrap up this run before exiting:`

  followed by the `laps wrapup` syntax instruction

#### Scenario: Multiple laps done calls in one run
- **WHEN** the agent calls `laps done` more than once within a single run
- **THEN** each invocation SHALL accumulate its lap ID against the same active run; the wrapup reminder SHALL be printed each time

### Requirement: `laps handoff` hook signals handoff intent and directs to wrapup
The system SHALL register `laps handoff` as a hook-only command (per laps SPEC §Hooks) that signals the agent intends to hand off the current task. The hook script SHALL set `RALLY_HANDOFF_STATE=1` environment variable (persisted in `.rally/run-state.json`) and SHALL print handoff-tuned instructions directing the agent to call `laps wrapup` with `--summary` and `--followup` arguments describing what needs to happen next.

#### Scenario: Agent calls laps handoff
- **WHEN** the agent invokes `laps handoff` during a laps-enabled run
- **THEN** the hook script SHALL set the handoff state to 1, and SHALL print instructions including the exact `laps wrapup --summary "..." --followup "..."` syntax, explaining that followups will become new laps at the queue head

### Requirement: `laps wrapup` hook-only command with handoff routing
The system SHALL register `laps wrapup` as a hook-only command (per laps SPEC §Hooks) that the agent invokes with `--summary "<one-line>"` and zero or more `--followup "<text>"` arguments. The hook script SHALL check `RALLY_HANDOFF_STATE`: if `0` or missing, it forwards `$@` to `rally progress --complete`; if `1`, it resets the state to `0` and forwards `$@` to `rally progress --handoff`. The script SHALL print `Progress recorded.` to the agent on success.

#### Scenario: Wrapup in normal (non-handoff) mode
- **WHEN** the agent invokes `laps wrapup --summary "Did X" --followup "Check Y"` and `RALLY_HANDOFF_STATE` is `0` or missing
- **THEN** the hook script SHALL invoke `rally progress --complete --summary "Did X" --followup "Check Y"` and SHALL print `Progress recorded.` on success

#### Scenario: Wrapup after handoff signal
- **WHEN** the agent invokes `laps wrapup --summary "Blocked on auth" --followup "Investigate token rotation"` and `RALLY_HANDOFF_STATE` is `1`
- **THEN** the hook script SHALL reset the state to `0`, SHALL invoke `rally progress --handoff --summary "Blocked on auth" --followup "Investigate token rotation"`, and SHALL print `Progress recorded.` on success

#### Scenario: Wrapup without summary
- **WHEN** the agent invokes `laps wrapup` with no `--summary`
- **THEN** `rally progress` SHALL exit non-zero with an error message; the hook script SHALL surface the error to the agent without writing a progress entry

### Requirement: Hook scripts forward `$@` for parsing
The hook scripts SHALL be thin shell layers that forward `$@` to the appropriate `rally progress` subcommand. Argument parsing SHALL be performed in rally (Go), not in the shell layer, so shell-quoting fragility is bounded to a single `exec` call per script.

#### Scenario: Quoted arg preserved through forwarding
- **WHEN** the agent invokes `laps wrapup --summary "Multi word summary"` and the hook script forwards `$@`
- **THEN** rally SHALL receive `Multi word summary` as a single argument value for `--summary`
