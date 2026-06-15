## MODIFIED Requirements

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL emit a structured log event per try, model a relay as a trace whose runs and tries are spans, and report genuine failures as Sentry Issues. Issues SHALL be reserved for failures that warrant operator attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and lap-integrity violations (`wrong_lap_consumed` / `multi_lap_consumed`). Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs, NOT Issues, to avoid alert noise. The handoff lifecycle outcomes (`TryOutcome` `handoff_requested` and `handoff_timeout`) SHALL be recorded as spans/logs and SHALL NOT be captured as Issues — a timeout-driven handoff is a designed recovery path (its response is a RECOVERY route), not an alert. A `needs_user` recovery classification or relay-synthesized cap signal MAY be captured as an Issue, since it is the escape hatch reserved for decisions wanting operator attention; this covers both a RECOVERY agent's recorded `needs_user` classification and a relay-synthesized `needs_user` raised when the consecutive-recovery cap is reached. The other four recovery classifications SHALL be recorded as spans/logs. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

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
- **WHEN** a try resolves as `handoff_requested` or `handoff_timeout`
- **THEN** the sink SHALL record it as a span/log and SHALL NOT capture an Issue

#### Scenario: needs_user recovery may raise an Issue
- **WHEN** a RECOVERY run records the `needs_user` classification, or the relay synthesizes `needs_user` on reaching the consecutive-recovery cap
- **THEN** the sink MAY capture an Issue so the deferred decision is surfaced for operator attention

#### Scenario: Runner fallback recorded as common event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after the current entry becomes unavailable
- **THEN** the sink SHALL record the rotation as a common recovery log event and SHALL NOT capture it as an Issue (rotating to a backup runner is a healthy recovery, not an alert)

### Requirement: Agent state on captured failures
When a try failure is captured, the system SHALL attach the failing try's agent state as filterable scalar tags: the current attempt and retry budget, the `TryOutcome` lifecycle value, the stable failure category (as defined by the error-categorisation taxonomy) for a `failed` outcome, and the agent-type resilience state (active, probation, frozen, or benched) where known. Each try event SHALL carry the `outcome` tag, and a `failed` outcome SHALL additionally carry the existing `failure_category` tag; lifecycle outcomes such as `handoff_timeout` and `handoff_requested` SHALL be distinguished by the `outcome` tag rather than a failure category. When the failure category is a usage/quota exhaustion, the system SHALL additionally attach the quota scope and reset timing where the failure evidence provides them. When a try runs under the `recovery` route, the system SHALL additionally attach the `recovery_classification` scalar tag where the recovery agent recorded one. The system SHALL read these values from the failure evidence / recorded outcome produced upstream and SHALL NOT re-classify the try. The harness and model SHALL remain available via the existing `runner` tag. Relay-level captures that have no failing try SHALL attach relay-level state only and SHALL omit try-only fields.

#### Scenario: Captured failure carries agent state
- **WHEN** an operator-worthy failure is captured
- **THEN** the event SHALL include the attempt, retry budget, `outcome`, failure category (for a `failed` outcome), and resilience state tags

#### Scenario: Lifecycle outcome distinguished by the outcome tag
- **WHEN** a try resolves with a `handoff_timeout` or `handoff_requested` outcome
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
