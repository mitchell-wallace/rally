## MODIFIED Requirements

### Requirement: Collapsed retry display
While an outing is retrying within its retry budget, the system SHALL render retry progress as a single line that updates in place, rather than printing a separate outcome footer for each try. When the outing reaches its terminal result, the system SHALL print exactly one outcome footer for the outing.

#### Scenario: Retrying outing shows one updating line
- **WHEN** a try fails but the outing will retry
- **THEN** the system SHALL update a single in-place retry line and SHALL NOT print a per-try outcome footer

#### Scenario: Terminal result prints one footer
- **WHEN** an outing reaches its terminal result
- **THEN** the system SHALL print exactly one outcome footer for the outing

### Requirement: Cancelled outcome display
The system SHALL render cancelled outing and try outcomes in a muted/grey style. Cancelled outcomes SHALL NOT use failure or success colour. Relay summaries SHALL present cancelled outcomes separately from failed outcomes.

#### Scenario: Cancelled footer is muted
- **WHEN** a try is recorded with outcome `cancelled`
- **THEN** the displayed footer or summary line SHALL use muted/grey styling and the label `cancelled`

#### Scenario: Cancelled summary is not failed
- **WHEN** a relay summary includes cancelled tries or outings
- **THEN** the summary SHALL NOT include those cancelled outcomes in the failed count
- **AND** it SHALL expose them as cancelled where counts or outcome buckets are shown

### Requirement: Tail active target and highlighting
The `rally tail` command SHALL preserve explicit historical try selection while making the default target active-outing aware. `--try N` for positive N SHALL retain existing 1-based historical semantics. `--try 0` and the default invocation SHALL prefer active try metadata for the active outing when present, then fall back to newest completed try history.

#### Scenario: Default tail follows active try
- **WHEN** active try metadata exists for the active outing and the operator runs `rally tail` without an explicit positive try number
- **THEN** the command SHALL stream the active try log instead of the newest completed try

#### Scenario: Explicit historical try remains unchanged
- **WHEN** the operator runs `rally tail --try 1`
- **THEN** the command SHALL select the first persisted historical try and SHALL NOT prefer active metadata
