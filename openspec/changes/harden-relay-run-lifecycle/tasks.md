## 1. Lap-ID pinning (state integrity)

- [ ] 1.1 Pin the assigned lap ID at run start (carry on the run/RunState)
- [ ] 1.2 On finalization (`laps wrapup`/run end), compare recorded-completed laps against the pinned ID in `internal/relay/runner.go`
- [ ] 1.3 Fail the run with `wrong_lap_consumed` / `multi_lap_consumed` reasons and do not advance the queue on a mismatch
- [ ] 1.4 Add a `laps_attempted` field to `TryRecord` (list of `{lap_id, timestamp}`) and record every lap completion attempt
- [ ] 1.5 Tests: phantom-completion rejection, multi-lap rejection, normal single-lap pass-through, attempted-lap recording

## 2. "Incomplete" failure class

- [ ] 2.1 Add an `incomplete` failure class to `internal/reliability/patterns.go` alongside infra/agent
- [ ] 2.2 In `internal/relay/runner.go`, classify a try as incomplete when it produces file changes (commits) but the agent did not finalize the lap (no `laps done`/`laps handoff` detected)
- [ ] 2.3 Incomplete runs retry but do NOT call `PauseAgent`/`RecordHourlyFailure` (do not advance the cascade)
- [ ] 2.4 Tests: incomplete run retries; incomplete does not count toward pause/freeze

## 3. Probation state + freeze decay

- [ ] 3.1 Add `StateProbation` to `internal/relay/resilience.go` state machine
- [ ] 3.2 Add `FreezeDuration` to resilience config (default 5h, under `[reliability]` in config.toml)
- [ ] 3.3 Make `getState` return `StateProbation` when a frozen event is older than `FreezeDuration`
- [ ] 3.4 Probation semantics in `SelectActiveAgent`: eligible for one run; success → promote to active (`UnpauseAgent`); failure → re-freeze with fresh timestamp
- [ ] 3.5 Make `syncRecoverySignals` (`internal/relay/route_runtime.go`) reflect probation in scheduler entries (similar to paused handling)
- [ ] 3.6 Update `agent_status.jsonl` reconstruction in `internal/store/store.go` to honor freeze decay and probation
- [ ] 3.7 Tests: frozen decays to probation after window; probation run success → active; probation run failure → re-frozen; all-frozen ends pass but remains decayable

## 4. `--new` explicitly resets agent status

- [ ] 4.1 Add `store.ResetAgentStatus()` that truncates agent status history for a clean slate
- [ ] 4.2 Route `--new` in `cmd/rally/main.go` through `ResetAgentStatus()` so all harness-model pairs start active
- [ ] 4.3 Tests: `--new` starts all agents active regardless of prior frozen/probation/paused history

## 5. Failure classification (per-harness-model, >1 infra threshold)

- [ ] 5.1 Extend `internal/reliability/patterns.go` `ClassifyError` with infra/agent/incomplete distinction; add patterns for harness/launch errors (`argument list too long`, `fork/exec`), API timeouts/network stalls, rate limits, and stall detection
- [ ] 5.2 Add per-harness-model keying: rate-limit flags keyed on `harness:model`, not just `harness`; add optional `model` field to `AgentStatusEvent`
- [ ] 5.3 In `internal/relay/runner.go`, only call `PauseAgent`/`RecordHourlyFailure` when >1 attempt within a run is classified as infra-class; agent-class and incomplete failures stay retry-eligible but do not escalate
- [ ] 5.4 Default unknown failures to the agent-class (does-not-freeze) side
- [ ] 5.5 Tests: >1 infra failure increments cascade; single infra failure does not; agent error and incomplete do not

## 6. Hourly retries up to 3 attempts

- [ ] 6.1 Set `maxAttempts=3` on the hourly retry path in `internal/relay/runner.go` (the `isHourlyRetry` path)
- [ ] 6.2 Tests: a single transient failure during an hourly retry does not burn a freeze life; retry budget of 3 honored

## 7. Role-aware stall-recovery

- [ ] 7.1 Add `.rally/state/verify-reports.jsonl` store (append-only JSONL, fields: `lap_id`, `verdict` [pass/fail], `timestamp`, `relay_id`, `summary`)
- [ ] 7.2 Gate the "files committed → success" stall-recovery in `internal/relay/runner.go` on role: VERIFY requires a verdict artifact in `verify-reports.jsonl`; implementation roles keep current behavior
- [ ] 7.3 Tests: stalled VERIFY without verdict stays failed; stalled VERIFY with pass verdict recovers; stalled implementation try with commits still recovers

## 8. Bounded prompt context

- [ ] 8.1 Add config under `[reliability]` in config.toml: `recent_try_count` (default 5), `recent_try_char_limit` (per-summary), `recent_context_char_limit` (overall)
- [ ] 8.2 Apply count + char budget with head/tail truncation where `recentContext` is built (`internal/relay/runner.go:~581`)
- [ ] 8.3 Tests: verbose summaries truncated; count honored; budgets enforced

## 9. Naming disambiguation (clarity refactor)

- [ ] 9.1 Rename the liveness detector freeze→stall: `internal/reliability/freeze.go` (`StallDetector`, `Assessment.Stalled`, threshold/field names, callers in `internal/relay/runner.go`)
- [ ] 9.2 Rename scheduler `EntryState.Frozen`→`Benched` in `internal/routing/scheduler.go` and callers (keep `Exhausted`); rename `failureFreezesEntry`→`failureBenchesEntry` and update reason matching
- [ ] 9.3 Keep the per-agent-type `frozen` name and `agent_status.jsonl` `event_type` value unchanged (no data-format change)
- [ ] 9.4 Update references in `AGENTS.md`/specs/tests so the three concepts (stall / frozen / benched) read distinctly

## 10. Docs & coordination

- [ ] 10.1 Update `AGENTS.md`/role-doc references if stall-recovery or VERIFY-verdict behavior is documented there
- [ ] 10.2 Coordinate with `tidy-rally-runtime-data-storage`: this change adds `laps_attempted` to `TryRecord` and `verify-reports.jsonl` to the store; `tidy` may restructure later
- [ ] 10.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
