## 1. Taxonomy and typed evidence

- [ ] 1.1 Add `FailureCategory` constants + display labels in `internal/reliability/` (the eight categories from design Decision 2)
- [ ] 1.2 Add the `FailureEvidence` struct and extend `StrategyDecision` with `Category` and a display label separate from `Reason`
- [ ] 1.3 Add an optional `Evidence *FailureEvidence` field to `TryResult` (`internal/agent/executor.go`); leave the `Executor` interface unchanged
- [ ] 1.4 Define the Category → `FailureClass` mapping (design Decision 3) as a single function; tests assert `usage_limit`/`invalid_model`/`auth_or_proxy` are NOT `FailureInfra`
- [ ] 1.5 Tests: every category maps to exactly one `FailureClass`; display labels carry no harness name unless the category is intentionally harness-specific

## 2. Classification reorder and harness scoping

- [ ] 2.1 Thread `picked.Harness` into `ClassifyError` (`runner.go:1272`) and add an optional harness constraint to each `Pattern`
- [ ] 2.2 Reorder `ClassifyError` (`patterns.go:237`): executor `Evidence` first, then provider/config/quota detection, then the dirty-tree `incomplete` pre-check (move it below `:241`), then harness-scoped patterns, then `agent_error`
- [ ] 2.3 Strip harness names from generic display labels; a pattern matches only when the failing harness matches (or the pattern is harness-agnostic)
- [ ] 2.4 Make `ClassifyError` tolerate nil/empty `Evidence` (process-level `harness_launch` failures have no `TryResult`)
- [ ] 2.5 Tests: a Codex log tail containing the prose "rate-limit" does NOT classify as a Claude rate limit

## 3. Provider/quota evidence parsers

- [ ] 3.1 Add parser helpers + tests for: Antigravity/Gemini `RESOURCE_EXHAUSTED` / `Individual quota reached` / `Resets in <dur>`; Claude `rate_limit_event` five-hour/seven-day, `model_not_found`, `authentication_failed`, 529 overload; Codex usage-limit messages; opencode JSON provider errors
- [ ] 3.2 Parsers populate `ResetAfter`/`ResetAt`/`RetryAfter`/`StatusCode`/`Provider` on `FailureEvidence`
- [ ] 3.3 Begin executor population of `FailureEvidence` for the harnesses with signatures above

## 4. Dirty-path exclusion

- [ ] 4.1 Extract a single shared exclusion helper (e.g. `gitx.IsRallyOwnedOrTransientPath`) in `internal/gitx/git.go`, replacing the duplicated `.rally/`/`.laps/` skips at `git.go:82` and `:112` (and the `filesChanged` record path)
- [ ] 4.2 Add `.claude/settings.local.json` to the helper; a try whose only dirty paths are excluded has no meaningful task change
- [ ] 4.3 Tests: only `.claude/settings.local.json` dirty + `RESOURCE_EXHAUSTED` → `usage_limit`; `src/foo.go` dirty without finalization → `incomplete_finalization`; invalid model + only settings dirty → `invalid_model`

## 5. Quota scope

- [ ] 5.1 Add a standalone `QuotaScope(harness, model) string` resolver (design Decision 4): antigravity label-substring family / opencode provider-prefix / direct-harness
- [ ] 5.2 Tests: antigravity `claude` vs `flash` vs `pro` resolve to distinct scopes from free-form labels; opencode splits on the first `/`; a direct-harness model with a stray `/` is not mis-split

## 6. Attempt-loop short-circuit and return contract

- [ ] 6.1 Break `runOne`'s attempt loop (`runner.go:1269-1301`) on first `usage_limit` / `auth_or_proxy` detection so they make exactly one attempt
- [ ] 6.2 Extend `runOne`'s return contract (`runner.go:906`) to surface the resolved `FailureCategory` + reset evidence to the routing dispatch loop (`runner.go:520-593`)
- [ ] 6.3 Tests: a `usage_limit` makes exactly one attempt (does not loop the retry budget) before control returns to the routing loop

## 7. Benching, reset recovery, and wait

- [ ] 7.1 Add `BenchUntil *time.Time` (or equivalent) to `EntryState` (`scheduler.go:8`) and a scope-keyed bench helper that benches every entry whose `QuotaScope` matches
- [ ] 7.2 Wire the routing dispatch loop to bench the quota scope (with `BenchUntil` from reset evidence, or a long conservative default) on a `usage_limit`
- [ ] 7.3 Guard the `StateActive` unbench in `syncRecoverySignals` (`route_runtime.go:257-260`) so a future `BenchUntil` is not undone; unbench + re-probe once when `now >= BenchUntil`
- [ ] 7.4 Persist the deadline as a new `agent_status.jsonl` event carrying `reset_at` + `quota_scope`; restore it across relays (design Decision 6)
- [ ] 7.5 Teach `selectionWaitError` (`route_runtime.go:313`) to derive a wait from the minimum pending `BenchUntil` so an all-benched lane waits instead of failing as `AllFrozen` (`runner.go:433`)
- [ ] 7.6 Tests: (a) a benched-but-active entry is NOT unbenched before `BenchUntil`; (b) all-benched-with-future-reset produces a wait, not an `AllFrozen` relay failure; (c) a persisted reset is restored on the next relay and re-probed once after it passes

## 8. Display and resume guidance

- [ ] 8.1 Footer and collapsed retry line show the accurate category with reset/wait detail (`usage limit, resets in 123h50m`, `rate limit, waiting 2m`, `invalid model`)
- [ ] 8.2 Try records / `summary.jsonl` store the stable `Category` separately from the human display reason
- [ ] 8.3 Include explicit finalization guidance in the resumed prompt on `incomplete_finalization` retries (and laps-backed operator pause/resume)
- [ ] 8.4 Tests: a genuine incomplete retry receives finalization guidance; the persisted record carries both `Category` and display reason

## 9. Regression coverage from observed signatures

- [ ] 9.1 Antigravity quota with zero changed files → `usage_limit`
- [ ] 9.2 Antigravity quota + only `.claude/settings.local.json` dirty → `usage_limit` (not `incomplete`)
- [ ] 9.3 Codex VERIFY log tail mentioning `rate-limit` as prose → not a Claude rate limit
- [ ] 9.4 Claude invalid model + settings dirty → `invalid_model` (not `incomplete`)
- [ ] 9.5 Real task-file dirty with no finalization → `incomplete_finalization`
