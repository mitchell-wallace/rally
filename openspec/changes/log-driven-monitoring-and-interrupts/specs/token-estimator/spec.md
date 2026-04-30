## ADDED Requirements

### Requirement: Per-harness character-based token estimate
The system SHALL display a best-effort token estimate in the live monitor for the active try. The estimate SHALL be derived from the active log file's character count divided by a per-harness `chars_per_token` divisor declared on the executor adapter. The value SHALL be displayed in the monitor line as `~Nk tok` (for thousands) or `~N tok` (for smaller values). No external API calls or tokenizer libraries SHALL be invoked.

#### Scenario: Harness declares a divisor
- **WHEN** the active try uses a harness whose adapter declares a `chars_per_token` divisor (e.g. `3.5`)
- **THEN** the live monitor SHALL display an estimate computed from the current log file size divided by that divisor

#### Scenario: Harness cannot expose enough text to estimate
- **WHEN** the active try uses a harness whose adapter declares `chars_per_token = 0` or whose log content cannot be measured (e.g. binary stream)
- **THEN** the live monitor SHALL display `—` in the token slot rather than an estimated value

#### Scenario: Estimate updates with log growth
- **WHEN** the live monitor refreshes during a try whose log is actively growing
- **THEN** the displayed token estimate SHALL update on each refresh to reflect the current log file size
