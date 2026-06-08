## Why

Recent real Rally relays showed the failure taxonomy is too coarse and sometimes
actively misleading. The labels drive operator decisions, retry timing, benching,
and Sentry issues — when they are wrong, Rally wastes tries, waits the wrong
duration, routes poorly, and is harder to debug after the fact. The objective is
**cleaner, more consistent, and more understandable error handling and
reporting**: one taxonomy, one priority order, and one typed evidence shape used
everywhere a failure is decided, displayed, or recorded.

Concretely, today (`internal/reliability/patterns.go`, `internal/relay/runner.go`):

- `rate limit` is one bucket for five conditions (short throttle, provider
  overload, multi-day subscription exhaustion, proxy block, invalid-model
  fallback) that need different actions.
- **`incomplete` wins first.** `ClassifyError` checks the dirty-tree context
  (`patterns.go:241`) before the pattern table (`patterns.go:250`), so a dirty
  harness-local file (e.g. `.claude/settings.local.json`) masks a stronger
  `RESOURCE_EXHAUSTED` or `model_not_found` as `incomplete`.
- **Patterns are global substring matches** (`containsSubstring`), not
  harness-scoped, so a Codex run gets labelled `claude rate-limit interrupt` from
  incidental log prose.
- Classification reads arbitrary text only; harnesses that emit structured error
  events get no benefit.
- Display label and retry strategy are coupled to one `Reason` string, so machine
  consumers (telemetry, scheduler) must string-match.

## What Changes

- **A typed failure taxonomy** of stable `FailureCategory` values
  (`usage_limit`, `short_rate_limit`, `provider_overloaded`, `invalid_model`,
  `auth_or_proxy`, `harness_launch`, `incomplete_finalization`, `agent_error`)
  with short display labels, replacing the overloaded `rate limit` pattern names.
- **Reordered classification priority**: structured executor evidence and
  provider/config/quota detection beat the dirty-tree `incomplete` check, so
  harness-local dirty files never mask a stronger failure.
- **Harness-scoped patterns**: classification is given the failing harness so a
  Codex failure can never be labelled a Claude rate limit; harness names are
  stripped from generic display labels.
- **Typed `FailureEvidence` on `TryResult`**, executor-populated where possible
  with a runner-side log-tail fallback, so display, retry, scheduler, and
  telemetry read structured fields (category, reset/retry timing, quota scope)
  instead of re-parsing a string.
- **Usage-limit benching with reset recovery**: a `usage_limit` benches the
  affected quota scope until its parsed reset (or a long conservative default),
  instead of looping one-minute waits, and the scheduler recovers the bench when
  the reset passes.
- **Harness-aware quota scope**: a single `QuotaScope(harness, model)` resolver
  groups route entries that share an account/quota bucket so benching one model
  benches the whole bucket.
- **A single shared dirty-path exclusion helper** in `internal/gitx` covering
  `.rally/`, `.laps/`, and known harness-local transient paths.
- **Resume guidance on incomplete retries** carries explicit finalization
  instructions.

## Capabilities

### Modified Capabilities
- `relay-runner`: failure detection gains the typed taxonomy orthogonal to the
  existing three-value `FailureClass`; incomplete classification is reordered
  below provider/config/quota evidence and uses the shared exclusion helper.
- `executor`: `TryResult` gains an optional `FailureEvidence` field that
  executors populate where they can.

### Added Capabilities
- `relay-runner`: harness-scoped classification, the failure taxonomy and its
  Category→FailureClass mapping, terminal-category attempt-loop short-circuit,
  usage-limit benching with reset recovery, harness-aware quota scope, and
  categorised failure display.

## Impact

- **Code**: `internal/reliability/patterns.go` (taxonomy, harness-scoped
  patterns, reordered `ClassifyError`, evidence parsing), `internal/gitx/git.go`
  (shared exclusion helper), `internal/relay/runner.go` (`runOne` return
  contract, attempt-loop short-circuit, classification wiring),
  `internal/routing/scheduler.go` (reset-deadline state, scope-keyed bench),
  `internal/relay/route_runtime.go` (`syncRecoverySignals` bench guard,
  `selectionWaitError` reset wait), `internal/relay/resilience.go` /
  `internal/store` (persisted reset event), `internal/agent/executor.go`
  (`FailureEvidence` on `TryResult`) and the per-harness adapters that can supply
  evidence.
- **Behavior**: usage limits bench rather than loop; harness-local dirt no longer
  masks provider/config errors; failure labels are accurate and harness-correct.
- **Out of scope**: the full harness-adapter normalization and conformance suite
  (`improve-harness-consistency`); a per-relay runner-disable UI (`build-new-tui`);
  which failures become Sentry Issues vs spans (telemetry spec).
- **Coordination**: `enrich-failure-telemetry` consumes this change's
  `FailureCategory`, `quota_scope`, reset evidence, and the **benched** state.
  `improve-harness-consistency` moves `FailureEvidence` *population* fully into the
  adapters under a conformance suite — this change keeps the runner-side fallback
  parser so that change has a clear before/after.
