## MODIFIED Requirements

### Requirement: Opt-in telemetry sink
The system SHALL provide a telemetry sink that is disabled by default for source builds. The sink SHALL activate New Relic only when a complete New Relic credential pair is available from `NEW_RELIC_LICENSE_KEY` + `NEW_RELIC_ACCOUNT_ID`, or from baked release defaults `DefaultNewRelicLicenseKey` + `DefaultNewRelicAccountID`, and SHALL be force-disabled when `RALLY_TELEMETRY=0` is set. When inactive, telemetry calls SHALL be no-ops with no network access and SHALL NOT create the machine-local identifier file. Legacy Sentry configuration SHALL remain supported for one compatibility window only as an explicit fallback when no complete New Relic credential pair is available.

#### Scenario: No telemetry credentials means no-op
- **WHEN** rally runs without a complete New Relic credential pair and without supported legacy Sentry fallback
- **THEN** all telemetry calls SHALL be no-ops and no events SHALL be sent

#### Scenario: Kill switch overrides configured telemetry
- **WHEN** New Relic or legacy Sentry telemetry credentials are available but `RALLY_TELEMETRY=0` is set
- **THEN** the sink SHALL remain disabled
- **AND** no machine-local identifier file SHALL be created

#### Scenario: Env New Relic credentials override baked credentials
- **WHEN** both `NEW_RELIC_LICENSE_KEY` + `NEW_RELIC_ACCOUNT_ID` and baked New Relic credentials are available
- **THEN** the environment credentials SHALL take precedence

#### Scenario: New Relic release default overrides legacy Sentry fallback
- **WHEN** a baked New Relic credential pair exists and legacy Sentry config is also present
- **THEN** telemetry SHALL initialize New Relic rather than Sentry

#### Scenario: Partial New Relic credentials do not activate
- **WHEN** only one of `NEW_RELIC_LICENSE_KEY` or `NEW_RELIC_ACCOUNT_ID` is available and no baked pair exists
- **THEN** New Relic telemetry SHALL NOT initialize
- **AND** the system SHALL either fall back to supported legacy Sentry config or remain no-op

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL emit one bounded New Relic custom event per try, represent relay/run/try timing as explicit custom span events with parent-child identifiers, and report genuine operator-worthy failures as New Relic custom failure events. Operator-worthy failures SHALL remain limited to failures that warrant attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and `needs_user` recovery signals. Lap-pin mismatches (`wrong_lap_consumed` / `multi_lap_consumed`) SHALL be warning diagnostics by default, not operator-worthy failures. Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs/custom events, NOT operator-worthy failures, to avoid alert noise. The timeout/handoff lifecycle outcomes (`TryOutcome` `run_timeout`, `handoff_requested`, and `handoff_timeout`) SHALL be recorded as spans/logs/custom events and SHALL NOT be reported as operator-worthy failures. A `needs_user` recovery classification or relay-synthesized cap signal MAY be reported as an operator-worthy failure, while the other four recovery classifications SHALL remain spans/logs/custom events. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

#### Scenario: Try emits a structured custom event
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit one bounded `RallyTry` custom event for that try

#### Scenario: Relay emits a logical trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL emit `RallySpan` custom events for relay, run, and try spans with span id, parent span id, operation, description, and duration attributes

#### Scenario: Infra failure becomes an operator-worthy custom event
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL emit a bounded `RallyFailure` custom event describing the failure

#### Scenario: Relay stall becomes an operator-worthy custom event
- **WHEN** a relay pass ends with all agent types frozen
- **THEN** the sink SHALL emit a bounded `RallyFailure` custom event describing the stall, so the lockout is surfaced for operator attention

#### Scenario: Agent-class failure does not report an operator failure
- **WHEN** a try fails with an agent-class failure that remains retry-eligible
- **THEN** the sink SHALL record it as a span/log/custom event only and SHALL NOT emit a `RallyFailure`

#### Scenario: Handoff outcomes do not report an operator failure
- **WHEN** a try resolves as `run_timeout`, `handoff_requested`, or `handoff_timeout`
- **THEN** the sink SHALL record it as a span/log/custom event and SHALL NOT emit a `RallyFailure`

#### Scenario: needs_user recovery may report an operator failure
- **WHEN** a RECOVERY run records the `needs_user` classification, or the relay synthesizes `needs_user` on reaching the consecutive-recovery cap
- **THEN** the sink MAY emit a `RallyFailure` so the deferred decision is surfaced for attention

#### Scenario: Lap mismatch is warning telemetry
- **WHEN** a try records `wrong_lap_consumed` or `multi_lap_consumed`
- **THEN** the sink SHALL emit a `RallyDiagnostic` custom event with `level=warning`, `event_kind=lap_pin_mismatch`, and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`
- **AND** `level=warning` SHALL come from a first-class `telemetry.LevelWarning` value that maps to New Relic diagnostic attributes and to Sentry warning severity during the fallback window
- **AND** it SHALL NOT emit a `RallyFailure` for the mismatch by default

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery custom event and SHALL NOT report it as an operator-worthy failure

### Requirement: Telemetry PII scrubbing
The system SHALL apply backend-neutral scrubbing before any telemetry payload is handed to New Relic. The scrubber SHALL never send the contents of `current_task.md` or full agent transcripts; only summaries and metadata SHALL be sent. For New Relic-bound Event API attributes, sensitive keys SHALL be dropped entirely rather than retained with a redacted placeholder value. The system SHALL additionally collapse the user's home-directory prefix in any transmitted working-directory or path-shaped field (e.g. `/home/<user>/…` → `~/…`) so the username is not transmitted, SHALL collapse home paths embedded inside transmitted free-text context fields such as raw provider signals, and SHALL NOT transmit hostname, username, host-derived server identity, runtime environment host metadata, automatic log lines, or network identity in any custom event or HTTPS request payload.

#### Scenario: Task prompt never shipped
- **WHEN** an event would otherwise include `current_task.md` contents or a full transcript
- **THEN** the scrubber SHALL remove that payload before the event is sent
- **AND** the New Relic Event API payload SHALL NOT contain a placeholder attribute for the removed field

#### Scenario: Working directory is username-stripped
- **WHEN** an event includes the working directory or a path under the user's home directory
- **THEN** the scrubber SHALL collapse the home prefix to `~` so the username is not transmitted

#### Scenario: Raw signal free text is username-stripped
- **WHEN** a raw provider signal or parsed message contains a path under the user's home directory
- **THEN** the scrubber SHALL collapse the embedded home prefix to `~` before sending

#### Scenario: Host identity never shipped
- **WHEN** any custom event or request payload is assembled
- **THEN** it SHALL NOT contain hostname, username, host-derived server identity, runtime host metadata, automatic log lines, or IP address

#### Scenario: Full machine id omitted from New Relic
- **WHEN** a New Relic custom event is assembled with machine identity available
- **THEN** it SHALL include `machine_id_prefix` and `relay_guid` where available
- **AND** it SHALL NOT include the full anonymous `machine_id`

### Requirement: Telemetry flush on exit
The system SHALL flush pending telemetry before the CLI process exits, using a bounded timeout so a slow or unreachable network never blocks exit. For the New Relic Event API sink this SHALL drain the in-memory event queue with bounded HTTPS POSTs and return when the timeout expires.

#### Scenario: Buffered events flushed
- **WHEN** the rally CLI is about to exit with telemetry enabled
- **THEN** the system SHALL flush buffered events with a bounded timeout (e.g. 2 seconds)

#### Scenario: Unreachable network does not hang exit
- **WHEN** flushing cannot complete because the network is unreachable
- **THEN** the system SHALL stop waiting after the bounded timeout and exit

### Requirement: Product telemetry DSN activation
The system SHALL support baked default New Relic credentials for release binaries while preserving user/operator overrides. The system SHALL resolve telemetry in this order: `RALLY_TELEMETRY=0` disables telemetry regardless of any configured credentials; `NEW_RELIC_LICENSE_KEY` + `NEW_RELIC_ACCOUNT_ID`; baked `DefaultNewRelicLicenseKey` + `DefaultNewRelicAccountID`; legacy Sentry fallback only when no complete New Relic credential pair exists; no credentials disables telemetry. The system SHALL initialize telemetry only for commands that run relays, so mechanical commands do not create telemetry side-effect files or open a telemetry client solely because baked credentials exist.

#### Scenario: Baked New Relic credentials activate release telemetry
- **WHEN** no env New Relic credentials are set
- **AND** the binary was built with baked New Relic credentials
- **AND** a relay command is executed
- **THEN** telemetry SHALL initialize with New Relic

#### Scenario: Mechanical commands do not activate baked telemetry
- **WHEN** the binary was built with baked New Relic credentials
- **AND** a mechanical command such as help, version, or update is executed
- **THEN** telemetry SHALL remain a no-op
- **AND** the command SHALL NOT create the machine-local identifier file

#### Scenario: Environment credentials override baked default
- **WHEN** `NEW_RELIC_LICENSE_KEY` and `NEW_RELIC_ACCOUNT_ID` are set
- **AND** baked New Relic credentials are also present
- **THEN** telemetry SHALL initialize with the environment credentials

#### Scenario: Legacy Sentry fallback warns
- **WHEN** only `SENTRY_DSN` or `[telemetry] sentry_dsn` is configured
- **THEN** the system SHALL initialize legacy Sentry telemetry for this compatibility window
- **AND** it SHALL warn that Sentry telemetry is deprecated in favor of New Relic

#### Scenario: Kill switch disables every provider
- **WHEN** `RALLY_TELEMETRY=0` is set
- **AND** env, baked, or legacy telemetry credentials are present
- **THEN** telemetry SHALL use the no-op sink and SHALL NOT create telemetry side-effect files

## ADDED Requirements

### Requirement: New Relic Event API limits
The system SHALL convert tags and contexts into New Relic-compatible Event API attributes before emission. Attributes SHALL be simple string or number values; event type names SHALL be from Rally's fixed set (`RallySpan`, `RallyTry`, `RallyDiagnostic`, `RallyFailure`); custom event attribute counts and request payload size SHALL be bounded. Rally SHALL apply a local budget of 64 attributes per event, below New Relic's documented custom-event maximum, to keep payloads predictable. When a payload exceeds limits, the system SHALL preserve correlation tags and failure/outcome fields first, then deterministic lower-priority context fields, and SHALL drop the remainder rather than encoding large nested blobs.

#### Scenario: Custom event attributes stay within limits
- **WHEN** a try log, diagnostic event, span event, or failure event is emitted to New Relic
- **THEN** the event type and attributes SHALL satisfy the local Event API name, key, value-type, 64-attribute budget, and payload-size limits

#### Scenario: Correlation fields are prioritized
- **WHEN** an event has more attributes than the New Relic event budget allows
- **THEN** the system SHALL keep relay/run/try/repo/lap/runner/outcome/failure fields before lower-priority context fields

#### Scenario: Nested contexts are flattened safely
- **WHEN** a telemetry context block contains nested data
- **THEN** the system SHALL flatten only bounded scalar values into prefixed attributes and SHALL NOT JSON-encode the whole context as a large string
