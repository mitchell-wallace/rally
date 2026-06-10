## 1. Taxonomy and typed evidence

- [x] 1.1 Add `FailureCategory` constants + display labels in `internal/reliability/` (the nine categories from design Decision 2, including `transient_infra`)
- [x] 1.2 Add the `FailureEvidence` struct and extend `StrategyDecision` with `Category` and a display label separate from `Reason`
- [x] 1.3 Add an optional `Evidence *FailureEvidence` field to `TryResult` (`internal/agent/executor.go`); leave the `Executor` interface unchanged
- [x] 1.4 Define the Category → `FailureClass` mapping (design Decision 3) as a single function; tests assert `usage_limit`/`invalid_model`/`auth_or_proxy` are NOT `FailureInfra`
- [x] 1.5 Tests: every category maps to exactly one `FailureClass`; display labels carry no harness name unless the category is intentionally harness-specific

## 2. Classification reorder and harness scoping

- [x] 2.1 Thread `picked.Harness` into `ClassifyError` (`runner.go:1272`) and add an optional harness constraint to each `Pattern`
- [x] 2.2 Reorder `ClassifyError` (`patterns.go:237`): executor `Evidence` first, then provider/config/quota detection, then the dirty-tree `incomplete` pre-check (move it below `:241`), then harness-scoped patterns, then `agent_error`
- [x] 2.3 Strip harness names from generic display labels; a pattern matches only when the failing harness matches (or the pattern is harness-agnostic)
- [x] 2.4a Assign a `FailureCategory` to every existing `ErrorPatterns` entry; the harness-agnostic API timeout / connection / network / TLS / non-overload 5xx patterns map to `transient_infra` (no behavior regression vs today's infra classification)
- [x] 2.4 Make `ClassifyError` tolerate nil/empty `Evidence` (process-level `harness_launch` failures have no `TryResult`)
- [x] 2.5 Tests: a Codex log tail containing the prose "rate-limit" does NOT classify as a Claude rate limit

## 3. Provider/quota evidence parsers

> **Note:** `harness_launch`, `incomplete_finalization`, and `transient_infra` are NOT executor-parsed — they are classified runner-side (task 2.4a, task 2.2). Executor population targets only the categories with provider-specific structured error signals.

- [x] 3.1 Add parser helpers + tests for: Antigravity/Gemini `RESOURCE_EXHAUSTED` / `Individual quota reached` / `Resets in <dur>`; Claude `rate_limit_event` five-hour/seven-day, `model_not_found`, `authentication_failed`, 529 overload; Codex usage-limit messages; opencode JSON provider errors
- [x] 3.2 Parsers populate `ResetAfter`/`ResetAt`/`RetryAfter`/`StatusCode`/`Provider` on `FailureEvidence`
- [x] 3.3 Begin executor population of `FailureEvidence` for the harnesses with signatures above

## 4. Dirty-path exclusion

- [x] 4.1 Extract a single shared exclusion helper (e.g. `gitx.IsRallyOwnedOrTransientPath`) in `internal/gitx/git.go`, replacing the duplicated `.rally/`/`.laps/` skips at `git.go:82`, `git.go:112`, and `filesChangedList` at `runner.go:1717-1718`
- [x] 4.2 Add `.claude/settings.local.json` to the helper; a try whose only dirty paths are excluded has no meaningful task change
- [x] 4.3 Tests: Claude `usage_limit` + only `.claude/settings.local.json` dirty → `usage_limit` (not `incomplete`); Antigravity `RESOURCE_EXHAUSTED` + zero meaningful changes → `usage_limit` (not `incomplete`); `src/foo.go` dirty without finalization → `incomplete_finalization`; Claude `invalid_model` + only settings dirty → `invalid_model`

## 5. Quota scope

- [x] 5.1 Add a standalone `QuotaScope(harness, model) string` resolver (design Decision 4): antigravity label-substring family / opencode provider-prefix / direct-harness
- [x] 5.2 Tests: antigravity `claude` vs `flash` vs `pro` resolve to distinct scopes from free-form labels; opencode splits on the first `/`; a direct-harness model with a stray `/` is not mis-split

## 6. Attempt-loop short-circuit and return contract

- [x] 6.1 Break `runOne`'s attempt loop (`runner.go:1269-1301`) on first `usage_limit` / `auth_or_proxy` detection so they make exactly one attempt
- [x] 6.2 Extend `runOne`'s return contract (`runner.go:906`) to surface the resolved `FailureCategory` + reset evidence to the routing dispatch loop (`runner.go:520-593`)
- [x] 6.3 Tests: a `usage_limit` makes exactly one attempt (does not loop the retry budget) before control returns to the routing loop

## 7. Benching, reset recovery, and wait

- [x] 7.1 Add `StateBenched` to `AgentState` (`resilience.go:13`) and a `benched` event type carrying `reset_at` + `quota_scope` on `AgentStatusEvent` (add the fields; update the `EventType` comment at `records.go:65` to list all valid types). Add a `BenchAgent(key, resetAt, scope, relayID)` method (`resilience.go`) that persists the event alongside `PauseAgent`/`FreezeAgent`.
- [x] 7.2 Teach `GetState` (`resilience.go:59`) to return `StateBenched` while `now < reset_at`, and to surface the key as `StateActive` for a single re-probe once the deadline passes — mirroring the pure-read frozen→probation decay (`resilience.go:89-93`). Cross-relay persistence and restoration are free via this replay (no bespoke scanner).
- [x] 7.3 Add a route-runtime `benchQuotaScope(resilience, scope, resetAt, relayID)` helper (mirroring `forceUnpauseAll`, `route_runtime.go:360`) that iterates every scheduler's entries, resolves each entry's `{Harness,Model}` key and `QuotaScope`, and writes a `benched` event via `BenchAgent` for every distinct matching key. Call it from the routing dispatch loop (`runner.go:520-593`) on a `usage_limit`, using the parsed reset or a long conservative default.
- [x] 7.4 Add one arm to `syncRecoverySignals` (`route_runtime.go:256`): `case StateBenched` benches the entry (`OnAgentFailed(state, "quota", true)`). No `StateActive` unbench guard is needed (a benched key is never `StateActive`); leave the `StateActive`/`StatePaused`/`StateProbation`/`StateFrozen` arms untouched.
- [x] 7.5 Widen `selectionWaitError` (`route_runtime.go:313`, currently paused-only at `:330`) to also derive a wait from the earliest `StateBenched` `reset_at`, so an all-benched lane waits instead of failing as `AllFrozen` (`runner.go:433`). Widen `forceUnpauseAll` (`route_runtime.go:360`) to also clear `StateBenched` (write an `active` event) on operator skip.
- [x] 7.6 Add the `benched` event type to the truncation retention allow-list in `truncateAgentStatus` (`store.go:128`, currently keeps only `frozen`/`probation`) so a long-lived multi-day reset is not truncated away.
- [x] 7.7 Tests: (a) a benched key is NOT selected before `reset_at`; (b) all-benched-with-future-reset produces a wait, not an `AllFrozen` relay failure; (c) a persisted reset survives a fresh relay via `GetState` replay and is re-probed once after it passes; (d) a post-deadline re-probe that again returns `usage_limit` re-benches a fresh window; (e) the `benched` event does not interfere with frozen/probation/paused state recovery

## 8. Display and resume guidance

- [ ] 8.1 Footer and collapsed retry line show the accurate category with reset/wait detail (`usage limit, resets in 123h50m`, `rate limit, waiting 2m`, `invalid model`)
- [ ] 8.2 `TryRecord` (`tries.jsonl`) gains a `Category` field storing the stable `FailureCategory` separately from the human-readable `FailReason` display string
- [ ] 8.3 Verify the existing `incompleteRetryGuidance` constant (`runner.go:151`) is adequate for the `incomplete_finalization` category; update it if needed, then include it in the resumed prompt on `incomplete_finalization` retries (including laps-backed operator pause/resume at `runner.go:1004-1006`)
- [ ] 8.4 Tests: a genuine incomplete retry receives finalization guidance; the persisted record carries both `Category` and display reason

## 9. Regression coverage from observed signatures

- [ ] 9.1 Antigravity quota with zero changed files → `usage_limit`
- [ ] 9.2 Antigravity quota + only `.claude/settings.local.json` dirty → `usage_limit` (not `incomplete`)
- [ ] 9.3 Codex VERIFY log tail mentioning `rate-limit` as prose → not a Claude rate limit
- [ ] 9.4 Claude invalid model + settings dirty → `invalid_model` (not `incomplete`)
- [ ] 9.5 Real task-file dirty with no finalization → `incomplete_finalization`
- [ ] 9.6 An API timeout / connection-reset log tail classifies `transient_infra` (infra-class), not `agent_error` (guards the no-regression intent of the new category)
