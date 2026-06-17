## MODIFIED Requirements

### Requirement: Opt-in telemetry sink
The system SHALL provide a telemetry sink that is disabled by default for source builds. The sink SHALL activate only when New Relic telemetry is configured via `NEW_RELIC_LICENSE_KEY`, `.rally/config.toml` `[telemetry] new_relic_license_key`, or a baked release `DefaultNewRelicLicenseKey`, and SHALL be force-disabled when `RALLY_TELEMETRY=0` is set. When inactive, telemetry calls SHALL be no-ops with no network access and SHALL NOT create the machine-local identifier file. Legacy Sentry configuration MAY remain supported only as an explicit fallback when no New Relic key or baked New Relic key is available.

#### Scenario: No telemetry key means no-op
- **WHEN** rally runs without a New Relic key, baked New Relic key, or supported legacy fallback
- **THEN** all telemetry calls SHALL be no-ops and no events SHALL be sent

#### Scenario: Kill switch overrides configured telemetry
- **WHEN** a New Relic key or legacy telemetry config is available but `RALLY_TELEMETRY=0` is set
- **THEN** the sink SHALL remain disabled
- **AND** no machine-local identifier file SHALL be created

#### Scenario: Env New Relic key overrides config
- **WHEN** both `NEW_RELIC_LICENSE_KEY` and `.rally/config.toml` provide telemetry credentials
- **THEN** `NEW_RELIC_LICENSE_KEY` SHALL take precedence

#### Scenario: New Relic release default overrides legacy Sentry fallback
- **WHEN** a baked New Relic key exists and legacy Sentry config is also present
- **THEN** telemetry SHALL initialize New Relic rather than Sentry

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL emit a structured custom event per try, model a relay as a New Relic background transaction whose run and try work is represented by child timing segments, and report genuine operator-worthy failures as New Relic errors/custom failure events. Operator-worthy failures SHALL remain limited to failures that warrant attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, lap-integrity violations (`wrong_lap_consumed` / `multi_lap_consumed`), and `needs_user` recovery signals. Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs/custom events, NOT operator-worthy errors, to avoid alert noise. The timeout/handoff lifecycle outcomes (`TryOutcome` `run_timeout`, `handoff_requested`, and `handoff_timeout`) SHALL be recorded as spans/logs/custom events and SHALL NOT be reported as errors. A `needs_user` recovery classification or relay-synthesized cap signal MAY be reported as an operator-worthy error, while the other four recovery classifications SHALL remain spans/logs/custom events. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

#### Scenario: Try emits a structured custom event
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit one bounded structured custom event for that try

#### Scenario: Relay emits a trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL produce a New Relic background transaction for the relay with child timing segments for runs and tries where the backend supports them

#### Scenario: Infra failure becomes an operator-worthy error
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL report a New Relic error/custom failure event describing the failure

#### Scenario: Relay stall becomes an operator-worthy error
- **WHEN** a relay pass ends with all agent types frozen
- **THEN** the sink SHALL report a New Relic error/custom failure event describing the stall, so the lockout is surfaced for operator attention

#### Scenario: Agent-class failure does not report an operator error
- **WHEN** a try fails with an agent-class failure that remains retry-eligible
- **THEN** the sink SHALL record it as a span/log/custom event only and SHALL NOT report an operator-worthy error

#### Scenario: Handoff outcomes do not report an operator error
- **WHEN** a try resolves as `run_timeout`, `handoff_requested`, or `handoff_timeout`
- **THEN** the sink SHALL record it as a span/log/custom event and SHALL NOT report an operator-worthy error

#### Scenario: needs_user recovery may report an operator error
- **WHEN** a RECOVERY run records the `needs_user` classification, or the relay synthesizes `needs_user` on reaching the consecutive-recovery cap
- **THEN** the sink MAY report an operator-worthy error so the deferred decision is surfaced for attention

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery custom event and SHALL NOT report it as an operator-worthy error

### Requirement: Telemetry PII scrubbing
The system SHALL apply backend-neutral scrubbing before any telemetry payload is handed to New Relic. The scrubber SHALL never send the contents of `current_task.md` or full agent transcripts; only summaries and metadata SHALL be sent. The system SHALL additionally collapse the user's home-directory prefix in any transmitted working-directory or path-shaped field (e.g. `/home/<user>/…` → `~/…`) so the username is not transmitted, SHALL collapse home paths embedded inside transmitted free-text context fields such as raw provider signals, and SHALL NOT transmit hostname, username, host-derived server identity, or network identity in any event, error, transaction, segment, or custom event.

#### Scenario: Task prompt never shipped
- **WHEN** an event would otherwise include `current_task.md` contents or a full transcript
- **THEN** the scrubber SHALL remove that payload before the event is sent

#### Scenario: Working directory is username-stripped
- **WHEN** an event includes the working directory or a path under the user's home directory
- **THEN** the scrubber SHALL collapse the home prefix to `~` so the username is not transmitted

#### Scenario: Raw signal free text is username-stripped
- **WHEN** a raw provider signal or parsed message contains a path under the user's home directory
- **THEN** the scrubber SHALL collapse the embedded home prefix to `~` before sending

#### Scenario: Host identity never shipped
- **WHEN** any event, error, transaction, segment, or custom event is assembled
- **THEN** it SHALL NOT contain hostname, username, host-derived server identity, or IP address

### Requirement: Telemetry flush on exit
The system SHALL flush pending telemetry before the CLI process exits, using a bounded timeout so a slow or unreachable network never blocks exit. For the New Relic sink this SHALL use the Go agent's connection and shutdown/flush mechanisms with Rally's bounded timeout.

#### Scenario: Buffered events flushed
- **WHEN** the rally CLI is about to exit with telemetry enabled
- **THEN** the system SHALL flush buffered events with a bounded timeout (e.g. 2 seconds)

#### Scenario: Unreachable network does not hang exit
- **WHEN** flushing cannot complete because the network is unreachable
- **THEN** the system SHALL stop waiting after the bounded timeout and exit

### Requirement: Product telemetry activation
The system SHALL support a baked default New Relic license key for release binaries while preserving user/operator overrides. The system SHALL resolve telemetry in this order: `RALLY_TELEMETRY=0` disables telemetry regardless of any configured key; `NEW_RELIC_LICENSE_KEY` environment variable; `.rally/config.toml` `telemetry.new_relic_license_key`; baked `DefaultNewRelicLicenseKey`; optional legacy Sentry fallback only when no New Relic key exists; no key disables telemetry. The system SHALL initialize telemetry only for commands that run relays, so mechanical commands do not create telemetry side-effect files or open a telemetry client solely because a baked default key exists.

#### Scenario: Baked New Relic key activates release telemetry
- **WHEN** no env or config New Relic key is set
- **AND** the binary was built with a baked New Relic key
- **AND** a relay command is executed
- **THEN** telemetry SHALL initialize with New Relic

#### Scenario: Mechanical commands do not activate baked telemetry
- **WHEN** the binary was built with a baked New Relic key
- **AND** a mechanical command such as help, version, or update is executed
- **THEN** telemetry SHALL remain a no-op
- **AND** the command SHALL NOT create the machine-local identifier file

#### Scenario: Environment key overrides baked default
- **WHEN** `NEW_RELIC_LICENSE_KEY` is set
- **AND** a config or baked New Relic key is also present
- **THEN** telemetry SHALL initialize with `NEW_RELIC_LICENSE_KEY`

#### Scenario: Config key overrides baked default
- **WHEN** `.rally/config.toml` contains `telemetry.new_relic_license_key`
- **AND** `NEW_RELIC_LICENSE_KEY` is unset
- **AND** a baked New Relic key is present
- **THEN** telemetry SHALL initialize with the config key

#### Scenario: Kill switch disables baked default
- **WHEN** `RALLY_TELEMETRY=0` is set
- **AND** env, config, baked, or legacy telemetry credentials are present
- **THEN** telemetry SHALL use the no-op sink and SHALL NOT create telemetry side-effect files

### Requirement: New Relic attribute limits
The system SHALL convert tags and contexts into New Relic-compatible attributes before emission. Attributes SHALL be simple string, number, or boolean values; attribute names and event type names SHALL satisfy New Relic custom event limits; custom event attribute counts SHALL be bounded below New Relic's maximum. When a payload exceeds limits, the system SHALL preserve correlation tags and failure/outcome fields first, then deterministic lower-priority context fields, and SHALL drop the remainder rather than encoding large nested blobs.

#### Scenario: Custom event attributes stay within limits
- **WHEN** a try log, diagnostic event, or failure event is emitted to New Relic
- **THEN** the event type and attributes SHALL satisfy New Relic custom event name, key, value-type, and attribute-count limits

#### Scenario: Correlation fields are prioritized
- **WHEN** an event has more attributes than the New Relic event budget allows
- **THEN** the system SHALL keep relay/run/try/repo/lap/runner/outcome/failure fields before lower-priority context fields

#### Scenario: Nested contexts are flattened safely
- **WHEN** a telemetry context block contains nested data
- **THEN** the system SHALL flatten only bounded scalar values into prefixed attributes and SHALL NOT JSON-encode the whole context as a large string
