## ADDED Requirements

### Requirement: Persisted text fields are length-capped
When persisting final-snippet text, the system SHALL cap each free-text field to a bounded maximum length of 3000 runes before writing. Truncation SHALL preserve both the head and tail of the content with an explicit truncation marker, consistent with the recent-context truncation used when building prompts. The cap SHALL apply regardless of which harness or code path produced the text, serving as a durable backstop against runaway output in `tries.jsonl` and `summary.jsonl`.

#### Scenario: Oversized summary capped on write
- **WHEN** a try record whose `summary` exceeds the cap is persisted
- **THEN** the stored `summary` SHALL be truncated to at most 3000 runes with a head+tail truncation marker

#### Scenario: Small summary unchanged
- **WHEN** a try record whose `summary` is within the cap is persisted
- **THEN** the stored `summary` SHALL be written verbatim with no truncation marker

#### Scenario: Cap applies to try record final-snippet fields
- **WHEN** a try record is persisted
- **THEN** `summary` and `remaining_work` SHALL be subject to the 3000-rune cap

#### Scenario: Cap applies to summary log fields
- **WHEN** a finalized run or handoff summary is appended to `summary.jsonl`
- **THEN** `RunEntry.Summary`, `HandoffEntry.Summary`, and each free-text `HandoffEntry.Followups` string SHALL be subject to the same 3000-rune cap
