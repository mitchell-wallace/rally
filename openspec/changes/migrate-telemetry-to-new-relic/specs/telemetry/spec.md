## MODIFIED Requirements

### Requirement: Opt-in telemetry sink
The system SHALL provide a telemetry sink that is disabled by default for source builds. The sink SHALL activate New Relic only when a New Relic license key is available from `NEW_RELIC_LICENSE_KEY` or from baked release default `DefaultNewRelicLicenseKey`, and SHALL be force-disabled when `RALLY_TELEMETRY=0` is set or `[telemetry] enabled = false` is configured. When inactive, telemetry calls SHALL be no-ops with no network access and SHALL NOT create the machine-local identifier file. The system SHALL NOT initialize Sentry as a fallback.

#### Scenario: No telemetry license means no-op
- **WHEN** rally runs without a New Relic license key
- **THEN** all telemetry calls SHALL be no-ops and no events SHALL be sent

#### Scenario: Kill switch overrides configured telemetry
- **WHEN** New Relic telemetry credentials are available but `RALLY_TELEMETRY=0` is set
- **THEN** the sink SHALL remain disabled
- **AND** no machine-local identifier file SHALL be created

#### Scenario: Config opt-out overrides configured telemetry
- **WHEN** New Relic telemetry credentials are available but `[telemetry] enabled = false` is configured
- **THEN** the sink SHALL remain disabled
- **AND** no machine-local identifier file SHALL be created

#### Scenario: Env New Relic license overrides baked license
- **WHEN** both `NEW_RELIC_LICENSE_KEY` and a baked New Relic license key are available
- **THEN** the environment license SHALL take precedence

#### Scenario: Sentry config does not activate telemetry
- **WHEN** only `SENTRY_DSN` or `[telemetry] sentry_dsn` is present
- **THEN** telemetry SHALL remain disabled
- **AND** the system SHALL NOT initialize a Sentry client

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL use New Relic Go APM transactions/segments for relay/run/try timing, emit one bounded New Relic custom event per try, emit bounded diagnostic custom events, and report genuine operator-worthy failures through New Relic error reporting. Operator-worthy failures SHALL remain limited to failures that warrant attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and `needs_user` recovery signals. Lap-pin mismatches (`wrong_lap_consumed` / `multi_lap_consumed`) SHALL be warning diagnostics by default, not operator-worthy failures. Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs/custom events, NOT operator-worthy failures, to avoid alert noise. The timeout/handoff lifecycle outcomes (`TryOutcome` `run_timeout`, `handoff_requested`, and `handoff_timeout`) SHALL be recorded as spans/logs/custom events and SHALL NOT be reported as operator-worthy failures. A `needs_user` recovery classification or relay-synthesized cap signal MAY be reported as an operator-worthy failure, while the other four recovery classifications SHALL remain spans/logs/custom events. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

#### Scenario: Try emits a structured custom event
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit one bounded `RallyTry` New Relic custom event for that try

#### Scenario: Relay emits a logical trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL represent relay/run/try timing with New Relic transactions and segments
- **AND** the sink SHALL attach bounded Rally span id, parent span id, operation, description, and duration attributes where supported

#### Scenario: Infra failure becomes operator-worthy telemetry
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL report a New Relic error with bounded failure attributes
- **AND** the sink MAY emit a bounded `RallyFailure` custom event for NRQL queryability

#### Scenario: Relay stall becomes operator-worthy telemetry
- **WHEN** a relay pass ends with all agent types frozen
- **THEN** the sink SHALL report a New Relic error or bounded `RallyFailure` event describing the stall, so the lockout is surfaced for operator attention

#### Scenario: Agent-class failure does not report an operator failure
- **WHEN** a try fails with an agent-class failure that remains retry-eligible
- **THEN** the sink SHALL record it as a span/log/custom event only and SHALL NOT report a New Relic error or `RallyFailure`

#### Scenario: Handoff outcomes do not report an operator failure
- **WHEN** a try resolves as `run_timeout`, `handoff_requested`, or `handoff_timeout`
- **THEN** the sink SHALL record it as a span/log/custom event and SHALL NOT report a New Relic error or `RallyFailure`

#### Scenario: needs_user recovery may report an operator failure
- **WHEN** a RECOVERY run records the `needs_user` classification, or the relay synthesizes `needs_user` on reaching the consecutive-recovery cap
- **THEN** the sink MAY report a New Relic error or `RallyFailure` so the deferred decision is surfaced for attention

#### Scenario: Lap mismatch is warning telemetry
- **WHEN** a try records `wrong_lap_consumed` or `multi_lap_consumed`
- **THEN** the sink SHALL emit a `RallyDiagnostic` custom event with `level=warning`, `event_kind=lap_pin_mismatch`, and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`
- **AND** `level=warning` SHALL come from a first-class `telemetry.LevelWarning` value that maps to New Relic diagnostic attributes
- **AND** it SHALL NOT report a New Relic error or emit a `RallyFailure` for the mismatch by default

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery custom event and SHALL NOT report it as an operator-worthy failure

### Requirement: Telemetry PII scrubbing
The system SHALL apply backend-neutral scrubbing to Rally-supplied telemetry before attributes or custom events are handed to New Relic. The scrubber SHALL never send the contents of `current_task.md` or full agent transcripts; only summaries and metadata SHALL be sent. Sensitive Rally-supplied keys SHALL be dropped entirely rather than retained with a redacted placeholder value. The system SHALL collapse the user's home-directory prefix in any Rally-supplied working-directory or path-shaped field (e.g. `/home/<user>/...` -> `~/...`) so the username is not transmitted by Rally custom attributes, SHALL collapse home paths embedded inside transmitted free-text context fields such as raw provider signals, and SHALL NOT add custom attributes containing raw hostname, username, IP address, prompt text, transcript text, or command log text. New Relic application log forwarding SHALL be disabled.

#### Scenario: Task prompt never shipped
- **WHEN** an event would otherwise include `current_task.md` contents or a full transcript
- **THEN** the scrubber SHALL remove that payload before the event is sent
- **AND** the New Relic custom event or error attributes SHALL NOT contain a placeholder attribute for the removed field

#### Scenario: Working directory is username-stripped
- **WHEN** an event includes the working directory or a path under the user's home directory
- **THEN** the scrubber SHALL collapse the home prefix to `~` so the username is not transmitted in Rally-supplied attributes

#### Scenario: Raw signal free text is username-stripped
- **WHEN** a raw provider signal or parsed message contains a path under the user's home directory
- **THEN** the scrubber SHALL collapse the embedded home prefix to `~` before sending

#### Scenario: Host identity is not added by Rally
- **WHEN** Rally custom attributes, custom events, or error attributes are assembled
- **THEN** they SHALL NOT contain raw hostname, username, IP address, prompt text, transcript text, or command log text
- **AND** the New Relic agent SHALL be configured with a generic host display name where supported

#### Scenario: Application logs are not forwarded
- **WHEN** New Relic telemetry is initialized
- **THEN** New Relic application log forwarding/decorating SHALL be disabled

### Requirement: Telemetry flush on exit
The system SHALL flush pending telemetry before the CLI process exits, using a bounded timeout so a slow or unreachable network never blocks exit. For the New Relic Go APM sink this SHALL use bounded New Relic shutdown/wait APIs and return when the timeout expires.

#### Scenario: Buffered events flushed
- **WHEN** the rally CLI is about to exit with telemetry enabled
- **THEN** the system SHALL flush buffered events with a bounded timeout (e.g. 2 seconds)

#### Scenario: Unreachable network does not hang exit
- **WHEN** flushing cannot complete because the network is unreachable
- **THEN** the system SHALL stop waiting after the bounded timeout and exit

### Requirement: Product telemetry DSN activation
The system SHALL support a baked default New Relic license key for release binaries while preserving user/operator overrides. The system SHALL resolve telemetry in this order: `RALLY_TELEMETRY=0` disables telemetry regardless of any configured credentials; `[telemetry] enabled=false` disables telemetry; `NEW_RELIC_LICENSE_KEY`; baked `DefaultNewRelicLicenseKey`; no license disables telemetry. The system SHALL initialize telemetry only for commands that run relays, so mechanical commands do not create telemetry side-effect files or open a telemetry client solely because baked credentials exist. The system SHALL NOT activate Sentry from legacy DSN config.

#### Scenario: Baked New Relic license activates release telemetry
- **WHEN** no env New Relic license is set
- **AND** the binary was built with a baked New Relic license key
- **AND** a relay command is executed
- **THEN** telemetry SHALL initialize with New Relic

#### Scenario: Mechanical commands do not activate baked telemetry
- **WHEN** the binary was built with a baked New Relic license key
- **AND** a mechanical command such as help, version, or update is executed
- **THEN** telemetry SHALL remain a no-op
- **AND** the command SHALL NOT create the machine-local identifier file

#### Scenario: Environment license overrides baked default
- **WHEN** `NEW_RELIC_LICENSE_KEY` is set
- **AND** a baked New Relic license key is also present
- **THEN** telemetry SHALL initialize with the environment license

#### Scenario: Legacy Sentry DSN is ignored
- **WHEN** only `SENTRY_DSN` or `[telemetry] sentry_dsn` is configured
- **THEN** telemetry SHALL remain disabled
- **AND** no Sentry client SHALL initialize

#### Scenario: Kill switch disables telemetry
- **WHEN** `RALLY_TELEMETRY=0` is set
- **AND** env or baked New Relic credentials are present
- **THEN** telemetry SHALL use the no-op sink and SHALL NOT create telemetry side-effect files

## ADDED Requirements

### Requirement: New Relic Go APM agent limits
The system SHALL convert Rally tags and contexts into New Relic-compatible custom attributes before attaching them to transactions, segments, custom events, or errors. Attributes SHALL be simple scalar values; custom event names SHALL be from Rally's fixed set (`RallyTry`, `RallyDiagnostic`, `RallyFailure`); custom event attribute counts and string lengths SHALL be bounded. Rally SHALL apply a local attribute budget to keep payloads predictable. When a payload exceeds limits, the system SHALL preserve correlation tags and failure/outcome fields first, then deterministic lower-priority context fields, and SHALL drop the remainder rather than encoding large nested blobs.

#### Scenario: Custom event attributes stay within limits
- **WHEN** a try log, diagnostic event, span attribute set, or failure event is emitted to New Relic
- **THEN** the event type and attributes SHALL satisfy Rally's local name, key, value-type, attribute-budget, and size limits

#### Scenario: Correlation fields are prioritized
- **WHEN** an event has more attributes than the New Relic/Rally budget allows
- **THEN** the system SHALL keep relay/run/try/repo/lap/runner/outcome/failure fields before lower-priority context fields

#### Scenario: Nested contexts are flattened safely
- **WHEN** a telemetry context block contains nested data
- **THEN** the system SHALL flatten only bounded scalar values into prefixed attributes and SHALL NOT JSON-encode the whole context as a large string
