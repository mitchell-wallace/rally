## ADDED Requirements

### Requirement: Full-screen terminal UI
The system SHALL provide a full-screen terminal UI as the default mode (no subcommand), using Bubble Tea with bubbles components. The UI SHALL use gitui-style bordered panels and respond to terminal resize events.

#### Scenario: Default launch opens TUI
- **WHEN** rally is invoked without a subcommand
- **THEN** the system SHALL launch the full-screen TUI

#### Scenario: Terminal resize adjusts layout
- **WHEN** the terminal is resized
- **THEN** all panels SHALL reflow to fill the available space

### Requirement: Dashboard panel
The system SHALL display a dashboard panel showing: current relay status (or "idle"), relay progress (completed/total runs), agent mix, and a list of recent runs with their outcome, agent type, runtime, and timestamp.

#### Scenario: Active relay shown
- **WHEN** a relay is running
- **THEN** the dashboard SHALL display the current run's agent type, attempt number, and progress (N/M runs completed)

#### Scenario: Recent run history shown
- **WHEN** the dashboard is displayed
- **THEN** it SHALL show recent runs from both the current and previous relays, including completion status, agent, runtime, and git stats

#### Scenario: Idle state shown
- **WHEN** no relay is active
- **THEN** the dashboard SHALL display "Idle" and show the most recent relay's summary

### Requirement: Live run status
The system SHALL display live run status during an active relay showing: elapsed runtime, git lines added/removed, and number of files changed. This status SHALL NOT stream agent stdout.

#### Scenario: Runtime updates during run
- **WHEN** a run is in progress
- **THEN** the live status SHALL display a continuously updating elapsed time

#### Scenario: Git stats shown after run
- **WHEN** a run completes
- **THEN** the live status SHALL display lines added, lines removed, and files changed from the run's git diff

### Requirement: Inbox panel
The system SHALL provide an inbox panel for managing messages in a FIFO queue. Users SHALL be able to: compose new messages, view pending and addressed messages, mark messages as addressed, and reorder pending messages.

#### Scenario: Compose new message
- **WHEN** the user presses the compose key in the inbox view
- **THEN** the system SHALL enter compose mode and accept text input for a new message body

#### Scenario: Reorder pending messages
- **WHEN** the user reorders a pending message
- **THEN** the system SHALL update the message's position in the FIFO queue

#### Scenario: View message status
- **WHEN** the inbox is displayed
- **THEN** pending messages SHALL appear above addressed messages, ordered by position

### Requirement: View navigation
The system SHALL support keyboard navigation between views: dashboard, inbox, and any overlays (e.g., project switcher if multi-project support is added later).

#### Scenario: Switch to inbox
- **WHEN** the user presses the inbox key from the dashboard
- **THEN** the system SHALL display the inbox panel

#### Scenario: Return to dashboard
- **WHEN** the user presses Escape from a non-dashboard view
- **THEN** the system SHALL return to the dashboard panel

### Requirement: Relay control from TUI
The system SHALL allow starting and stopping relays from the TUI via keyboard shortcuts.

#### Scenario: Start relay from TUI
- **WHEN** the user presses the start key and no relay is running
- **THEN** the system SHALL start a relay using the configured settings

#### Scenario: Stop relay from TUI
- **WHEN** the user presses the stop key while a relay is running
- **THEN** the system SHALL request a graceful stop (complete current run, then halt)
