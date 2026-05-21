## ADDED Requirements

### Requirement: Pattern-driven retry strategy table
The system SHALL maintain a static lookup table in `internal/reliability/patterns.go` mapping known harness error patterns to retry strategies. The table SHALL include entries for at least these documented cases:

| Pattern                                  | Strategy            |
|------------------------------------------|---------------------|
| opencode "API bad request" from provider | rotate (advance route) |
| gemini-cli exit 1                        | resume + retry      |
| claude rate-limit interrupt              | wait + resume       |
| codex completion despite limit warning   | no-op               |
| unknown failure                          | fresh restart       |

Patterns SHALL be matched against the last N lines of the try log (deterministic, no heuristics on partial output). The matched strategy SHALL drive the retry-loop's next action.

#### Scenario: opencode API-bad-request triggers rotate
- **WHEN** a try fails and the last N lines of the log contain the opencode "API bad request" pattern
- **THEN** the relay-runner SHALL invoke `OnAgentFailed(entry, "api-bad-request")` and SHALL advance to the next route entry (rotate) instead of retrying the same entry

#### Scenario: gemini-cli exit 1 triggers resume + retry
- **WHEN** a try ends with `gemini-cli` exiting 1 and the adapter supports resume
- **THEN** the relay-runner SHALL retry with the resume flag, preserving session state

#### Scenario: claude rate-limit triggers wait + resume
- **WHEN** a try fails with a claude rate-limit interrupt pattern in the log
- **THEN** the relay-runner SHALL wait for the cooldown duration (extracted from the error message if present, else a default) and SHALL retry with resume

#### Scenario: codex completion despite limit warning is no-op
- **WHEN** a try produces a successful completion but the log includes a codex limit-warning pattern
- **THEN** the relay-runner SHALL treat the try as completed normally; no retry

#### Scenario: Unknown failure falls through to fresh restart
- **WHEN** a try fails and no pattern in the table matches the log content
- **THEN** the relay-runner SHALL retry with a fresh start (the v0.2.x default behaviour)

### Requirement: Pattern table is the single update point
The system SHALL match error patterns ONLY via the table in `internal/reliability/patterns.go`. New harness CLIs or new error formats SHALL be added by editing this file. Patterns SHALL NOT be matched ad-hoc inside executor adapters or relay-runner branches.

#### Scenario: Adding a new pattern
- **WHEN** a new harness error pattern is observed in production
- **THEN** the fix SHALL be a code change adding a new row to the `patterns.go` table; integration tests SHALL exercise the new pattern with a fixture log
