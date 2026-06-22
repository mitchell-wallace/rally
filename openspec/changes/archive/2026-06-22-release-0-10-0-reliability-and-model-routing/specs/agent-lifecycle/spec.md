## MODIFIED Requirements

### Requirement: Responsive stop and quit shortcuts
The system SHALL act on the shutdown shortcuts according to their stated timing.
"quit now" (Ctrl+C) SHALL cancel the currently running attempt immediately via graceful
shutdown and then abort the relay, without waiting for the attempt to complete.
"graceful stop" (Ctrl+X) SHALL cancel and drain the currently running attempt via
graceful shutdown, record that operator cancellation as source `graceful_stop`, and
then stop the relay without starting a new run. That operator cancellation SHALL be
surfaced to the runner as cancellation source metadata so normal executor exit handling
cannot report the attempt as a failed harness error.
While a cancel is draining, the system SHALL remain responsive to a further
"quit now" press and SHALL escalate to immediate termination.

#### Scenario: Quit now cancels the running attempt immediately
- **WHEN** the operator triggers "quit now" while an attempt is running
- **THEN** the system SHALL cancel the attempt via graceful shutdown and abort the relay without waiting for the attempt to finish
- **AND** the runner SHALL receive cancellation source `quit_now`

#### Scenario: Quit now ends a stalled agent promptly
- **WHEN** the operator triggers "quit now" while the agent is stalled
- **THEN** the system SHALL terminate the attempt within the graceful-shutdown window rather than waiting for the stall threshold
- **AND** the runner SHALL receive cancellation source `quit_now`

#### Scenario: Graceful stop cancels and drains the current attempt
- **WHEN** the operator triggers "graceful stop" while an attempt is running
- **THEN** the system SHALL cancel the attempt via graceful shutdown, drain it, and stop the relay without starting a new run
- **AND** the runner SHALL receive cancellation source `graceful_stop`
