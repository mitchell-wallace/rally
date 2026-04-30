## ADDED Requirements

### Requirement: Per-harness resume capability declaration
The system SHALL extend the executor adapter interface with `ResumeSupported() bool`. Adapters that support session resume on retry SHALL return `true` and SHALL surface a session-id from `Execute` (via a new field on `TryResult`). Adapters that don't support resume SHALL return `false` and the relay-runner SHALL fall back to a fresh-start retry.

#### Scenario: Adapter declares resume support
- **WHEN** an executor adapter returns `ResumeSupported() == true` and `Execute` populates `TryResult.SessionID`
- **THEN** subsequent retries within the same run SHALL pass the harness-specific resume flag and the captured session-id to `Execute`

#### Scenario: Adapter does not declare resume support
- **WHEN** `ResumeSupported() == false` for the active adapter
- **THEN** retries SHALL use a fresh-start invocation; no session-id is captured or passed

### Requirement: Resume-aware retry path
The system SHALL implement retries differently based on the active adapter's resume capability. On `ResumeSupported() == true`, retries pass the resume parameters and `.rally/run-state.json` is preserved across the retry. On `ResumeSupported() == false`, retries restart fresh and `.rally/run-state.json` is cleared.

#### Scenario: Retry preserves run-state on resume
- **WHEN** a try fails, the adapter supports resume, and a retry is about to start
- **THEN** `.rally/run-state.json` SHALL be preserved (the v0.4.0 handoff flag and accumulated bead IDs persist for the resumed agent)

#### Scenario: Retry clears run-state on fresh start
- **WHEN** a try fails, the adapter does not support resume, and a fresh-start retry is about to begin
- **THEN** `.rally/run-state.json` SHALL be cleared before the retry executes (handoff flag and accumulated bead IDs reset)

#### Scenario: Resume across crash mid-handoff
- **WHEN** a run crashes between the first and second `mb handoff` calls AND the adapter supports resume AND a resume retry is attempted
- **THEN** the handoff flag in `.rally/run-state.json` SHALL be preserved so the agent can complete the second `mb handoff` call

#### Scenario: Fresh start across crash mid-handoff
- **WHEN** a run crashes between the first and second `mb handoff` calls AND the adapter does not support resume AND a fresh-start retry is attempted
- **THEN** the handoff flag SHALL be cleared (the original handoff intent is lost; the bead remains open so the next run picks it up normally)
