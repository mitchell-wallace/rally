## MODIFIED Requirements

### Requirement: Telemetry event taxonomy
When enabled, the system SHALL emit a structured custom event per try, model relay/run/try timing through the active telemetry sink's span abstraction, report genuine failures as backend-neutral operator-worthy failure events, and record routing decisions as a dedicated `RallyRoute` custom event (distinct from `RallyTry`). Operator-worthy failures SHALL be reserved for failures that warrant attention: infra-class failures (rate limit, harness/launch error, API timeout), a relay ending with all agent types frozen (stall), panic, "agent exited without finalizing", detection of `laps done` emitted as text, and lap-integrity violations that genuinely leave the queue unsafe. Lap-integrity mismatches (`wrong_lap_consumed` / `multi_lap_consumed`) SHALL be recorded as `LevelWarning` diagnostic events and SHALL carry `event_kind=lap_pin_mismatch` and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`, but SHALL NOT be captured as operator-worthy failures by default and SHALL NOT attach `failure_category` because 0.9.x reserves `failure_category` for failed lifecycle outcomes. Operator-cancelled attempts SHALL be recorded as cancellation telemetry or spans/logs only and SHALL NOT be captured as operator-worthy failures. The timeout/handoff lifecycle outcomes (`TryOutcome` `run_timeout`, `handoff_requested`, and `handoff_timeout`) SHALL be recorded as spans/logs/custom events and SHALL NOT be captured as operator-worthy failures. A `needs_user` recovery classification or relay-synthesized cap signal MAY be captured as an operator-worthy failure, while the other recovery classifications SHALL remain spans/logs/custom events. Ordinary agent-class try failures (recoverable agent errors, short no-ops) SHALL be recorded as spans/logs/custom events, NOT operator-worthy failures, to avoid alert noise. Failure classification (infra vs agent) is defined by the `harden-relay-run-lifecycle` change.

Non-limit failure evidence MAY be attached to `RallyTry` events and spans when
the runner has bounded, scrubbed evidence from `FailureEvidence`, but ordinary
agent-class failures SHALL still not become `RallyFailure` events. `RallyFailure`
MAY include non-limit evidence only when the failure was already
operator-worthy under this requirement, such as infra-class failure, harness
launch/exec error, marker-as-text, panic, or `needs_user`. Raw evidence SHALL be
sanitized before emission: prompt/current_task/transcript-looking fields and
full `output:` / `stderr:` sections SHALL NOT be emitted as structured
telemetry fields. Lap IDs are not sensitive and MAY be emitted.

Routing decisions (route fallback, recovery-cap-hit) SHALL be recorded as
`RallyRoute` custom events, NOT as `RallyTry` events. A `RallyRoute` event
SHALL carry `relay_id`, `run_id`, `lap_id`, `role`, `from_runner`,
`to_runner`, `repo`, `repo_name`, and any fallback-cause fields, plus the
fixed `event = "route_fallback"` discriminator. It SHALL NOT carry
`outcome`, `attempt`, `try_id`, or other try-only fields, because the
routing decision happens outside any try scope.

#### Scenario: Try emits a structured log
- **WHEN** a try is appended via the store
- **THEN** the sink SHALL emit a `RallyTry` structured event for that try
- **AND** the event SHALL carry a non-empty `outcome` tag

#### Scenario: Relay emits a trace hierarchy
- **WHEN** a relay starts and runs/tries execute
- **THEN** the sink SHALL produce a relay/run/try timing hierarchy through its span abstraction

#### Scenario: Infra failure becomes operator-worthy telemetry
- **WHEN** a try fails with an infra-class failure mode
- **THEN** the sink SHALL capture an operator-worthy `RallyFailure` event describing the failure

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

#### Scenario: Route fallback is a RallyRoute event
- **WHEN** the routing scheduler rotates a lane to the next runner entry after a failed or unavailable prior run, or hits a recovery cap
- **THEN** the sink SHALL emit a `RallyRoute` custom event carrying `from_runner`, `to_runner`, fallback cause, `relay_id`, `run_id`, `lap_id`, `role`, `repo`, and `repo_name`
- **AND** it SHALL NOT emit a `RallyTry` event for the routing decision
- **AND** no `RallyTry` event SHALL have a NULL `outcome` tag

### Requirement: Telemetry tagging and correlation
Every telemetry event SHALL be tagged with `relay_id`, `run_id`, `try_id`, `role`, `runner` (harness+model), `repo`, and `lap_id` where applicable, so events are filterable and correlate with the local `summary.jsonl` digest. The `runner` tag SHALL always include the model component when the executor resolved one, even for routes configured with a bare alias; the runner SHALL use the executor-reported `ResolvedModel` for the tag and SHALL NOT collapse the tag to the bare harness name when a model is available.

#### Scenario: Events carry correlation tags
- **WHEN** any telemetry event is emitted during a try
- **THEN** it SHALL include the available `relay_id`, `run_id`, `try_id`, `role`, `runner`, `repo`, and `lap_id` tags

#### Scenario: Runner tag carries the resolved model
- **WHEN** a try runs on a route configured with a bare alias and the executor resolved a default model
- **THEN** the `runner` tag on every event for that try SHALL be `<harness>:<resolved-model>`
- **AND** it SHALL NOT be the bare harness name

### Requirement: Raw limit-signal capture
The system SHALL attach the bounded raw signal and parsed message from the failure
evidence to an info-level diagnostic event as a context block whenever a failed try's
category is a provider-limit signal (usage limit, short rate limit, or provider
overload), so the exact provider response shapes observed in the field can be used to
validate and normalize the per-harness evidence parsers. This diagnostic event SHALL be
emitted independently of whether the failure is captured as an operator-worthy Issue.
The attached raw signal SHALL remain bounded, SHALL pass through the PII scrubber, and
SHALL NOT include prompt or transcript content.

For non-limit categories, the system SHALL populate the `failure_evidence` context
block on `RallyFailure` events regardless of which classification path produced the
category: `executor_evidence`, `dirty_tree`, `text_pattern`, `unmatched`,
`codex_session_log`, `codex_no_session_log`, `opencode_disk_log`, and the existing
`safe_exec_error` source. Every categorised `RallyFailure` event SHALL be
self-contained: an operator running `SELECT latest(failure_evidence.raw_signal),
latest(failure_evidence.message), latest(failure_evidence.source) FROM RallyFailure`
SHALL receive a non-empty result for every row.

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

#### Scenario: Non-limit failure evidence is populated for every source
- **WHEN** a `RallyFailure` event is captured for a non-limit category and the classification path was Priority 3, 4, or 5 (dirty-tree, text-pattern, or default)
- **THEN** the `failure_evidence` context block SHALL be populated with `source`, `message`, and a bounded `raw_signal`
- **AND** the `source` SHALL be `dirty_tree`, `text_pattern`, `unmatched`, or a session/disk-log source depending on which path produced the category

#### Scenario: Session-log and disk-log sources are surfaced
- **WHEN** a failure's evidence was populated from the codex session log or the opencode disk log
- **THEN** the `failure_evidence.source` SHALL be `codex_session_log`, `codex_no_session_log`, or `opencode_disk_log`
- **AND** the source SHALL be distinguishable from the in-band `executor_evidence` and `safe_exec_error` sources in NRQL

## ADDED Requirements

### Requirement: RallyRoute custom event
The system SHALL emit a `RallyRoute` custom event for routing-decision moments that are not try outcomes: route fallback from one runner entry to the next, and the recovery-cap-hit signal. The event SHALL carry the fixed discriminator `event = "route_fallback"`, the routing context (`from_runner`, `to_runner`, `role`, `lap_id`, `repo`, `repo_name`, `relay_id`, `run_id`), and any fallback-cause fields the runner already computes (triggering run/try id, triggering outcome, fail reason, failure class/category where known, route name, route-entry exhausted reason). The event SHALL NOT carry `outcome`, `attempt`, `try_id`, or other try-only fields. The runner SHALL NOT emit a `RallyTry` event for any routing decision.

#### Scenario: Route fallback emits RallyRoute
- **WHEN** the routing scheduler rotates a lane to the next runner entry after a failed or unavailable prior run
- **THEN** the sink SHALL emit a `RallyRoute` event with `event = "route_fallback"` and the routing context
- **AND** no `RallyTry` event SHALL be emitted for the rotation

#### Scenario: Recovery cap hit emits RallyRoute
- **WHEN** a relay's recovery cap is reached and the lap is classified `needs_user`
- **THEN** the sink SHALL emit a `RallyRoute` event carrying the cap-hit context
- **AND** the operator-worthy `needs_user` `RallyFailure` (if emitted) SHALL remain a separate event

#### Scenario: RallyTry is never emitted without an outcome
- **WHEN** the runner constructs the fields for a `RallyTry` event
- **THEN** the `outcome` field SHALL be a non-empty string from the `TryOutcome` taxonomy
- **AND** the telemetry sink SHALL NOT accept a `RallyTry` emission with an empty or missing `outcome`
