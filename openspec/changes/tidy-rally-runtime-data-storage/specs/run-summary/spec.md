## ADDED Requirements

### Requirement: Append-only run summary digest
The system SHALL maintain a human- and machine-readable run digest at `.rally/summary.jsonl` as the sole top-level data file under `.rally/`, replacing the former `progress.yaml`. The file SHALL be append-only, with one JSON object per line representing a finalized run or handoff. Each record SHALL carry at least `run_id`, `summary`, `updated_at`, and (when present) `laps_completed` and `handoff` (with `summary`, `followups`, `created_lap_ids`).

#### Scenario: Run finalization appends a line
- **WHEN** a run is finalized (completed or handed off)
- **THEN** the system SHALL append one JSON line capturing the run/handoff entry with an RFC3339 `updated_at` timestamp

#### Scenario: summary.jsonl is git-tracked
- **WHEN** the `.rally/` directory is committed to git
- **THEN** `summary.jsonl` SHALL be included in version control as the durable cross-container run digest

#### Scenario: progress.yaml no longer written
- **WHEN** any run is finalized after this change
- **THEN** the system SHALL write only to `summary.jsonl` and SHALL NOT create or update `progress.yaml`
