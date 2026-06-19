# 0.10.1 Telemetry Follow-up Plan

## Context

New Relic review after the 0.10.0 release found that most actionable signal is
in Rally custom events (`RallyTry`, `RallyFailure`, `RallyDiagnostic`), not in
standard APM `Transaction`, `TransactionError`, or `Span` rows. The data window
is mostly pre-0.10.0, but it exposed observability gaps that are still worth
closing in 0.10.1:

- `route_fallback` events do not explain which try/failure triggered rotation.
- `agent_error` is too broad for launcher/parser/provider failures.
- lap-pin diagnostics need more lap context.
- timeout and handoff-timeout records need enough context to distinguish
  intentional budget expiry, stale silence, missing session capture, and failed
  handoff-only continuation.
- provider-limit evidence can include transcript-shaped raw text; keep bounded
  evidence, but prefer compact provider error signals when available.

Product decision already made: lap IDs are not sensitive. They may be emitted in
diagnostic and try/failure telemetry. Continue scrubbing paths, prompts,
transcripts, and secrets.

## Implementation Shape

### 1. Route fallback cause telemetry

Extend the existing `route_fallback` `EmitTryLog` event in
`internal/relay/runner.go`; do not create a new event stream.

Implementation:

- Add a small run-loop-local `fallbackCause` snapshot updated after each
  unsuccessful `runOne` result and cleared after it is consumed by the next
  fallback event. Key the snapshot to the previous selected runner/run and clear
  it on any next selection that does not emit a fallback for that exact previous
  runner, so stale causes cannot attach to unrelated later rotations.
- Capture from the failed run result/store:
  - `trigger_run_id`
  - `trigger_try_id` from `relay.LastTryID` / `store.NextTryID()-1`
  - `trigger_outcome`
  - `trigger_fail_reason`
  - `trigger_failure_class`
  - `trigger_failure_category`
  - `trigger_lap_id`
  - `route_name`
  - `route_entry_exhausted_reason` using the same coarse vocabulary already
    used by scheduler calls (`retry-budget-exhausted`, `stall`, `skip`, or
    `category:<failure_category>` where applicable)
- Add the same fields to the run span where scalar tags make sense.
- Tests: extend route fallback telemetry tests in `internal/relay/runner_test.go`
  or `internal/relay/runner_failure_telemetry_test.go` to assert the fallback
  event carries the trigger fields and does not emit `RallyFailure`.

Acceptance:

- Fallback remains a healthy recovery event, not an operator-worthy failure.
- New Relic can facet fallback by trigger category/reason without joining local
  store files.

### 2. Agent-error evidence for unparseable exits

Preserve the stable `agent_error` category, but add bounded diagnostic fields so
generic failures become triageable.

Implementation:

- Add optional fields to `telemetry.FailureState` for bounded evidence already
  derivable from `reliability.FailureEvidence`:
  - `EvidenceMessage`
  - `EvidenceRawSignal`
  - `EvidenceSource`
- Replace the current limit-only `FailureEvidenceContext` behavior with:
  - limit categories keep `failure_evidence.message` and compact provider
    signal fields, plus reset/quota tags.
  - non-limit categories may attach `failure_evidence.message`,
    `failure_evidence.raw_signal`, and `failure_evidence.source` when the caller
    supplies them, but ordinary agent-class failures emit that evidence only on
    `RallyTry`/spans. `RallyFailure` receives non-limit evidence only for paths
    that are already operator-worthy today, such as infra-class failures,
    `execErr`, marker-as-text, panic, or `needs_user`.
  - no prompt/transcript-looking keys are ever emitted; content remains bounded
    and scrubbed.
- Populate from `agent.TryResult.Evidence` in `runOne` for all categories, not
  only provider limits. Use existing `reliability.FailureEvidence` as the source
  of truth; do not add exit-code/parser-error fields in 0.10.1 unless the
  executor result model already exposes them.
- For launcher/parser errors where no `FailureEvidence` exists, capture a bounded
  safe summary from the already-observed `execErr` or parsed result error path,
  after stripping transcript-shaped `output:`/`stderr:` sections. Do not attach
  full prompt/current_task/transcript content, even bounded.
- Tests:
  - `internal/telemetry/failure_state_test.go` for non-limit evidence context,
    scrubbing, truncation, and no prompt/transcript keys.
  - runner telemetry tests proving an ordinary `agent_error` with evidence emits
    bounded fields on `RallyTry`/spans but not `RallyFailure`, while existing
    issue-worthy failures still carry bounded evidence when captured.

Acceptance:

- `agent_error` stays the fallback category, but events show enough source
  evidence to decide whether to add a harness-specific parser later.

### 3. Lap-pin mismatch detail

Extend the existing `lap_pin_mismatch` `RallyDiagnostic` warning event.

Implementation:

- Add safe lap metadata:
  - `expected_lap_id`
  - `consumed_lap_count`
  - `consumed_lap_ids` as a comma-separated bounded scalar
  - `recorded_laps` / `laps_attempted` counts where already available
- Keep existing tags:
  - `event_kind=lap_pin_mismatch`
  - `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`
  - `level=warning`
- Do not attach `failure_category` unless there is a separate failed lifecycle
  outcome with a real category.
- Tests: extend existing lap mismatch telemetry tests to assert lap IDs and
  counts are present.

Acceptance:

- Operators can identify exactly which lap(s) were consumed from New Relic
  without opening local `.laps` state.

### 4. Timeout and handoff-timeout context

Extend the existing per-try telemetry fields in `runOne` and
`runBoundedHandoffOnly`.

Implementation:

- Emit timeout context on `RallyTry` and try span for `run_timeout`,
  `try_timeout`, `handoff_requested`, and `handoff_timeout` paths:
  - `timeout_kind=run_budget|try_cap|handoff`
  - `timeout_budget_ms`
  - `last_output_age_ms` when the monitor can supply it, otherwise omit
  - `session_captured=true|false`
  - `resume_supported=true|false`
  - `handoff_only_attempted=true|false`
  - `handoff_only_try_id` when created
  - `handoff_resume_blocker` using existing `noHandoffResumeReason`
- Reuse existing progress/run-state and monitor data; do not add a second
  timeout state machine.
- Tests:
  - timeout runOne tests assert run budget vs try cap fields.
  - bounded handoff tests assert no-session/no-resume blockers and
    handoff-only attempt metadata.

Acceptance:

- A timeout event explains whether Rally could resume for handoff, why not, and
  whether it was silence/stall-like or a hard configured budget.

### 5. Compact provider evidence preference

Keep raw evidence for parser improvement, but prefer compact provider error
objects over transcript-shaped fallback text.

Implementation:

- Add helper in `internal/telemetry/failure_state.go` that builds
  `failure_evidence.provider_signal` from `FailureEvidence.Message` /
  `RawSignal` when it looks like a structured provider error object.
- For raw transcript tails, retain only a bounded `raw_signal` fallback and add
  `evidence_shape=provider_object|transcript_tail|plain_text`.
- Tests: realistic Codex/Gemini/OpenCode snippets prove provider objects are
  preferred and transcript tails remain bounded/scrubbed.

Acceptance:

- New Relic evidence fields are useful for parser normalization without carrying
  unnecessarily large transcript fragments.

### 6. 0.10.1 version and release

After implementation and review:

- bump `internal/buildinfo/VERSION` from `0.10.0` to `0.10.1`;
- keep `main.Version == "dev"`;
- run `just test`, `just check`, and
  `openspec validate release-0-10-0-reliability-and-model-routing --strict`;
- release through the standard `dev -> main` flow;
- smoke test the published binary with `op:opencode/big-pickle`.

## Non-goals

- Do not change retry/freezing/benching semantics except where telemetry fields
  expose already-existing decisions.
- Do not create a new New Relic event type.
- Do not emit prompt/current_task/transcript content as structured fields.
- Do not treat route fallback or lap-pin mismatch as operator-worthy failures.
