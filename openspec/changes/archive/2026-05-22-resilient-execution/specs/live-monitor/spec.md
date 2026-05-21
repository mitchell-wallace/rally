## ADDED Requirements

### Requirement: Freeze-state surfaced in live monitor
The system SHALL display a freeze-state indicator in the live monitor when the freeze detector (per `freeze-detection` capability) has flagged the active try as frozen or as in the ambiguous-pending state during the threshold window. The indicator SHALL be visually distinct from the steady-state line so the operator can see at a glance that intervention may be needed.

#### Scenario: Freeze indicator appears
- **WHEN** the freeze detector flags the active try frozen
- **THEN** the live monitor line SHALL include a `❄ frozen` (or equivalent) marker, in addition to the existing concern fields (conn count, IO bytes) which are already rendered when the heuristic trips

#### Scenario: Pending-freeze indicator
- **WHEN** the freeze threshold has not yet been crossed but the silence is approaching it (e.g. ≥ 60% of the threshold has elapsed without log activity)
- **THEN** the live monitor line MAY include a `⚠ slowing` (or equivalent) marker so the operator sees the trend before the kill happens

### Requirement: Recovery indicator after freeze-driven retry
The system SHALL render a brief recovery indicator (e.g. `↻ recovered`) in the live monitor immediately following a successful resume-retry that was triggered by a freeze graceful-kill. The indicator SHALL appear on the next tick after the resumed try begins producing log output and SHALL clear once steady state resumes.

#### Scenario: Recovery indicator on resumed try
- **WHEN** a freeze-killed try is resumed via the resume-aware retry path and the resumed agent begins producing log output
- **THEN** the next live-monitor tick SHALL render the `↻ recovered` marker; subsequent ticks SHALL drop it once the steady-state line has rendered for at least one full tick

### Requirement: Token estimator uses configured `chars_per_token`
The system SHALL source per-harness `chars_per_token` divisors from `[reliability].chars_per_token` config when set, falling back to the v0.3.0 hardcoded default per harness when unset. The displayed estimate format (`~Nk tok` or `—`) is unchanged from v0.3.0.

#### Scenario: Configured divisor used
- **WHEN** `[reliability].chars_per_token.opencode = 4.0` is set and the active try uses the opencode harness
- **THEN** the token-estimator helper SHALL use `4.0` as the divisor for that try

#### Scenario: Default divisor used when unset
- **WHEN** `[reliability].chars_per_token` does not include an entry for the active harness
- **THEN** the token-estimator helper SHALL use the harness adapter's hardcoded `CharsPerToken()` value
