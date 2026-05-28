## 1. Lap-ID pinning (state integrity)

- [ ] 1.1 Pin the assigned lap ID at run start (carry on the run/RunState)
- [ ] 1.2 On finalization (`laps wrapup`/run end), compare recorded-completed laps against the pinned ID in `internal/relay/runner.go`
- [ ] 1.3 Fail the run with `wrong_lap_consumed` / `multi_lap_consumed` reasons and do not advance the queue on a mismatch
- [ ] 1.4 Add a `laps_attempted` field to `TryRecord` (list of `{lap_id, timestamp}`) and record every lap completion attempt
- [ ] 1.5 Tests: phantom-completion rejection, multi-lap rejection, normal single-lap pass-through, attempted-lap recording

## 2. "Incomplete" failure class

- [x] 2.1 Add an `incomplete` failure class to `internal/reliability/patterns.go` alongside infra/agent
- [ ] 2.2 In `internal/relay/runner.go`, classify a try as incomplete when it produces file changes (dirty working tree) but the agent did not finalize the lap (no `laps done`/`laps handoff` detected)
- [ ] 2.3 Suppress auto-commit for incomplete tries — leave changes uncommitted so the retry agent inherits them as partial progress
- [ ] 2.4 On retry after incomplete, inject prompt guidance: "The last run was incomplete. Check any current git changes, finish anything not done, verify correctness, commit when good, then run `laps done`."
- [ ] 2.5 Incomplete runs retry but do NOT call `PauseAgent`/`RecordHourlyFailure` (do not advance the resilience cascade)
- [ ] 2.6 Tests: incomplete run leaves changes uncommitted; retry agent gets guidance; incomplete does not count toward pause/freeze

## 3. Probation state + freeze decay

- [x] 3.1 Add `StateProbation` to `internal/relay/resilience.go` state machine
- [x] 3.2 Centralize resilience constants in a single location (`internal/relay/constants.go` or similar): `FreezeDuration` (5h), `PauseDuration` (1h), `HourlyRetriesBeforeFreeze` (5), `HourlyRetryMaxAttempts` (3)
- [x] 3.3 Make `getState` return `StateProbation` when a frozen event is older than `FreezeDuration`; `getState` remains a pure read function (no side effects)
- [x] 3.4 One-shot enforcement: `syncRecoverySignals` persists a probation event and unbenches the scheduler entry when the frozen→probation transition is first observed. The entry remains selectable while state is probation; the once-per-cycle guarantee is enforced by the probation event guard and `runOne` writing an active or frozen event when the run resolves.
- [x] 3.5 Persist probation event (`event_type: "probation"`) exactly once in `syncRecoverySignals` when it first observes a key transitioning from frozen to probation
- [x] 3.6 Probation semantics in `runOne`: `maxAttempts=3` (same as hourly retries); success or incomplete → promote to active; failure (agent or infra) → re-freeze with fresh timestamp
- [x] 3.7 Bump `agentStatusWindowSize` from 50 to 500 events; on truncation, synthesize a summary event preserving the latest effective state + timestamp for active frozen/probation entries
- [x] 3.8 Tests: frozen decays to probation after window; probation one-shot enforcement; probation success → active; probation incomplete → active; probation failure → re-frozen; all-frozen ends pass but remains decayable; window truncation preserves freeze timestamps
- [ ] 3.9 Baseline tests for `resilience.go`: create `internal/relay/resilience_test.go` covering `getState` state transitions, `PauseAgent`/`UnpauseAgent`/`FreezeAgent` event recording, `RecordHourlyFailure` threshold + counting-loop boundaries, and `SelectActiveAgent` cycling (run before probation work begins)

## 4. `--new` explicitly resets agent status

- [ ] 4.1 Add `store.ResetAgentStatus()` that truncates agent status history for a clean slate
- [ ] 4.2 Route `--new` in `cmd/rally/main.go` through `ResetAgentStatus()` so all harness-model pairs start active
- [ ] 4.3 Tests: `--new` starts all agents active regardless of prior frozen/probation/paused history

## 5. Failure classification (per-harness-model, >1 infra threshold)

- [x] 5.1 Extend `internal/reliability/patterns.go` `ClassifyError` with infra/agent/incomplete distinction; add patterns for harness/launch errors (`argument list too long`, `fork/exec`), API timeouts/network stalls, rate limits, and stall detection
- [x] 5.2 Define `ResilienceKey{Harnes, Model}` type in `internal/relay/resilience.go`; update every method signature: `getState(key)`, `PauseAgent(key, relayID)`, `RecordHourlyFailure(key, relayID)`, `FreezeAgent(key, relayID)`, `UnpauseAgent(key, relayID)`, `SelectActiveAgent(mix, runIndex)` (uniqueness map keys on `harness:model`)
- [x] 5.3 Add optional `model` field to `AgentStatusEvent`; update `GetAgentStatus` to accept model parameter and filter on both `agent_type` and `model` when model is present
- [x] 5.4 Update all callers of resilience methods in `internal/relay/runner.go` and `internal/relay/route_runtime.go` to pass `selection.Agent.Model` (or resolved model); enumerate all 10 method signatures and 20+ call sites
- [ ] 5.5 In `internal/relay/runner.go`, only call `PauseAgent`/`RecordHourlyFailure` when >1 attempt within a run is classified as infra-class; agent-class and incomplete failures stay retry-eligible but do not escalate
- [x] 5.6 Default unknown failures to the agent-class (does-not-freeze) side
- [ ] 5.7 Tests: >1 infra failure increments cascade; single infra failure does not; agent error and incomplete do not; per-harness-model keying isolates rate-limit to specific model

## 6. Hourly retries up to 3 attempts

- [ ] 6.1 Set `maxAttempts=3` on the hourly retry path in `internal/relay/runner.go` (the `isHourlyRetry` path)
- [ ] 6.2 Tests: a single transient failure during an hourly retry does not burn a freeze life; retry budget of 3 honored

## 7. Role-aware stall-recovery

- [ ] 7.1 Gate the "files committed → success" stall-recovery in `internal/relay/runner.go` on role: VERIFY is excluded (a stalled VERIFY try is never auto-accepted on the basis of commits); implementation roles keep current behavior
- [ ] 7.2 Tests: stalled VERIFY (even with a committed trivial fix) stays a retry-eligible failure; stalled implementation try with commits still recovers

## 8. Bounded prompt context

- [ ] 8.1 Add config under `[reliability]` in config.toml: `recent_try_count` (default 5), `recent_try_char_limit` (per-summary), `recent_context_char_limit` (overall)
- [ ] 8.2 Apply count + char budget with head/tail truncation where `recentContext` is built (`internal/relay/runner.go:~581`)
- [ ] 8.3 Tests: verbose summaries truncated; count honored; budgets enforced

## 9. Naming disambiguation (clarity refactor)

- [x] 9.1 Rename the liveness detector freeze→stall: `internal/reliability/freeze.go` (`StallDetector`, `Assessment.Stalled`, threshold/field names, callers in `internal/relay/runner.go`)
- [x] 9.2 Rename scheduler `EntryState.Frozen`→`Benched` in `internal/routing/scheduler.go` and callers (keep `Exhausted`); rename `failureFreezesEntry`→`failureBenchesEntry` and replace string-matching with an explicit enum/boolean parameter on `OnAgentFailed`; update `AllExhausted` reference; clarify `syncRecoverySignals` `StatePaused` boolean logic (`!state.Frozen && !state.Exhausted` → explicit `!(state.Frozen && state.Exhausted)`)
- [x] 9.3 Keep the per-agent-type `frozen` name and `agent_status.jsonl` `event_type` value unchanged (no data-format change)
- [x] 9.4 Update `RecordHourlyFailure` counting loop to also break on `frozen` and `probation` events (not just `active`), avoiding cross-cycle counting bugs
- [ ] 9.5 Update references in `AGENTS.md`/specs/tests so the three concepts (stall / frozen / benched) read distinctly

## 10. Docs & coordination

- [ ] 10.1 Update `AGENTS.md`/role-doc references if stall-recovery behavior is documented there
- [ ] 10.2 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
