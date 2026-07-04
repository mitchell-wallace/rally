## MODIFIED Requirements

### Requirement: Outing summary log
The system SHALL maintain a human- and machine-readable outing digest at `.rally/summary.jsonl` as the sole top-level data file under `.rally/`. The file SHALL be append-only, with one JSON object per line representing a finalized outing or handoff. New records SHALL carry `outing_id` (string), `summary` (string), `updated_at` (RFC3339 string), and optionally `laps_completed` and `handoff`.

For backwards compatibility, readers SHALL accept legacy `run_id` records and populate the outing identity from that key. If both `outing_id` and `run_id` are present with different values, readers SHALL reject the record with a parse error rather than silently choosing one.

#### Scenario: New summary records use outing_id
- **WHEN** a finalized outing or handoff summary is appended
- **THEN** the written JSON line SHALL contain `outing_id`
- **AND** it SHALL NOT write `run_id`

#### Scenario: Legacy summary records still load
- **WHEN** `.rally/summary.jsonl` contains a legacy record with `run_id` and no `outing_id`
- **THEN** the system SHALL load it as the outing identity

#### Scenario: Conflicting summary identity keys fail
- **WHEN** a summary record contains both `outing_id` and `run_id` with different values
- **THEN** the system SHALL reject that record with a parse error naming the conflict

### Requirement: Active try outing-state metadata
The system SHALL persist transient active try metadata in outing state so live CLI commands can target an in-flight try before its final record is appended to try history. New state writes SHALL use `outing_id` and `active_outing_id`. Readers SHALL accept legacy `run_id` and `active_run_id` keys with the same conflict handling as summary records.

#### Scenario: New active metadata uses outing keys
- **WHEN** an executor is running for a try
- **THEN** outing state SHALL expose the active relay ID, active outing ID, active try ID, active log path, and active start time using outing-keyed JSON

#### Scenario: Legacy active metadata still loads
- **WHEN** state contains legacy `run_id` or `active_run_id`
- **THEN** the system SHALL load those values into the outing identity fields

#### Scenario: Active metadata cleanup preserves non-active outing state
- **WHEN** active try metadata is cleared
- **THEN** cleanup SHALL NOT remove outing state fields unrelated to active-tail targeting
