## ADDED Requirements

### Requirement: Configurable stall threshold default
The system SHALL read the stall/liveness threshold from `stall_threshold_secs` in `.rally/config.toml` and SHALL default it to 900 seconds (15 minutes) when unset. The "slowing" display indicator SHALL derive from this threshold (at 0.6× the threshold) so a single configured value moves both the kill threshold and the warning indicator together.

#### Scenario: Default threshold when unset
- **WHEN** a relay starts and `stall_threshold_secs` is not configured
- **THEN** the system SHALL use a 900-second stall threshold
- **AND** the "slowing" indicator SHALL appear only after ~540 seconds (0.6×) of log silence

#### Scenario: Operator overrides threshold
- **WHEN** `stall_threshold_secs` is set to a positive value
- **THEN** the system SHALL use that value for both the stall threshold and the derived slowing indicator

#### Scenario: Normal reasoning is not flagged
- **WHEN** an agent produces no log activity for a period shorter than the slowing-indicator window (e.g. a multi-minute reasoning burst under the default)
- **THEN** the system SHALL NOT display a "slowing" indicator for that period

### Requirement: Live retry indicator
While a run is retrying within its retry budget, the system SHALL surface the retry progress as an inline field (`retry N/M`) on the existing live status line. The system SHALL NOT print a separate console block for each retry attempt.

#### Scenario: Retry in progress
- **WHEN** a run begins attempt N of M (N > 1)
- **THEN** the live status line SHALL include a `retry N/M` field
- **AND** no new status block SHALL be printed solely to announce the retry

### Requirement: Run-level result tally
The final relay summary SHALL count each run once: a run counts as a pass if it ultimately completed, and as a failure only if all of its retry attempts were exhausted without completion. Individual retried (non-final) attempts SHALL NOT each be counted as failures.

#### Scenario: Run succeeds after retries
- **WHEN** a run fails several attempts and then completes on a later attempt
- **THEN** the final summary SHALL count the run as one pass and zero failures

#### Scenario: Run exhausts retries
- **WHEN** a run fails every attempt up to its retry budget
- **THEN** the final summary SHALL count the run as one failure

### Requirement: Runner normalizes final snippets
The relay runner SHALL normalize the final snippet used for persisted `TryResult.Summary`, retry context, and `summary.jsonl` so Rally's persisted surfaces agree about what the agent reported. When a `laps wrapup` summary is recorded after `laps done` or `laps handoff`, that wrapup summary SHALL be the golden source. If no wrapup summary was recorded, the runner SHALL use the executor's parsed final assistant or structured summary text. If neither source exists, the runner SHALL use the executor's bounded tail text or explicit no-finalization/error indicator.

#### Scenario: Wrapup summary recorded
- **WHEN** an agent finalizes a run by calling `laps done` or `laps handoff` and then `laps wrapup --summary ...`
- **THEN** the persisted `TryResult.Summary` SHALL use the `laps wrapup` summary text
- **AND** retry context and `summary.jsonl` SHALL use the same normalized final snippet

#### Scenario: Executor summary fallback
- **WHEN** no `laps wrapup` summary was recorded but the executor returned parsed final assistant or structured summary text
- **THEN** the runner SHALL use that executor summary as the normalized final snippet

#### Scenario: Bounded fallback summary
- **WHEN** no wrapup summary and no parsed executor final text are available
- **THEN** the runner SHALL use the executor's bounded tail text or explicit no-finalization/error indicator as the normalized final snippet

### Requirement: State commit respects operator gitignore
When the system commits its `.rally` operational state paths, any `.rally` path that the operator has placed under `.gitignore` SHALL be skipped without error and without forcing the add. The default tracked `.rally` paths SHALL explicitly include `.rally/config.toml` and `.rally/summary.jsonl`. The system SHALL NOT use `git add -f` and SHALL NOT abort the run because a `.rally` operational path was gitignored. This requirement is scoped to `.rally` operational paths and SHALL NOT permit skipping `.laps/laps.json`.

#### Scenario: Tracked path is gitignored by operator
- **WHEN** the system attempts to add a `.rally` operational state path the operator has gitignored
- **THEN** the system SHALL skip that path, continue committing the remaining tracked `.rally` paths, and SHALL NOT return an error for the ignored path

#### Scenario: Laps queue state remains mandatory
- **WHEN** `.laps/laps.json` is present in the workspace
- **THEN** this gitignore-tolerance behavior SHALL NOT treat it as optional or silently omit it from required commits
