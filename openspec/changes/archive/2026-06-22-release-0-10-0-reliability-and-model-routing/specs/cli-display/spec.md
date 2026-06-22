## ADDED Requirements

### Requirement: Cancelled outcome display
The system SHALL render cancelled run and try outcomes in a muted/grey style. Cancelled outcomes SHALL NOT use the failure colour and SHALL NOT use the success colour. Relay summaries SHALL present cancelled outcomes separately from failed outcomes.

#### Scenario: Cancelled footer is muted
- **WHEN** an attempt is recorded with outcome `cancelled`
- **THEN** the displayed footer or summary line SHALL use muted/grey styling and the label `cancelled`
- **AND** it SHALL NOT use red failure styling or green success styling

#### Scenario: Cancelled output includes source
- **WHEN** a cancelled attempt has a source such as `skip`, `graceful_stop`, or `quit_now`
- **THEN** the displayed output SHALL include the cancellation source where outcome details are shown

#### Scenario: Cancelled summary is not failed
- **WHEN** a relay summary includes cancelled attempts or runs
- **THEN** the summary SHALL NOT include those cancelled outcomes in the failed count
- **AND** it SHALL expose them as cancelled where counts or outcome buckets are shown

### Requirement: Tail active target and highlighting
The `rally tail` command SHALL preserve explicit historical try selection while making the default target active-run aware. `--try N` for positive N SHALL retain existing 1-based historical semantics. `--try 0` and the default invocation SHALL prefer active try metadata when present, then fall back to newest completed try history. Tail highlighting SHALL be opt-in with a plain default.

#### Scenario: Default tail follows active try
- **WHEN** active try metadata exists and the operator runs `rally tail` without an explicit positive try number
- **THEN** the command SHALL stream the active try log instead of the newest completed try

#### Scenario: Fresh workspace active tail does not error
- **WHEN** a workspace has an active try log but no completed try records yet
- **THEN** `rally tail` SHALL target the active try log rather than reporting that no tries are recorded

#### Scenario: Explicit historical try remains unchanged
- **WHEN** the operator runs `rally tail --try 1`
- **THEN** the command SHALL select the first persisted historical try and SHALL NOT prefer active metadata

#### Scenario: Missing active log falls back
- **WHEN** active try metadata points at a missing log file, is stale, or belongs to no unfinished relay/run, and completed try history exists
- **THEN** the command SHALL print a warning and fall back to the newest completed try

#### Scenario: Plain tail remains default
- **WHEN** no highlight mode is requested
- **THEN** tail output SHALL be copied without syntax highlighting

#### Scenario: Heuristic highlighting is opt-in
- **WHEN** the operator requests heuristic highlighting
- **THEN** tail output SHALL apply lightweight token-aware colouring without requiring a new external highlighter dependency
