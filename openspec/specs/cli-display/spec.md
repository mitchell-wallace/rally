# cli-display Specification

## Purpose
TBD - created by archiving change cli-polish. Update Purpose after archive.
## Requirements
### Requirement: Width-aware shortcut hint
The system SHALL render the keyboard-shortcut hint on a single line regardless of
terminal width. At render time the system SHALL detect the terminal width and select
the widest hint tier (full, medium, narrow, or minimal) that fits on one line. When the
terminal width cannot be determined (e.g. stdout is not a TTY), the system SHALL use a
safe default width and corresponding tier and SHALL NOT wrap.

#### Scenario: Wide terminal shows full hint
- **WHEN** the terminal is wide enough for the full hint
- **THEN** the system SHALL render the full tier on a single line

#### Scenario: Narrow terminal selects a fitting tier
- **WHEN** the terminal is too narrow for the full hint
- **THEN** the system SHALL select the widest tier that fits on one line, down to the minimal tier

#### Scenario: Countdown redraw overwrites cleanly
- **WHEN** a 1-second countdown redraw occurs below a single-line hint
- **THEN** each tick SHALL overwrite the previous line rather than appending a new one

#### Scenario: Non-TTY output does not wrap
- **WHEN** terminal width cannot be determined
- **THEN** the system SHALL use a safe default width and a tier that does not wrap

### Requirement: Left-aligned hints
The system SHALL render the shortcut hint flush against the left edge, without centering
or leading padding.

#### Scenario: Hint renders flush-left
- **WHEN** the shortcut hint is rendered
- **THEN** it SHALL start at the left edge with no centering or indentation

### Requirement: Full-width headers
The system SHALL render header, footer, and summary lines to fill the terminal width,
capped at 80 columns, using box-drawing fill characters. On terminals narrower than the
content, the system SHALL clamp to the available width and truncate the label rather than
breaking the structure.

#### Scenario: Header fills available width
- **WHEN** a header line is rendered on a terminal at or above the content width
- **THEN** the system SHALL fill the line to the terminal width (capped at 80) with box-drawing characters

#### Scenario: Header clamps on a very narrow terminal
- **WHEN** the terminal is narrower than the header content
- **THEN** the system SHALL clamp to the available width and truncate the label, preserving the line structure

### Requirement: Activity age bounded by try runtime
The system SHALL bound the displayed `last activity` age by the current try's elapsed
runtime: the reported age SHALL NOT exceed how long the try has been running. As a
consequence, the derived "slowing" indicator SHALL NOT appear until the try's own log
silence reaches the slowing window. When no activity timestamp is available, the system
SHALL continue to display the no-activity placeholder.

#### Scenario: Fresh try with a stale log mtime reads as recent
- **WHEN** a try has just started and the active log file's modification time predates the try
- **THEN** the system SHALL report `last activity` as under one minute, NOT the absolute file age

#### Scenario: Slowing does not fire on pre-existing staleness
- **WHEN** a try has been running for less than the slowing window, regardless of the log file's prior mtime
- **THEN** the system SHALL NOT display the "slowing" indicator

### Requirement: Collapsed retry display
While a run is retrying within its retry budget, the system SHALL render the retry
progress as a single line that updates in place, rather than printing a separate
outcome footer for each attempt. When the run reaches its terminal result, the system
SHALL print exactly one outcome footer for the run.

#### Scenario: Retrying run shows one updating line
- **WHEN** a run fails an attempt but has retry budget remaining
- **THEN** the system SHALL update a single in-place retry line and SHALL NOT print a per-attempt outcome footer

#### Scenario: Terminal result prints one footer
- **WHEN** a run reaches its terminal result (recovered, or retry budget exhausted)
- **THEN** the system SHALL print exactly one outcome footer for the run

### Requirement: Terminal-outcome failure colouring
The system SHALL apply the failure colour to a "failed" outcome only when the failure
is terminal — the run's retry budget is exhausted, or the run had a single attempt.
Non-terminal retry states SHALL render in a neutral/dim style rather than the failure
colour.

#### Scenario: Interim retry attempt is not coloured as a failure
- **WHEN** an attempt fails but the run will retry
- **THEN** the system SHALL render the interim state in a neutral/dim style, not the failure colour

#### Scenario: Terminal failure is coloured
- **WHEN** a run fails its final attempt (or its only attempt)
- **THEN** the system SHALL render the failure footer in the failure colour

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

