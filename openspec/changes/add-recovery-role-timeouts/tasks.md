## 1. TryOutcome lifecycle type

- [x] 1.1 Add a `TryOutcome` type with values `completed`, `handoff_requested`, `incomplete`, `run_timeout`, `handoff_timeout`, `failed`, `interrupted` (new file in `internal/reliability/` or `internal/store/`), with helpers `IsSuccess()` (completed/handoff_requested) and `IsTerminalForRun()` (handoff_timeout and the existing terminal categories; `run_timeout` is terminal for the normal attempt loop but followed by the handoff-only try when available)
- [x] 1.2 Leave `FailureCategory` (`internal/reliability/category.go`) unchanged — do NOT add `run_timeout` or `handoff_*` to it; only a `failed` outcome carries a `FailureCategory`
- [x] 1.3 Add `Outcome TryOutcome` and `HandoffOnly bool` to `store.TryRecord` (`internal/store/records.go`), retaining `Completed bool`; surface the resolving try's `Outcome` on `runOutcome` (`runner.go:1104`) alongside `Category`
- [x] 1.4 Route success/freeze/Issue/retry decisions through `TryOutcome` so no site treats a failure-cause category as a success
- [x] 1.5 Add `handoff_timeout` to the `terminalCategory`/short-circuit set in `runOne` so the attempt loop ends on first detection
- [x] 1.6 Tests: `handoff_requested` is a success outcome and never increments the freeze counter; `run_timeout` is non-freezing, non-Issue, and not a recovery trigger by itself; `handoff_timeout` is non-freezing and terminal; a `failed` outcome still carries a `FailureCategory`; `FailureCategory` has no `handoff_*` or `run_timeout` values

## 2. Configurable run/try timeouts

- [x] 2.1 Add `RunTimeoutSecs`, `TryTimeoutSecs`, and `HandoffTimeoutSecs` to `[reliability]` (`internal/config/config_v2.go`), defaulting to 4500, 3600, and 300 when unset/0; surface them on `RunnerConfig` (`runner.go:40`)
- [x] 2.2 Validate/clamp so `handoff_timeout_secs` never reaches/exceeds the effective `try_timeout_secs` or `run_timeout_secs`; when `try_timeout_secs >= run_timeout_secs`, accept the config and apply only the run budget (per-try cap subsumed) rather than erroring
- [x] 2.3 Add all three fields to the interactive config form (`internal/cli/config.go`) reliability group
- [x] 2.4 Tests: defaults apply when unset; configured values are read; a handoff window ≥ try/run bound is clamped or rejected

## 3. Run/try timeout enforcement

- [x] 3.1 Track a per-run wall-clock budget across the attempt loop (`runner.go:1218`) measured from run start, excluding the bounded handoff phase
- [x] 3.2 Construct the per-run deadline ONCE before the attempt loop (~`runner.go:1218`, measured from run start) and pass the same channel into every `runActionLoop` invocation as a new select arm — do NOT create it inside the loop like `stallTicker`/`attemptCtx` (`runner.go:1344-1358`), or it would reset each retry and never measure across retries. The per-try cap MAY be created per-attempt (mirroring `stallTick`). On fire of either, cancel `attemptCtx`, set `out.timedOut`/`out.runBudgetExhausted`, drain `tryCh`, break
- [x] 3.3 A per-try cap firing with run budget remaining ends the attempt and MAY start a fresh retry within the remaining budget; run-budget exhaustion stops retries and proceeds to the bounded handoff (task 4). Whichever of run/try/stall fires first wins
- [x] 3.4 Distinguish the timed-out outcomes from a stall and from an ordinary agent error in post-loop handling
- [x] 3.5 Tests (fake clock / injected sleep): cumulative retry time crossing `run_timeout_secs` stops the run and triggers handoff; a single attempt crossing `try_timeout_secs` with budget left is cancelled and retried; a run finishing under budget is unaffected; stall still fires when it precedes either timeout

## 4. Bounded handoff-only resume

- [x] 4.1 On run-budget exhaustion, if `exec.ResumeSupported()` and a session ID was captured, first persist the budget-cancelled implementation attempt as `OutcomeRunTimeout` (same `RunID`, current `AttemptNumber`, non-freezing, not a recovery trigger), then run one handoff-only `executeTry` with `ResumeSessionID` set, a fresh `attemptCtx` bounded by `HandoffTimeout` (not counted against the run budget), and the handoff-only prompt; persist that continuation as a separate `TryRecord` with the same `RunID`, next `AttemptNumber` (allowed to be `maxAttempts+1`), `HandoffOnly=true`, and the resolving outcome; do not apply the stall detector to this phase
- [x] 4.2 Set `OutcomeHandoffRequested` ONLY when a durable current-run `progress.HandoffEntry` exists (an entry appended after `summaryEntryCountBeforeRun`, proving both `laps handoff` and `laps wrapup` completed); use transient `HandoffState != 0` only to detect partial/no-wrapup handoff attempts; otherwise `OutcomeHandoffTimeout`
- [x] 4.3 When the harness cannot resume or no session ID exists, skip the resume, do not fabricate a handoff-only try record, and set the budget-cancelled implementation attempt's final outcome to `OutcomeHandoffTimeout` with a no-resume/no-session reason so recovery routing still has a persisted resolving try
- [x] 4.4 Tests: resumable harness → implementation try record with `run_timeout` plus a separate handoff-only try record with same `RunID`, next `AttemptNumber`, and `HandoffOnly=true`; handoff+wrapup → resolving continuation records `handoff_requested`; handoff without wrapup / failed / timed-out → resolving continuation records `handoff_timeout`; no-resume/no-session → single implementation try record with `handoff_timeout` and no synthetic continuation; none increment the freeze counter

## 5. Outcome computation and recovery triggers in the run

- [ ] 5.1 In `runOne`, compute the `TryOutcome` and a persisted `DirtyHandoff bool` (using the current-run durable handoff entry plus `hasOwnUncommittedChanges`); surface the outcome plus the lap ID on `runOutcome`
- [ ] 5.2 Derive trigger 1 (dirty handoff: `DirtyHandoff == true` at try resolution — NOT the `incomplete` `TryOutcome`, which a handoff makes unreachable via `finalized` at `runner.go:1425/1427`) and trigger 2 (`handoff_timeout`) from the resolving run; ordinary `failed` outcomes are NOT recovery triggers
- [ ] 5.3 Confirm `incomplete`-alone (no handoff) keeps the existing resume-with-finalization retry path, clean-handoff-alone keeps the existing follow-up flow, and a `failed` try keeps the existing bench/route/rotate path — none trigger recovery
- [ ] 5.4 Suppress auto-commit on a dirty handoff: extend the auto-commit gate at `runner.go:1434` so it also skips when the current run has a durable handoff entry and `hasOwnUncommittedChanges`, leaving the dirty tree for the recovery run (clean handoffs have nothing to commit and are unaffected)
- [ ] 5.5 Tests: dirty-handoff and handoff_timeout produce recovery triggers; incomplete-alone, clean-handoff-alone, and ordinary failed do not; a dirty handoff leaves the working tree dirty (not auto-committed) while a clean handoff path is unchanged

## 6. Recovery routing (persisted-record-derived)

- [ ] 6.1 Persist `ResolvedRoute string` (set from `selection.Route.Name`, ~`runner.go:614`), `DirtyHandoff bool`, and `HandoffCreatedLapIDs []string` (copied from the durable handoff entry) on `store.TryRecord`; keep `LapAssignee` (`runner.go:1625`) as the unsubstituted queue assignee and do not use it to identify recovery runs
- [ ] 6.2 Add a store query that, for a claimed lap that is not done, evaluates recovery triggers from `tries.jsonl` using `TryRecord.LapID` + `Outcome` + `DirtyHandoff` + `HandoffCreatedLapIDs`: read the **resolving try of the most-recent run** (the handoff-only continuation try when one exists, otherwise the last try of the highest `RunID` for that original `LapID`; do not mistake a preceding `run_timeout` record for the resolver) and test for `handoff_timeout` or `DirtyHandoff`; treat the original dirty lap and any `HandoffCreatedLapIDs` followup at the queue head as recovery-continuation targets for that same dirty tree
- [ ] 6.3 In `routeRuntime.next` (`route_runtime.go:176`), when the resolved lap is recovery-pending per 6.2, resolve the `recovery` route by **substituting the assignee** — call `ActiveRoute(routing.Lap{Assignee: "recovery"}, r.overrideRoute())`, NOT by passing `recovery` as the `override` arg (which `select.go:71-81` returns verbatim without consulting `routes["recovery"]`); preserve precedence of any relay-wide `--route` override. Return an effective assignee/prompt role of `recovery` for role prompt resolution, run/try telemetry role tags, and recovery-classification gating, while preserving the original lap assignee on `TryRecord.LapAssignee`. Do NOT mutate `.laps/laps.json`
- [ ] 6.4 Ensure the dispatch loop does not advance the queue past a recovery-pending dirty tree before a recovery run executes: if `laps add head` created followups from a dirty handoff, the first claimed followup from `HandoffCreatedLapIDs` must route to RECOVERY rather than bypassing the original dirty handoff; recovery-pending clears naturally once a recovery run resolves
- [ ] 6.5 Anti-loop cap: in the 6.2 query, also count distinct consecutive `RunID`s for the lap whose resolving try has `ResolvedRoute == "recovery"` (per 6.1; do NOT count raw try rows and do NOT use `LapAssignee`); allow at most 2. On reaching the cap, the lap is no longer recovery-pending — stop routing to recovery, raise a relay-synthesized `needs_user` operator Issue (task 9.2), and fall back to the lap's normal route (never loop). The cap-hit decision happens at selection time with no recovery agent, so it does NOT write a `RecoveryClassification`
- [ ] 6.6 Fall back to the lap's normal route with a warning when no `recovery` route is configured (never deadlock)
- [ ] 6.7 Tests: dirty-handoff and handoff_timeout each route the next run to `recovery`; a dirty handoff that creates a head followup routes that followup to RECOVERY using the original dirty trigger; the recovery-forced run receives the RECOVERY prompt/effective role even when the original lap assignee was junior/senior/ui; an ordinary failed run does not; recovery state survives a simulated relay restart (re-derived from `Outcome`/`DirtyHandoff`/`HandoffCreatedLapIDs` records, no in-memory carryover); a missing `recovery` route falls back with a warning; the queue does not advance past the dirty tree; a recovery run that itself times out/dirty-hands-off re-arms recovery up to the cap, and the 3rd consecutive recovery trigger stops at the cap (counted via distinct resolving recovery `RunID`s) and raises a `needs_user` Issue instead of looping

## 7. RECOVERY role and prompts

- [ ] 7.1 Add `internal/agent_prompt/roles/recovery.md`: recovery-and-continuation role, the five-way classification contract with meanings, classify-then-act (act unless `needs_user`), follow-up-laps allowance without dodging recovery, `laps wrapup --classification <value>` + finalize instructions; keep it OpenSpec-agnostic
- [ ] 7.2 Add the handoff-only prompt as a `general/` snippet (e.g. `general/handoff_only.md`) forbidding implementation and directing blocker summary + `laps handoff` + `laps wrapup`; expose it via an `agent_prompt` accessor and wire it into the bounded resume (task 4.1)
- [ ] 7.3 Add the five-iteration voluntary-handoff guidance (with the debugging-iteration definition, judgment-based framing) to each implementation role doc individually — `roles/junior.md`, `roles/senior.md`, `roles/ui.md` — NOT to a `general/` snippet (those inject unconditionally into VERIFY/RECOVERY too). Do NOT add it to `roles/verify.md`/`roles/recovery.md`
- [ ] 7.4 Add `recovery` to the fixed config-form role list (`internal/cli/config.go:72`, the `roleNames` slice) and update the custom-route sort bound from `roleNames[5:]` to `roleNames[6:]` (`config.go:83`) so the new fixed role isn't sorted into custom routes; seed a default `recovery` route preferring a senior-class runner
- [ ] 7.5 Tests: `Role("recovery")`/`Role("RECOVERY")` resolves the embedded snippet and respects an on-disk override; the snippet contains all five classifications; implementation-role prompts contain the five-iteration rule and VERIFY/RECOVERY prompts do not; the composed prompt references the recovery-classification field ONLY when the effective assignee/prompt role is `recovery` and never on JUNIOR/SENIOR/UI/VERIFY (confirming the instruction lives only in `roles/recovery.md`, not shared/general snippets)

## 8. Recovery classification persistence

- [ ] 8.1 Add a `Classification string` field to `progress.RunEntry` (`internal/progress/store.go`) and a `rally progress --classification <value>` flag (`internal/progress/cli.go`), forwarded by `laps wrapup`, so classification can be recorded on both `laps done` and `laps handoff` completions
- [ ] 8.2 Add `RecoveryClassification string` to `store.TryRecord` (surfaced on the run record); read it back from the current-run summary entry in `runOne`, validate against `{continue, discard, course_correct, repair_plan, needs_user}`, leave empty for non-recovery runs and for omitted/unrecognised values (best-effort, never fails the run)
- [ ] 8.3 Tests: a valid classification round-trips from `laps wrapup --classification ...` to the try record for both recovery completion and recovery handoff paths; a non-recovery run leaves it empty; an unrecognised/omitted value leaves it empty without failing the run

## 9. Telemetry and Sentry grouping

- [ ] 9.1 Add `Outcome` and `RecoveryClassification` fields to `FailureState` (`internal/telemetry/failure_state.go`) and surface them via `FailureStateTags`; emit an `outcome` scalar tag (`TryOutcome`) on every try event, keeping the existing `failure_category` tag (`tags.go:73`) only for a `failed` outcome (do NOT add a parallel `category` tag); ensure `run_timeout`/`handoff_timeout`/`handoff_requested` are spans/logs, not Issues
- [ ] 9.2 Attach `recovery_classification` as a scalar tag (from the new `FailureState` field) on recovery-route try events; capture `needs_user` as an Issue while the other four remain spans/logs
- [ ] 9.3 (Optional) emit a `recovery_started` common log event for UI/triage — not a try exit condition or an Issue
- [ ] 9.4 Tests: a `run_timeout`/`handoff_timeout`/`handoff_requested` try is a span/log, not an Issue, and distinguished by `outcome`; handoff-only continuation try logs/spans are identifiable as handoff-only; a recovery try carries `recovery_classification`; a `needs_user` recovery may capture an Issue

## 10. Version and docs

- [ ] 10.1 Bump `internal/buildinfo/VERSION` to `0.9.0`
- [ ] 10.2 Update README/role documentation to describe the RECOVERY role, the per-run/per-try timeout + bounded handoff, the two recovery triggers, and the reliability config keys
