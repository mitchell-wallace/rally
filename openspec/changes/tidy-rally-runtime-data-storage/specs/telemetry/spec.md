## ADDED Requirements

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
When enabled, the system SHALL emit a structured log event per try, model a relay as a trace whose runs and tries are spans, and report genuine failures as Sentry Issues. Issues SHALL be reserved for failures that warrant operator attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and lap-integrity violations (`wrong_lap_consumed` / `multi_lap_consumed`). Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs, NOT Issues, to avoid alert noise. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

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

#### Scenario: Runner fallback recorded as recovery
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a recovery span/breadcrumb and SHALL NOT capture it as an Issue (rotating to a backup runner is a healthy recovery, not an alert)

### Requirement: Prompt-size observability
When enabled, the system SHALL record the assembled-prompt size for each try and a breakdown of how much each source contributes (e.g. recent-try context, previous summary, project instructions, role instructions, task prompt, inbox/relay messages), so runaway prompt growth is detectable without reading transcripts.

#### Scenario: Prompt size emitted per try
- **WHEN** a try's prompt is assembled and the try is logged
- **THEN** the structured log event SHALL include the total assembled-prompt size and a per-source size breakdown

#### Scenario: Breakdown attributes each source
- **WHEN** the prompt-size breakdown is emitted
- **THEN** it SHALL attribute byte counts to each prompt source so a dominant source can be identified

### Requirement: Telemetry tagging and correlation
Every telemetry event SHALL be tagged with `relay_id`, `run_id`, `try_id`, `role`, `runner` (harness+model), `repo`, and `lap_ids` where applicable, so events are filterable and correlate with the local `summary.jsonl` digest.

#### Scenario: Events carry correlation tags
- **WHEN** any telemetry event is emitted during a try
- **THEN** it SHALL include the available `relay_id`, `run_id`, `try_id`, `role`, `runner`, `repo`, and `lap_ids` tags

### Requirement: Telemetry PII scrubbing
The system SHALL apply a `before_send` scrubber that prevents large or sensitive payloads from being transmitted. The scrubber SHALL never send the contents of `current_task.md` or full agent transcripts; only summaries and metadata SHALL be sent.

#### Scenario: Task prompt never shipped
- **WHEN** an event would otherwise include `current_task.md` contents or a full transcript
- **THEN** the scrubber SHALL remove that payload before the event is sent

### Requirement: Telemetry flush on exit
The system SHALL flush pending telemetry before the CLI process exits, using a bounded timeout so a slow or unreachable network never blocks exit.

#### Scenario: Buffered events flushed
- **WHEN** the rally CLI is about to exit with telemetry enabled
- **THEN** the system SHALL flush buffered events with a bounded timeout (e.g. 2 seconds)

#### Scenario: Unreachable network does not hang exit
- **WHEN** flushing cannot complete because the network is unreachable
- **THEN** the system SHALL stop waiting after the bounded timeout and exit
