## ADDED Requirements

### Requirement: Keyboard shortcuts for skip, pause, and stop during try execution
The system SHALL capture Ctrl+S, Ctrl+P, and Ctrl+X as keyboard shortcuts during try execution. All SHALL require double-press confirmation (same pattern as Ctrl+C guard). Available shortcuts SHALL be displayed below the status line. None of these shortcuts SHALL modify microbead task state.

#### Scenario: Shortcut hint display
- **WHEN** a try is executing
- **THEN** the system SHALL render shortcut hints below the status line: `[Ctrl+S skip]  [Ctrl+P pause]  [Ctrl+X stop]  [Ctrl+C quit]`

#### Scenario: Ctrl+S skip — first press
- **WHEN** the operator presses Ctrl+S while a try is executing
- **THEN** the system SHALL display a confirmation message such as "Press Ctrl+S again to skip" and SHALL NOT yet skip

#### Scenario: Ctrl+S skip — confirmed
- **WHEN** the operator presses Ctrl+S a second time within the confirmation window
- **THEN** the system SHALL cancel the current try and assign the same microbead to the next runner in the round-robin rotation (a new run)
- **AND** the round-robin rotation SHALL continue its normal sequence (skip advances the rotation pointer, same as a completed run would)
- **AND** microbead task state SHALL NOT be modified

#### Scenario: Ctrl+P pause — first press
- **WHEN** the operator presses Ctrl+P while a try is executing
- **THEN** the system SHALL display a confirmation message such as "Press Ctrl+P again to pause" and SHALL NOT yet pause

#### Scenario: Ctrl+P pause — confirmed
- **WHEN** the operator presses Ctrl+P a second time within the confirmation window
- **THEN** the system SHALL cancel the current try and display "Paused — press Enter to resume"
- **AND** the relay SHALL wait for the operator to press Enter before continuing

#### Scenario: Ctrl+P resume
- **WHEN** the operator presses Enter while the relay is paused
- **THEN** the system SHALL start a new try within the same run (same runner)

#### Scenario: Ctrl+X stop — first press
- **WHEN** the operator presses Ctrl+X while a try is executing
- **THEN** the system SHALL display a confirmation message such as "Press Ctrl+X again to stop" and SHALL NOT yet stop

#### Scenario: Ctrl+X stop — confirmed
- **WHEN** the operator presses Ctrl+X a second time within the confirmation window
- **THEN** the system SHALL let the current try finish normally, then exit the relay without starting the next run

#### Scenario: Confirmation timeout
- **WHEN** only one press of Ctrl+S, Ctrl+P, or Ctrl+X occurs and the confirmation window expires
- **THEN** the confirmation state SHALL reset; a subsequent press SHALL be treated as a new first press

### Requirement: Terminal raw mode during try execution
The system SHALL put the terminal in raw mode during try execution to capture keyboard shortcuts directly. The original terminal state SHALL be restored when the try completes, the relay pauses, or the relay exits.

### Requirement: Ctrl+R retry deferred to resilient-execution
Ctrl+R (retry — new try, same runner, consuming retry budget with operator override when exhausted) is NOT part of this change. It SHALL be added by the `resilient-execution` change, which introduces explicit retry budget management.
