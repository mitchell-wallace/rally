## ADDED Requirements

### Requirement: Relay output uses styled formatting
The system SHALL render try headers, try footers, and relay summaries using lipgloss-based styled formatting with colour and visual hierarchy.

#### Scenario: Try header
- **WHEN** a try begins execution
- **THEN** the system SHALL render a separator line, agent name, run index, attempt number, and start time in local `HH:MM` format

#### Scenario: Try footer
- **WHEN** a try completes
- **THEN** the system SHALL render the outcome (pass/fail), runtime, count of files changed, and commit hash (if applicable)

#### Scenario: Relay summary
- **WHEN** a relay completes all iterations
- **THEN** the system SHALL render total runs, pass/fail counts, and total runtime

#### Scenario: Colour scheme
- **WHEN** rendering styled output
- **THEN** the system SHALL use green for success, red for failure, yellow for retries, and dim grey for timestamps
