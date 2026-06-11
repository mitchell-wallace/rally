## ADDED Requirements

### Requirement: Run-environment context
When enabled, the system SHALL attach a run-environment context to the relay trace and
to every captured failure, carrying the rally version, operating system, architecture,
and a coarse terminal descriptor (the value of `$TERM`, or a non-TTY marker when stdout
is not a terminal). The system SHALL NOT include hostname, username, or network
identity in this context.

#### Scenario: Failure carries environment context
- **WHEN** a failure is captured with telemetry enabled
- **THEN** the event SHALL include the rally version, OS, architecture, and terminal descriptor

#### Scenario: Environment context omits host identity
- **WHEN** the run-environment context is built
- **THEN** it SHALL NOT contain hostname, username, or IP address

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
the machine-local identifier, the relay start date, and the local relay id, together
with the relay start timestamp and the machine-local identifier. The system SHALL
continue to emit the local `relay_id` tag for within-machine correlation.

#### Scenario: Relay carries a globally-unique identifier
- **WHEN** a relay starts with telemetry enabled
- **THEN** its trace and any failures SHALL be tagged with a globally-unique relay identifier, the relay start timestamp, and the machine-local identifier

#### Scenario: Local relay id retained
- **WHEN** the globally-unique identifier is attached
- **THEN** the local `relay_id` tag SHALL still be emitted

### Requirement: Agent state on captured failures
When a failure is captured, the system SHALL attach the failing try's agent state as
filterable scalar tags: the current attempt and retry budget, the stable failure
category (as defined by the error-categorisation taxonomy), and the agent-type
resilience state (active, probation, frozen, or benched) where known. When the failure
category is a usage/quota exhaustion, the system SHALL additionally attach the quota
scope and reset timing where the failure evidence provides them. The system SHALL read
these values from the failure evidence produced upstream and SHALL NOT re-classify the
failure. The harness and model SHALL remain available via the existing `runner` tag.

#### Scenario: Captured failure carries agent state
- **WHEN** an operator-worthy failure is captured
- **THEN** the event SHALL include the attempt, retry budget, failure category, and resilience state tags

#### Scenario: Usage-limit failure carries reset evidence
- **WHEN** a captured failure has a usage/quota-exhaustion category with reset evidence
- **THEN** the event SHALL additionally include the quota scope and reset timing as scalar tags

#### Scenario: Agent-class failures unaffected
- **WHEN** an agent-class try failure is recorded as a span/log rather than an Issue
- **THEN** the agent-state tags SHALL NOT change whether it is captured as an Issue

### Requirement: Raw limit-signal capture
When a captured failure's category is a provider-limit signal (usage limit, short rate
limit, or provider overload), the system SHALL attach the bounded raw signal and parsed
message from the failure evidence to the event as a context block, so the exact provider
response shapes observed in the field can be used to validate and normalize the
per-harness evidence parsers. The attached raw signal SHALL remain bounded, SHALL pass
through the PII scrubber, and SHALL NOT include prompt or transcript content.

#### Scenario: Limit failure carries the raw provider signal
- **WHEN** a failure with a usage-limit, short-rate-limit, or provider-overload category is captured with failure evidence present
- **THEN** the event SHALL include the bounded raw signal and parsed message as a context block

#### Scenario: Raw signal stays bounded and scrubbed
- **WHEN** the raw limit signal is attached
- **THEN** it SHALL be bounded in size, SHALL pass through the `before_send` scrubber, and SHALL NOT contain prompt or transcript content

#### Scenario: Non-limit categories attach no raw signal
- **WHEN** a captured failure has a category that is not a provider-limit signal
- **THEN** the system SHALL NOT attach a raw-signal context block

## MODIFIED Requirements

### Requirement: Telemetry PII scrubbing
The system SHALL apply a `before_send` scrubber that prevents large or sensitive
payloads from being transmitted. The scrubber SHALL never send the contents of
`current_task.md` or full agent transcripts; only summaries and metadata SHALL be sent.
The system SHALL additionally collapse the user's home-directory prefix in any
transmitted working-directory or path-shaped field (e.g. `/home/<user>/…` → `~/…`) so
the username is not transmitted, and SHALL NOT transmit hostname, username, or network
identity in any event.

#### Scenario: Task prompt never shipped
- **WHEN** an event would otherwise include `current_task.md` contents or a full transcript
- **THEN** the scrubber SHALL remove that payload before the event is sent

#### Scenario: Working directory is username-stripped
- **WHEN** an event includes the working directory or a path under the user's home directory
- **THEN** the scrubber SHALL collapse the home prefix to `~` so the username is not transmitted

#### Scenario: Host identity never shipped
- **WHEN** any event is assembled
- **THEN** it SHALL NOT contain hostname, username, or IP address
