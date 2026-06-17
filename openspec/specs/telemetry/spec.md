# telemetry Specification

## Purpose
TBD - created by archiving change tidy-rally-runtime-data-storage. Update Purpose after archive.
## Requirements
### Requirement: Opt-in telemetry sink
The system SHALL provide a telemetry sink that is disabled by default. The sink SHALL activate only when a Sentry DSN is configured via `config.toml` `[telemetry] sentry_dsn` or the `SENTRY_DSN` environment variable, and SHALL be force-disabled when `RALLY_TELEMETRY=0` is set. When inactive, telemetry calls SHALL be no-ops with no network access.

#### Scenario: No DSN means no-op
- **WHEN** rally runs without a configured DSN
- **THEN** all telemetry calls SHALL be no-ops and no events SHALL be sent

#### Scenario: Kill switch overrides DSN
- **WHEN** a DSN is configured but `RALLY_TELEMETRY=0` is set
- **THEN** the sink SHALL remain disabled

#### Scenario: Env DSN overrides config
- **WHEN** both `config.toml` and `SENTRY_DSN` provide a DSN
- **THEN** the `SENTRY_DSN` value SHALL take precedence

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL emit a structured log event per try, model a relay as a trace whose runs and tries are spans, and report genuine failures as Sentry Issues. Issues SHALL be reserved for failures that warrant operator attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and lap-integrity violations (`wrong_lap_consumed` / `multi_lap_consumed`). Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs, NOT Issues, to avoid alert noise. The timeout/handoff lifecycle outcomes (`TryOutcome` `run_timeout`, `handoff_requested`, and `handoff_timeout`) SHALL be recorded as spans/logs and SHALL NOT be captured as Issues — a timeout-driven handoff is a designed recovery path (its response is a RECOVERY route), not an alert. A `needs_user` recovery classification or relay-synthesized cap signal MAY be captured as an Issue, since it is the escape hatch reserved for decisions wanting operator attention; this covers both a RECOVERY agent's recorded `needs_user` classification and a relay-synthesized `needs_user` raised when the consecutive-recovery cap is reached. The other four recovery classifications SHALL be recorded as spans/logs. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

#### Scenario: Try emits a structured log
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit a structured log event for that try

#### Scenario: Relay emits a trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL produce a transaction for the relay with child spans for runs and tries

#### Scenario: Infra failure becomes an Issue
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL capture a Sentry Issue describing the failure

#### Scenario: Relay stall becomes an Issue
- **WHEN** a relay pass ends with all agent types frozen
- **THEN** the sink SHALL capture a Sentry Issue describing the stall, so the lockout is surfaced for operator attention

#### Scenario: Agent-class failure does not raise an Issue
- **WHEN** a try fails with an agent-class failure that remains retry-eligible
- **THEN** the sink SHALL record it as a span/log only and SHALL NOT capture an Issue

#### Scenario: Handoff outcomes do not raise an Issue
- **WHEN** a try resolves as `run_timeout`, `handoff_requested`, or `handoff_timeout`
- **THEN** the sink SHALL record it as a span/log and SHALL NOT capture an Issue

#### Scenario: needs_user recovery may raise an Issue
- **WHEN** a RECOVERY run records the `needs_user` classification, or the relay synthesizes `needs_user` on reaching the consecutive-recovery cap
- **THEN** the sink MAY capture an Issue so the deferred decision is surfaced for operator attention

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery log event and SHALL NOT capture it as an Issue (rotating to a backup runner is a healthy recovery, not an alert)

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
The system SHALL apply a `before_send` scrubber that prevents large or sensitive
payloads from being transmitted. The scrubber SHALL never send the contents of
`current_task.md` or full agent transcripts; only summaries and metadata SHALL be sent.
The system SHALL additionally collapse the user's home-directory prefix in any
transmitted working-directory or path-shaped field (e.g. `/home/<user>/…` → `~/…`) so
the username is not transmitted, SHALL collapse home paths embedded inside transmitted
free-text context fields such as raw provider signals, and SHALL NOT transmit hostname,
username, host-derived `server_name`, or network identity in any event.

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
- **WHEN** any event is assembled
- **THEN** it SHALL NOT contain hostname, username, host-derived `server_name`, or IP address

### Requirement: Telemetry flush on exit
The system SHALL flush pending telemetry before the CLI process exits, using a bounded timeout so a slow or unreachable network never blocks exit.

#### Scenario: Buffered events flushed
- **WHEN** the rally CLI is about to exit with telemetry enabled
- **THEN** the system SHALL flush buffered events with a bounded timeout (e.g. 2 seconds)

#### Scenario: Unreachable network does not hang exit
- **WHEN** flushing cannot complete because the network is unreachable
- **THEN** the system SHALL stop waiting after the bounded timeout and exit

### Requirement: Product telemetry DSN activation
The system SHALL support a baked default Sentry DSN for release binaries while
preserving user/operator overrides. The system SHALL resolve the effective DSN in this
order: `RALLY_TELEMETRY=0` disables telemetry regardless of any configured DSN;
`SENTRY_DSN` environment variable; `.rally/config.toml` `telemetry.sentry_dsn`; baked
default DSN; no DSN disables telemetry. The system SHALL initialize telemetry only for
commands that run relays, so mechanical commands do not create telemetry side-effect
files or open a Sentry client solely because a baked default DSN exists.

#### Scenario: Baked DSN activates release telemetry
- **WHEN** no env or config DSN is set
- **AND** the binary was built with a baked default DSN
- **AND** a relay command is executed
- **THEN** telemetry SHALL initialize with the baked default DSN

#### Scenario: Mechanical commands do not activate baked telemetry
- **WHEN** the binary was built with a baked default DSN
- **AND** a mechanical command such as help, version, or update is executed
- **THEN** telemetry SHALL remain a no-op
- **AND** the command SHALL NOT create the machine-local identifier file

#### Scenario: Environment DSN overrides baked default
- **WHEN** `SENTRY_DSN` is set
- **AND** a config or baked default DSN is also present
- **THEN** telemetry SHALL initialize with `SENTRY_DSN`

#### Scenario: Config DSN overrides baked default
- **WHEN** `.rally/config.toml` contains `telemetry.sentry_dsn`
- **AND** `SENTRY_DSN` is unset
- **AND** a baked default DSN is present
- **THEN** telemetry SHALL initialize with the config DSN

#### Scenario: Kill switch disables baked default
- **WHEN** `RALLY_TELEMETRY=0` is set
- **AND** env, config, or baked default DSNs are present
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

