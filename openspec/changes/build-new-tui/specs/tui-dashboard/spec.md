## ADDED Requirements

### Requirement: Tabbed relay visibility
The system SHALL provide a full-screen terminal UI with navigable Dashboard,
Transcript, Agents, and Laps views backed by presentation-neutral data.

#### Scenario: Operator changes tabs
- **WHEN** an operator presses a tab shortcut in `rally tui`
- **THEN** the selected view is rendered without interrupting the active relay

### Requirement: Runtime controls remain available
The TUI SHALL translate quit, skip, pause, and graceful-stop shortcuts through
the runtime control seam with arm-and-confirm behavior.

#### Scenario: Operator confirms a runtime action
- **WHEN** an operator presses the same runtime shortcut twice within the confirmation window
- **THEN** the TUI sends a confirmed control press for that action

### Requirement: Workspace-free demo
The TUI SHALL provide synthetic demo playback that does not require a Rally
workspace.

#### Scenario: Demo launched outside a workspace
- **WHEN** a user runs `rally tui --demo` outside a repository
- **THEN** the tabbed UI opens with synthetic relay data
