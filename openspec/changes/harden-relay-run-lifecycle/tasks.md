## 1. Lap-ID pinning (state integrity)

- [ ] 1.1 Pin the assigned lap ID(s) at run start (carry on the run/RunState)
- [ ] 1.2 On finalization (`laps wrapup`/run end), compare recorded-completed laps against the pinned set in `internal/relay/runner.go`
- [ ] 1.3 Fail the run with `wrong_lap_consumed` / `multi_lap_consumed` reasons and do not advance the queue on a mismatch
- [ ] 1.4 Record attempted lap IDs (with timestamps), not only accepted ones
- [ ] 1.5 Tests: phantom-completion rejection, multi-lap rejection, normal single-lap pass-through

## 2. Completion file-change cross-check (opt-in)

- [ ] 2.1 Add an optional expected-files field on a lap (read where laps are loaded)
- [ ] 2.2 When present, verify those paths changed since run start before accepting `laps done`; warn/reject otherwise
- [ ] 2.3 When absent, skip the check (no behavior change)
- [ ] 2.4 Tests: modified → accept, untouched → reject/warn, no-field → skip

## 3. Freeze decay + recovery

- [ ] 3.1 Add `FreezeDuration` to the resilience config/defaults (`internal/relay/resilience.go`)
- [ ] 3.2 Make `getState` treat a frozen event older than `FreezeDuration` as active/probation (decay)
- [ ] 3.3 Make `syncRecoverySignals` (`internal/relay/route_runtime.go`) re-evaluate freeze rather than re-apply verbatim on resume/start
- [ ] 3.4 Update `agent_status.jsonl` reconstruction in `internal/store/store.go` to honor freeze decay
- [ ] 3.5 Tests: frozen decays to active after the window; resume re-evaluates; all-frozen ends the pass but remains decayable

## 4. `--new` resets agent status

- [ ] 4.1 In `cmd/rally/main.go` `--new` handling, reset pause/freeze state (append `active` events or truncate)
- [ ] 4.2 Add a `store.ResetAgentStatus` (or equivalent) and route `--new` through it
- [ ] 4.3 Tests: `--new` starts all agents active regardless of prior frozen history

## 5. Infra-only failure classification

- [ ] 5.1 Extend `internal/reliability/patterns.go` `ClassifyError` with an infra/agent distinction; add patterns for harness/launch errors (`argument list too long`), API timeouts/network stalls, and rate limits
- [ ] 5.2 In `internal/relay/runner.go`, only call `PauseAgent`/`RecordHourlyFailure` for infra-class failures; agent-class failures stay retry-eligible but do not increment the freeze counter
- [ ] 5.3 Default unknown failures to the non-infra (does-not-freeze) side
- [ ] 5.4 Tests: infra failure increments cascade; agent error and short no-op do not

## 6. Less timid retries

- [ ] 6.1 Allow hourly retries more than one attempt in `internal/relay/runner.go` (the `isHourlyRetry` / `maxAttempts=1` path)
- [ ] 6.2 Tests: a single transient failure during an hourly retry does not record an hourly failure toward freeze

## 7. Role-aware freeze-recovery

- [ ] 7.1 Gate the "files committed → success" freeze-recovery in `internal/relay/runner.go` on role: VERIFY requires a verification verdict artifact
- [ ] 7.2 Define/locate the verdict artifact contract (what VERIFY must produce)
- [ ] 7.3 Tests: frozen VERIFY without verdict stays failed; frozen implementation try with commits still recovers

## 8. Bounded prompt context

- [ ] 8.1 Add config for recent-try run count (default ~5) and per-summary + overall character budgets
- [ ] 8.2 Apply count + char budget with head/tail truncation where `recentContext` is built (`internal/relay/runner.go:~581`)
- [ ] 8.3 Tests: verbose summaries truncated; count honored; argv transport unchanged

## 9. Docs & coordination

- [ ] 9.1 Update `AGENTS.md`/role-doc references if freeze/recovery or VERIFY-verdict behavior is documented there
- [ ] 9.2 Confirm record-shape needs (attempted laps, commit list) are reflected in `tidy-rally-runtime-data-storage` rather than forked here
- [ ] 9.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
