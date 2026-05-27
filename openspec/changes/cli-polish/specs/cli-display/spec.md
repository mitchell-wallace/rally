## ADDED Requirements

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
