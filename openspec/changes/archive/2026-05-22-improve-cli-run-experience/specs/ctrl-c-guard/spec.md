## ADDED Requirements

### Requirement: Double-tap Ctrl+C to exit during try execution
The system SHALL intercept Ctrl+C during try execution and require a second press within a 4-second window to trigger a graceful stop. The first press SHALL display a confirmation message. Outside of try execution (e.g. at relay resume prompt, between runs), single Ctrl+C SHALL work normally.

#### Scenario: First Ctrl+C during try execution
- **WHEN** the operator presses Ctrl+C while a try is executing
- **THEN** the system SHALL display "Press Ctrl+C again to exit" and SHALL NOT terminate the try

#### Scenario: Second Ctrl+C within window
- **WHEN** the operator presses Ctrl+C a second time within 4 seconds of the first press
- **THEN** the system SHALL initiate a graceful stop of the relay

#### Scenario: Timeout after first Ctrl+C
- **WHEN** the operator presses Ctrl+C once during try execution and more than 4 seconds elapse without a second press
- **THEN** the confirmation state SHALL reset; a subsequent Ctrl+C SHALL be treated as a new first press

#### Scenario: Ctrl+C outside try execution
- **WHEN** the operator presses Ctrl+C while not inside a try (e.g. between runs, at a prompt)
- **THEN** the system SHALL exit normally on a single press
