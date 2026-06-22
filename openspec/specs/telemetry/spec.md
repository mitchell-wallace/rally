# telemetry Specification

## Purpose
TBD - created by archiving change tidy-rally-runtime-data-storage. Update Purpose after archive.
## Requirements
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
When enabled, the system SHALL emit a structured custom event per try, model relay/run/try timing through the active telemetry sink's span abstraction, and report genuine failures as backend-neutral operator-worthy failure events. Operator-worthy failures SHALL be reserved for failures that warrant attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and lap-integrity violations that genuinely leave the queue unsafe. Lap-integrity mismatches (`wrong_lap_consumed` / `multi_lap_consumed`) SHALL be recorded as `LevelWarning` diagnostic events and SHALL carry `event_kind=lap_pin_mismatch` and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`, but SHALL NOT be captured as operator-worthy failures by default and SHALL NOT attach `failure_category` because 0.9.x reserves `failure_category` for failed lifecycle outcomes. Operator-cancelled attempts SHALL be recorded as cancellation telemetry or spans/logs only and SHALL NOT be captured as operator-worthy failures. The timeout/handoff lifecycle outcomes (`TryOutcome` `run_timeout`, `handoff_requested`, and `handoff_timeout`) SHALL be recorded as spans/logs/custom events and SHALL NOT be captured as operator-worthy failures. A `needs_user` recovery classification or relay-synthesized cap signal MAY be captured as an operator-worthy failure, while the other recovery classifications SHALL remain spans/logs/custom events. Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs/custom events, NOT operator-worthy failures, to avoid alert noise. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

Non-limit failure evidence MAY be attached to `RallyTry` events and spans when
the runner has bounded, scrubbed evidence from `FailureEvidence`, but ordinary
agent-class failures SHALL still not become `RallyFailure` events. `RallyFailure`
MAY include non-limit evidence only when the failure was already
operator-worthy under this requirement, such as infra-class failure, harness
launch/exec error, marker-as-text, panic, or `needs_user`. Raw evidence SHALL be
sanitized before emission: prompt/current_task/transcript-looking fields and
full `output:` / `stderr:` sections SHALL NOT be emitted as structured
telemetry fields. Lap IDs are not sensitive and MAY be emitted.

#### Scenario: Try emits a structured log
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit a structured event for that try

#### Scenario: Relay emits a trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL produce a relay/run/try timing hierarchy through its span abstraction

#### Scenario: Infra failure becomes operator-worthy telemetry
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL capture an operator-worthy failure event describing the failure

#### Scenario: Relay stall becomes operator-worthy telemetry
- **WHEN** a relay pass ends with all agent types frozen
- **THEN** the sink SHALL capture an operator-worthy failure event describing the stall, so the lockout is surfaced for operator attention

#### Scenario: Agent-class failure does not raise operator telemetry
- **WHEN** a try fails with an agent-class failure that remains retry-eligible
- **THEN** the sink SHALL record it as a span/log/custom event only and SHALL NOT capture an operator-worthy failure

#### Scenario: Lap mismatch is warning telemetry
- **WHEN** a try records `wrong_lap_consumed` or `multi_lap_consumed`
- **THEN** the sink SHALL record a `LevelWarning` diagnostic event with `event_kind=lap_pin_mismatch` and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`
- **AND** it SHALL NOT capture an operator-worthy failure for the mismatch by default
- **AND** it SHALL NOT attach `failure_category` unless the try also has a failed lifecycle outcome with a real `FailureCategory`

#### Scenario: Operator cancellation is not failure telemetry
- **WHEN** a try is recorded with outcome `cancelled`
- **THEN** the sink SHALL record cancellation status and source on spans/logs
- **AND** it SHALL NOT capture an operator-worthy failure for the cancelled attempt

#### Scenario: Cancelled unfinalized run is not incomplete finalization
- **WHEN** a laps-backed try is cancelled before finalizing with `laps done` or `laps handoff`
- **THEN** telemetry SHALL NOT capture an `incomplete_finalization` operator-worthy failure for that cancelled try

### Requirement: Prompt-size observability
When enabled, the system SHALL record the assembled-prompt size for each try and a breakdown of how much each source contributes (e.g. recent-try context, previous summary, project instructions, role instructions, task prompt, inbox/relay messages), so runaway prompt growth is detectable without reading transcripts.

#### Scenario: Prompt size emitted per try
- **WHEN** a try's prompt is assembled and the try is logged
- **THEN** the structured log event SHALL include the total assembled-prompt size and a per-source size breakdown

#### Scenario: Breakdown attributes each source
- **WHEN** the prompt-size breakdown is emitted
- **THEN** it SHALL attribute byte counts to each prompt source so a dominant source can be identified

### Requirement: Telemetry tagging and correlation
Every telemetry event SHALL be tagged with `relay_id`, `run_id`, `try_id`, `role`, `runner` (harness+model), `repo`, and `lap_id` where applicable, so events are filterable and correlate with the local `summary.jsonl` digest.

#### Scenario: Events carry correlation tags
- **WHEN** any telemetry event is emitted during a try
- **THEN** it SHALL include the available `relay_id`, `run_id`, `try_id`, `role`, `runner`, `repo`, and `lap_id` tags

### Requirement: Telemetry PII scrubbing
The system SHALL apply backend-neutral scrubbing to Rally-supplied telemetry before attributes, custom events, errors, or Rally-controlled log records are handed to New Relic. The scrubber SHALL never send the contents of `current_task.md` or full agent transcripts; only summaries and metadata SHALL be sent. Sensitive Rally-supplied keys SHALL be dropped entirely rather than retained with a redacted placeholder value. The system SHALL collapse the user's home-directory prefix in any Rally-supplied working-directory or path-shaped field (e.g. `/home/<user>/...` -> `~/...`) so the username is not transmitted by Rally custom attributes, SHALL collapse home paths embedded inside transmitted free-text context fields such as raw provider signals, and SHALL NOT add custom attributes or Rally-controlled log records containing raw hostname, username, IP address, prompt text, transcript text, or raw command output. New Relic application log forwarding SHALL be enabled intentionally.

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

#### Scenario: Application logs are forwarded intentionally
- **WHEN** New Relic telemetry is initialized
- **THEN** New Relic application log forwarding and application-log metrics SHALL be enabled with a bounded sample limit
- **AND** local log decorating SHALL remain disabled unless a later product decision enables it
- **AND** the 0.9.1 migration SHALL NOT add new Rally `Application.RecordLog` calls or logger integrations; `RallyTry` custom events remain the per-try observability stream

### Requirement: Telemetry flush on exit
The system SHALL flush pending telemetry before the CLI process exits, using a bounded timeout so a slow or unreachable network never blocks exit. For the New Relic Go APM sink this SHALL use bounded New Relic shutdown/wait APIs and return when the timeout expires.

#### Scenario: Buffered events flushed
- **WHEN** the rally CLI is about to exit with telemetry enabled
- **THEN** the system SHALL flush buffered events with a bounded timeout (e.g. 2 seconds)

#### Scenario: Unreachable network does not hang exit
- **WHEN** flushing cannot complete because the network is unreachable
- **THEN** the system SHALL stop waiting after the bounded timeout and exit

### Requirement: Product telemetry license-key activation
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

### Requirement: Run-environment context
When enabled, the system SHALL attach a run-environment context to the relay trace and
to every captured failure, carrying the rally version, operating system, architecture,
and a coarse terminal descriptor (the value of `$TERM`, or a non-TTY marker when stdout
is not a terminal). The system SHALL NOT include hostname, username, or network
identity in this context or in Sentry's top-level `server_name` field.

#### Scenario: Failure carries environment context
- **WHEN** a failure is captured with telemetry enabled
- **THEN** the event SHALL include the rally version, OS, architecture, and terminal descriptor

#### Scenario: Environment context omits host identity
- **WHEN** the run-environment context is built
- **THEN** it SHALL NOT contain hostname, username, or IP address
- **AND** the outgoing event SHALL NOT contain a host-derived `server_name`

### Requirement: Anonymous machine-local identity
The system SHALL maintain an anonymous, stable machine-local identifier that is a
randomly generated value, NOT derived from any machine attribute (hostname, MAC,
username). The identifier SHALL be generated once and persisted in rally's data
directory, and SHALL only be created when telemetry is active. When the persisted
identifier cannot be read or written, the system SHALL fall back to an ephemeral
anonymous value rather than failing the run.

#### Scenario: Identifier is stable across runs
- **WHEN** telemetry is active across multiple runs on the same machine
- **THEN** the system SHALL reuse the same persisted machine-local identifier

#### Scenario: Identifier is not derived from machine attributes
- **WHEN** the machine-local identifier is generated
- **THEN** it SHALL be a random value with no derivation from hostname, MAC, or username

#### Scenario: Disabled telemetry persists nothing
- **WHEN** telemetry is disabled (no DSN, or the kill switch is set)
- **THEN** the system SHALL NOT create the machine-local identifier file

### Requirement: Globally-unique relay identity
When enabled, the system SHALL attach a globally-unique relay identifier derived from
the machine-local identifier, the repo key, the relay start date, and the local relay
id, together with the relay start timestamp and the decided machine identity field
placement. The system SHALL emit `machine_id_prefix` as the filterable machine tag,
SHALL keep the full anonymous machine id in event context only, and SHALL continue to
emit the local `relay_id` tag for within-workspace correlation.

#### Scenario: Relay carries a globally-unique identifier
- **WHEN** a relay starts with telemetry enabled
- **THEN** its trace and any failures SHALL be tagged with a globally-unique relay identifier and the relay start timestamp, and SHALL carry the anonymous machine identity only in the decided tag/context locations

#### Scenario: Relay identifier is unique across repos
- **WHEN** two workspaces on the same machine have the same local relay id on the same date
- **THEN** their globally-unique relay identifiers SHALL differ by repo key

#### Scenario: Local relay id retained
- **WHEN** the globally-unique identifier is attached
- **THEN** the local `relay_id` tag SHALL still be emitted

#### Scenario: Full machine id is context-only
- **WHEN** machine identity is attached
- **THEN** `machine_id_prefix` SHALL be emitted as a tag
- **AND** the full anonymous machine id SHALL NOT be emitted as a tag
- **AND** the full anonymous machine id SHALL be available in the `rally` context

### Requirement: Agent state on captured failures
When a try failure is captured, the system SHALL attach the failing try's agent state as filterable scalar tags: the current attempt and retry budget, the `TryOutcome` lifecycle value, the stable failure category (as defined by the error-categorisation taxonomy) for a `failed` outcome, and the agent-type resilience state (active, probation, frozen, or benched) where known. Each try event SHALL carry the `outcome` tag, and a `failed` outcome SHALL additionally carry the existing `failure_category` tag; lifecycle outcomes such as `run_timeout`, `handoff_timeout`, and `handoff_requested` SHALL be distinguished by the `outcome` tag rather than a failure category. Handoff-only continuation tries SHALL also be identifiable as handoff-only in structured try logs/spans. When the failure category is a usage/quota exhaustion, the system SHALL additionally attach the quota scope and reset timing where the failure evidence provides them. When a try runs under the `recovery` route, the system SHALL additionally attach the `recovery_classification` scalar tag where the recovery agent recorded one. The system SHALL read these values from the failure evidence / recorded outcome produced upstream and SHALL NOT re-classify the try. The harness and model SHALL remain available via the existing `runner` tag. Relay-level captures that have no failing try SHALL attach relay-level state only and SHALL omit try-only fields.

#### Scenario: Captured failure carries agent state
- **WHEN** an operator-worthy failure is captured
- **THEN** the event SHALL include the attempt, retry budget, `outcome`, failure category (for a `failed` outcome), and resilience state tags

#### Scenario: Lifecycle outcome distinguished by the outcome tag
- **WHEN** a try resolves with a `run_timeout`, `handoff_timeout`, or `handoff_requested` outcome
- **THEN** the try event SHALL carry that value in the `outcome` tag and SHALL NOT encode it as a failure `category`

#### Scenario: Relay-level stall omits try-only fields
- **WHEN** a relay-level all-frozen stall is captured
- **THEN** the event SHALL include relay/global context and `agent_state=frozen`
- **AND** it SHALL NOT include attempt, retry budget, reset evidence, or raw-signal fields

#### Scenario: Usage-limit failure carries reset evidence
- **WHEN** a captured failure has a usage/quota-exhaustion category with reset evidence
- **THEN** the event SHALL additionally include the quota scope and reset timing as scalar tags

#### Scenario: Recovery run carries its classification
- **WHEN** a try runs under the `recovery` route and the recovery agent recorded a classification
- **THEN** the event SHALL include the `recovery_classification` scalar tag

#### Scenario: Agent-class failures unaffected
- **WHEN** an agent-class try failure is recorded as a span/log rather than an Issue
- **THEN** the agent-state tags SHALL NOT change whether it is captured as an Issue

### Requirement: Raw limit-signal capture
The system SHALL attach the bounded raw signal and parsed message from the failure
evidence to an info-level diagnostic event as a context block whenever a failed try's
category is a provider-limit signal (usage limit, short rate limit, or provider
overload), so the exact provider response shapes observed in the field can be used to
validate and normalize the per-harness evidence parsers. This diagnostic event SHALL be
emitted independently of whether the failure is captured as an operator-worthy Issue.
The attached raw signal SHALL remain bounded, SHALL pass through the PII scrubber, and
SHALL NOT include prompt or transcript content.

#### Scenario: Limit failure carries the raw provider signal
- **WHEN** a failed try has a usage-limit, short-rate-limit, or provider-overload category with failure evidence present
- **THEN** telemetry SHALL emit an info-level diagnostic event carrying the bounded raw signal and parsed message as a context block

#### Scenario: Limit diagnostic does not depend on Issue capture
- **WHEN** a provider-limit failure is not operator-worthy enough to be captured as an Issue
- **THEN** telemetry SHALL still emit the info-level limit-signal diagnostic event
- **AND** the diagnostic event SHALL be distinguishable by scalar tags such as `event_kind=limit_signal`

#### Scenario: Raw signal stays bounded and scrubbed
- **WHEN** the raw limit signal is attached
- **THEN** it SHALL be bounded in size, SHALL pass through the `before_send` scrubber, and SHALL NOT contain prompt or transcript content

#### Scenario: Non-limit categories attach no raw signal
- **WHEN** a captured failure has a category that is not a provider-limit signal
- **THEN** the system SHALL NOT emit a limit-signal diagnostic event
- **AND** it SHALL NOT attach a raw-signal context block

### Requirement: New Relic Go APM agent limits
The system SHALL convert Rally tags and contexts into New Relic-compatible custom attributes before attaching them to transactions, segments, custom events, or errors. Attributes SHALL be simple scalar values; custom event names SHALL be from Rally's fixed set (`RallyTry`, `RallyDiagnostic`, `RallyFailure`); custom event and error payloads SHALL contain at most 64 attributes, attribute keys SHALL be under 255 bytes, and string values SHALL be bounded. Rally SHALL apply a local attribute budget to keep payloads predictable. When a payload exceeds limits, the system SHALL preserve correlation tags and failure/outcome fields first, then deterministic lower-priority context fields, and SHALL drop the remainder rather than encoding large nested blobs.

#### Scenario: Custom event attributes stay within limits
- **WHEN** a try log, diagnostic event, span attribute set, or failure event is emitted to New Relic
- **THEN** the event type and attributes SHALL satisfy Rally's local name, key, value-type, attribute-budget, and size limits
- **AND** custom event and error attributes SHALL stay at or below 64 attributes with keys under 255 bytes

#### Scenario: Correlation fields are prioritized
- **WHEN** an event has more attributes than the New Relic/Rally budget allows
- **THEN** the system SHALL keep relay/run/try/repo/lap/runner/outcome/failure fields before lower-priority context fields

#### Scenario: Nested contexts are flattened safely
- **WHEN** a telemetry context block contains nested data
- **THEN** the system SHALL flatten only bounded scalar values into prefixed attributes and SHALL NOT JSON-encode the whole context as a large string

### Requirement: Warning-level telemetry
The system SHALL support a warning telemetry level for diagnostic events that should be more visible than info events but should not automatically create operator-worthy failures. After the 0.9.1 telemetry migration, the New Relic Go APM sink SHALL emit `level=warning` on the resulting `RallyDiagnostic` custom event.

#### Scenario: Warning event maps to warning diagnostic telemetry
- **WHEN** telemetry emits a diagnostic event with `LevelWarning`
- **THEN** the active sink SHALL send it as warning telemetry rather than info or error telemetry

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery log/custom event and SHALL NOT capture it as an operator-worthy failure (rotating to a backup runner is a healthy recovery, not an alert)

#### Scenario: Runner fallback records trigger context
- **WHEN** the routing scheduler rotates a lane to the next runner entry after a failed or unavailable prior run
- **THEN** the fallback event SHALL include the triggering run/try id, triggering outcome, fail reason, failure class/category where known, lap id, route name, and route-entry exhausted reason
- **AND** the fallback event SHALL NOT capture an operator-worthy failure

#### Scenario: Lap mismatch includes lap ids
- **WHEN** a try records `wrong_lap_consumed` or `multi_lap_consumed`
- **THEN** the warning diagnostic SHALL include the expected lap id, consumed lap count, and consumed lap ids when known

#### Scenario: Timeout telemetry explains handoff state
- **WHEN** a try resolves as `run_timeout`, `handoff_requested`, or `handoff_timeout`
- **THEN** the try event and span SHALL include timeout kind, timeout budget, session capture status, resume support status, handoff-only attempt status, and handoff blocker reason where known

#### Scenario: Ordinary agent error evidence stays non-alerting
- **WHEN** an ordinary agent-class try failure has bounded evidence
- **THEN** the evidence SHALL be recorded on `RallyTry`/span telemetry
- **AND** the system SHALL NOT create a `RallyFailure` solely because evidence exists

