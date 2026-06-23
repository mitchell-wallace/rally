## ADDED Requirements

### Requirement: Removed gemini harness alias warning
The system SHALL emit a one-time warning when a configured route or model alias resolves to the removed `gemini` harness (alias `ge` or `gemini`), so operators upgrading past 0.12.0 receive actionable guidance instead of a silent resolution failure. The warning SHALL name the configured role, route entry, and the resolved alias, and SHALL recommend the equivalent `antigravity` configuration. The warning SHALL be emitted at most once per alias per relay start, and SHALL NOT block startup or be promoted to an error: legacy `[harness.ge.models]`, `gemini_model`, and `routes x = ["ge:…"]` blocks SHALL be silently ignored (not rejected) so a malformed config does not prevent the relay from running.

#### Scenario: Removed alias produces a one-time warning
- **WHEN** a relay starts and a configured route entry resolves to the `ge` or `gemini` alias
- **THEN** the system SHALL emit a warning naming the role, route entry, and resolved alias, and SHALL recommend `antigravity` as the replacement
- **AND** the warning SHALL NOT repeat for the same alias during the same relay

#### Scenario: Legacy gemini config is silently ignored
- **WHEN** a config contains `[harness.ge.models]`, `gemini_model`, or other gemini-specific blocks after the 0.12.0 upgrade
- **THEN** the system SHALL NOT reject the config
- **AND** the system SHALL NOT attempt to load those blocks as active configuration

#### Scenario: Routes that resolve to the removed alias do not start a runner
- **WHEN** a route entry's alias resolves to `gemini` during selection
- **THEN** the selector SHALL treat the entry as unresolvable and advance to the next entry in the route (or fail with a clear unresolvable-route error if no fallback exists)
- **AND** no `GeminiExecutor` SHALL be invoked because the executor no longer exists in the binary
