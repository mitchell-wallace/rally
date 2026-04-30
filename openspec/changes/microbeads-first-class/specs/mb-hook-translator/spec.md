## ADDED Requirements

### Requirement: `mb done` after-hook records bead and prompts wrapup
The system SHALL register a `passback: true` after-hook on `mb done` that records the just-closed bead ID against the active run's progress entry and prints next-step `mb wrapup` instructions back to the agent via stdout. The instructions SHALL include the exact `mb wrapup --summary "..." --followup "..."` syntax.

#### Scenario: Agent calls mb done
- **WHEN** the agent invokes `mb done <id>` during a microbeads-backed run
- **THEN** the after-hook SHALL invoke `rally progress --record-bead <id>` internally to accumulate the ID and SHALL print to stdout (passback) the line:

  `✓ Marked done. Wrap up this run before exiting:`

  followed by the `mb wrapup` syntax instruction

#### Scenario: Multiple mb done calls in one run
- **WHEN** the agent calls `mb done` more than once within a single run
- **THEN** each invocation SHALL accumulate its bead ID against the same active run; the wrapup reminder SHALL be printed each time

### Requirement: `mb wrapup` hook-only command
The system SHALL register `mb wrapup` as a hook-only command (per microbeads SPEC §Hooks) that the agent invokes with `--summary "<one-line>"` and zero or more `--followup "<text>"` arguments. The hook script SHALL forward `$@` to `rally progress --finalise`. The script SHALL print `Progress recorded.` to the agent on success.

#### Scenario: Wrapup with summary and followups
- **WHEN** the agent invokes `mb wrapup --summary "Did X" --followup "Check Y" --followup "Do Z"`
- **THEN** the hook script SHALL invoke `rally progress --finalise --summary "Did X" --followup "Check Y" --followup "Do Z"` and SHALL print `Progress recorded.` on success

#### Scenario: Wrapup without summary
- **WHEN** the agent invokes `mb wrapup` with no `--summary`
- **THEN** `rally progress --finalise` SHALL exit non-zero with an error message; the hook script SHALL surface the error to the agent without writing a progress entry

### Requirement: `mb handoff` two-call protocol
The system SHALL register `mb handoff` as a hook-only command that supports a two-call protocol: a first call without (or with empty) `--reason` flips a per-run handoff flag in `.rally/run-state.json` and prints handoff-tuned instructions; a second call with `--reason` and zero or more `--followup` arguments creates one bead per `--followup` at the queue head via `mb add head`, writes the handoff entry through `rally progress --handoff`, and clears the flag.

#### Scenario: First call with no args
- **WHEN** the agent invokes `mb handoff` with no further arguments
- **THEN** the hook script SHALL set the handoff flag in `.rally/run-state.json` and SHALL print the handoff-tuned instructions including the exact second-call syntax (`mb handoff --reason "..." --followup "..."`)

#### Scenario: Second call with reason and followups
- **WHEN** the agent invokes `mb handoff --reason "needs auth team review" --followup "investigate token rotation"` after the flag has been set
- **THEN** the hook script SHALL invoke `mb add head --title "investigate token rotation"` for each `--followup`, SHALL invoke `rally progress --handoff --reason "needs auth team review" --followup "investigate token rotation"`, SHALL clear the flag, and SHALL print the confirmation `Handoff recorded. Created N follow-up bead(s) at queue head. Original bead remains open for next run.`

#### Scenario: Followups go to queue head
- **WHEN** the second `mb handoff` call processes any number of `--followup` arguments
- **THEN** each follow-up SHALL be inserted via `mb add head` (not `mb add tail`), so blockers are addressed before the original bead is retried

### Requirement: Hook scripts forward `$@` for parsing
The hook scripts SHALL be thin shell layers that forward `$@` to the appropriate `rally progress` subcommand. Argument parsing SHALL be performed in rally (Go), not in the shell layer, so shell-quoting fragility is bounded to a single `exec` call per script.

#### Scenario: Quoted arg preserved through forwarding
- **WHEN** the agent invokes `mb wrapup --summary "Multi word summary"` and the hook script forwards `$@`
- **THEN** rally SHALL receive `Multi word summary` as a single argument value for `--summary`
