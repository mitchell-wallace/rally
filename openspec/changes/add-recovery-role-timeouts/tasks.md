## 1. TryOutcome lifecycle type

- [ ] 1.1 Add a `TryOutcome` type with values `completed`, `handoff_requested`, `incomplete`, `handoff_timeout`, `failed`, `interrupted` (new file in `internal/reliability/` or `internal/store/`), with helpers `IsSuccess()` (completed/handoff_requested) and `IsTerminalForRun()` (handoff_timeout and the existing terminal categories)
- [ ] 1.2 Leave `FailureCategory` (`internal/reliability/category.go`) unchanged — do NOT add `handoff_*` to it; only a `failed` outcome carries a `FailureCategory`
- [ ] 1.3 Add `Outcome TryOutcome` to `store.TryRecord` (`internal/store/records.go`), retaining `Completed bool`; surface `Outcome` on `runOutcome` (`runner.go:1104`) alongside `Category`
- [ ] 1.4 Route success/freeze/Issue/retry decisions through `TryOutcome` so no site treats a failure-cause category as a success
- [ ] 1.5 Add `handoff_timeout` to the `terminalCategory`/short-circuit set in `runOne` so the attempt loop ends on first detection
- [ ] 1.6 Tests: `handoff_requested` is a success outcome and never increments the freeze counter; `handoff_timeout` is non-freezing and terminal; a `failed` outcome still carries a `FailureCategory`; `FailureCategory` has no `handoff_*` values

## 2. Configurable run/try timeouts

- [ ] 2.1 Add `RunTimeoutSecs`, `TryTimeoutSecs`, and `HandoffTimeoutSecs` to `[reliability]` (`internal/config/config_v2.go`), defaulting to 4500, 3600, and 300 when unset/0; surface them on `RunnerConfig` (`runner.go:40`)
- [ ] 2.2 Validate/clamp so `handoff_timeout_secs` never reaches/exceeds the effective `try_timeout_secs` or `run_timeout_secs`; when `try_timeout_secs >= run_timeout_secs`, accept the config and apply only the run budget (per-try cap subsumed) rather than erroring
- [ ] 2.3 Add all three fields to the interactive config form (`internal/cli/config.go`) reliability group
- [ ] 2.4 Tests: defaults apply when unset; configured values are read; a handoff window ≥ try/run bound is clamped or rejected

## 3. Run/try timeout enforcement

- [ ] 3.1 Track a per-run wall-clock budget across the attempt loop (`runner.go:1218`) measured from run start, excluding the bounded handoff phase
- [ ] 3.2 Construct the per-run deadline ONCE before the attempt loop (~`runner.go:1218`, measured from run start) and pass the same channel into every `runActionLoop` invocation as a new select arm — do NOT create it inside the loop like `stallTicker`/`attemptCtx` (`runner.go:1344-1358`), or it would reset each retry and never measure across retries. The per-try cap MAY be created per-attempt (mirroring `stallTick`). On fire of either, cancel `attemptCtx`, set `out.timedOut`/`out.runBudgetExhausted`, drain `tryCh`, break
- [ ] 3.3 A per-try cap firing with run budget remaining ends the attempt and MAY start a fresh retry within the remaining budget; run-budget exhaustion stops retries and proceeds to the bounded handoff (task 4). Whichever of run/try/stall fires first wins
- [ ] 3.4 Distinguish the timed-out outcomes from a stall and from an ordinary agent error in post-loop handling
- [ ] 3.5 Tests (fake clock / injected sleep): cumulative retry time crossing `run_timeout_secs` stops the run and triggers handoff; a single attempt crossing `try_timeout_secs` with budget left is cancelled and retried; a run finishing under budget is unaffected; stall still fires when it precedes either timeout

## 4. Bounded handoff-only resume

- [ ] 4.1 On run-budget exhaustion, if `exec.ResumeSupported()` and a session ID was captured, run one `executeTry` with `ResumeSessionID` set, a fresh `attemptCtx` bounded by `HandoffTimeout` (not counted against the run budget), and the handoff-only prompt; do not apply the stall detector to this phase
- [ ] 4.2 Set `OutcomeHandoffRequested` ONLY when both `laps handoff` (`HandoffState != 0`) and a completed `laps wrapup` (durable `progress.HandoffEntry`) are observed; otherwise `OutcomeHandoffTimeout`
- [ ] 4.3 When the harness cannot resume or no session ID exists, skip the resume and set `OutcomeHandoffTimeout`
- [ ] 4.4 Tests: resumable harness → bounded continuation; handoff+wrapup → `handoff_requested`; handoff without wrapup / failed / timed-out / no-resume → `handoff_timeout`; neither increments the freeze counter

## 5. Outcome computation and recovery triggers in the run

- [ ] 5.1 In `runOne`, compute the `TryOutcome` (reusing `hasOwnUncommittedChanges` and the handoff state); surface it plus the lap ID on `runOutcome`
- [ ] 5.2 Derive trigger 1 (dirty handoff: `handoffState != 0 && hasOwnUncommittedChanges` at try resolution — NOT the `incomplete` `TryOutcome`, which a handoff makes unreachable via `finalized` at `runner.go:1425/1427`) and trigger 2 (`handoff_timeout`) from the resolving run; ordinary `failed` outcomes are NOT recovery triggers
- [ ] 5.3 Confirm `incomplete`-alone (no handoff) keeps the existing resume-with-finalization retry path, clean-handoff-alone keeps the existing follow-up flow, and a `failed` try keeps the existing bench/route/rotate path — none trigger recovery
- [ ] 5.4 Suppress auto-commit on a dirty handoff: extend the auto-commit gate at `runner.go:1434` so it also skips when `handoffState != 0 && hasOwnUncommittedChanges`, leaving the dirty tree for the recovery run (clean handoffs have nothing to commit and are unaffected)
- [ ] 5.5 Tests: dirty-handoff and handoff_timeout produce recovery triggers; incomplete-alone, clean-handoff-alone, and ordinary failed do not; a dirty handoff leaves the working tree dirty (not auto-committed) while a clean handoff path is unchanged

## 6. Recovery routing (persisted-record-derived)

- [ ] 6.1 Persist a `ResolvedRoute string` field on `store.TryRecord` set from `selection.Route.Name` (~`runner.go:614`), since `LapAssignee` (`runner.go:1625`) records the unsubstituted queue assignee and cannot identify a recovery run
- [ ] 6.2 Add a store query that, for a head lap that is not done, evaluates recovery triggers from `tries.jsonl` using `TryRecord.LapID` + `Outcome`: read the **resolving try of the most-recent run** (the last try of the highest `RunID` for that `LapID`, not merely the last try row, since a run has multiple retry tries) and test for `handoff_timeout` or dirty handoff
- [ ] 6.3 In `routeRuntime.next` (`route_runtime.go:176`), when the resolved lap is recovery-pending per 6.2, resolve the `recovery` route by **substituting the assignee** — call `ActiveRoute(routing.Lap{Assignee: "recovery"}, r.overrideRoute())`, NOT by passing `recovery` as the `override` arg (which `select.go:71-81` returns verbatim without consulting `routes["recovery"]`); preserve precedence of any relay-wide `--route` override. Do NOT mutate `.laps/laps.json`
- [ ] 6.4 Ensure the dispatch loop does not advance the queue past a recovery-pending lap before a recovery run executes, and that recovery-pending clears naturally once a recovery run resolves
- [ ] 6.5 Anti-loop cap: in the 6.2 query, also count consecutive runs for the lap whose `ResolvedRoute == "recovery"` (per 6.1; do NOT use `LapAssignee`); allow at most 2. On reaching the cap, the lap is no longer recovery-pending — stop routing to recovery, raise a relay-synthesized `needs_user` operator Issue (task 9.2), and fall back to the lap's normal route (never loop). The cap-hit decision happens at selection time with no recovery agent, so it does NOT write a `RecoveryClassification`
- [ ] 6.6 Fall back to the lap's normal route with a warning when no `recovery` route is configured (never deadlock)
- [ ] 6.7 Tests: dirty-handoff and handoff_timeout each route the next run to `recovery`; an ordinary failed run does not; recovery state survives a simulated relay restart (re-derived from records, no in-memory carryover); a missing `recovery` route falls back with a warning; the queue does not advance past the lap; a recovery run that itself times out/dirty-hands-off re-arms recovery up to the cap, and the 3rd consecutive recovery trigger stops at the cap (counted via `ResolvedRoute`) and raises a `needs_user` Issue instead of looping

## 7. RECOVERY role and prompts

- [ ] 7.1 Add `internal/agent_prompt/roles/recovery.md`: recovery-and-continuation role, the five-way classification contract with meanings, classify-then-act (act unless `needs_user`), follow-up-laps allowance without dodging recovery, record-classification + finalize instructions; keep it OpenSpec-agnostic
- [ ] 7.2 Add the handoff-only prompt as a `general/` snippet (e.g. `general/handoff_only.md`) forbidding implementation and directing blocker summary + `laps handoff` + `laps wrapup`; expose it via an `agent_prompt` accessor and wire it into the bounded resume (task 4.1)
- [ ] 7.3 Add the five-iteration voluntary-handoff guidance (with the debugging-iteration definition, judgment-based framing) to each implementation role doc individually — `roles/junior.md`, `roles/senior.md`, `roles/ui.md` — NOT to a `general/` snippet (those inject unconditionally into VERIFY/RECOVERY too). Do NOT add it to `roles/verify.md`/`roles/recovery.md`
- [ ] 7.4 Add `recovery` to the fixed config-form role list (`internal/cli/config.go:72`, the `roleNames` slice) and update the custom-route sort bound from `roleNames[5:]` to `roleNames[6:]` (`config.go:83`) so the new fixed role isn't sorted into custom routes; seed a default `recovery` route preferring a senior-class runner
- [ ] 7.5 Tests: `Role("recovery")`/`Role("RECOVERY")` resolves the embedded snippet and respects an on-disk override; the snippet contains all five classifications; implementation-role prompts contain the five-iteration rule and VERIFY/RECOVERY prompts do not; the composed prompt references the recovery-classification field ONLY on a `recovery`-assigned run and never on JUNIOR/SENIOR/UI/VERIFY (confirming the instruction lives only in `roles/recovery.md`, not shared/general snippets)

## 8. Recovery classification persistence

- [ ] 8.1 Add a `Classification string` field to `progress.HandoffEntry` (`internal/progress/store.go`), written through the `laps wrapup`/handoff flow
- [ ] 8.2 Add `RecoveryClassification string` to `store.TryRecord` (surfaced on the run record); read it back from the handoff entry/run-state in `runOne`, validate against `{continue, discard, course_correct, repair_plan, needs_user}`, leave empty for non-recovery runs and for omitted/unrecognised values (best-effort, never fails the run)
- [ ] 8.3 Tests: a valid classification round-trips from wrapup to the try record; a non-recovery run leaves it empty; an unrecognised/omitted value leaves it empty without failing the run

## 9. Telemetry and Sentry grouping

- [ ] 9.1 Add `Outcome` and `RecoveryClassification` fields to `FailureState` (`internal/telemetry/failure_state.go`) and surface them via `FailureStateTags`; emit an `outcome` scalar tag (`TryOutcome`) on every try event, keeping the existing `failure_category` tag (`tags.go:73`) only for a `failed` outcome (do NOT add a parallel `category` tag); ensure `handoff_timeout`/`handoff_requested` are spans/logs, not Issues
- [ ] 9.2 Attach `recovery_classification` as a scalar tag (from the new `FailureState` field) on recovery-route try events; capture `needs_user` as an Issue while the other four remain spans/logs
- [ ] 9.3 (Optional) emit a `recovery_started` common log event for UI/triage — not a try exit condition or an Issue
- [ ] 9.4 Tests: a `handoff_timeout`/`handoff_requested` try is a span/log, not an Issue, and distinguished by `outcome`; a recovery try carries `recovery_classification`; a `needs_user` recovery may capture an Issue

## 10. Version and docs

- [ ] 10.1 Bump `internal/buildinfo/VERSION` to `0.9.0`
- [ ] 10.2 Update README/role documentation to describe the RECOVERY role, the per-run/per-try timeout + bounded handoff, the two recovery triggers, and the reliability config keys
