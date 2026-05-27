## Why

A black-box review of a stalled `rally` relay against the sibling `Prayer-app`
repo (preserved in `qa-report/`, `qa-report-2/`, `qa-suggestion/`) surfaced a
set of run-lifecycle defects. The code the relay produced was substantively
complete and green, yet rally's recorded state said otherwise and the relay
could not be cleanly closed or recovered. Three classes of defect stand out:

1. **State drift** — a `laps done` retry consumed the *next* lap as "done" with
   zero code written (a phantom completion), because rally never checks that the
   lap completed matches the lap the run was assigned. Separately, a VERIFY
   agent's freeze-recovery was blessed as success purely because files were
   committed, even though VERIFY produced no verification verdict.
2. **A permanent freeze lockout** — after extended failures every agent type was
   marked `frozen`, and because frozen state is terminal with no decay and is
   re-applied verbatim across relays, neither `rally resume` nor a fresh
   `rally start` could run. Only `rally start --new` worked, and only by luck
   (it does not actually reset agent status). Worse, every non-success —
   ordinary agent errors, "no changes made", harness launch failures, rate
   limits, timeouts — counts equally toward freezing, and hourly retries get a
   single attempt, so a few unlucky transient failures permanently brick a
   harness for the whole repo.
3. **Prompt bloat** — the assembled prompt (`current_task.md`) grew large enough
   to trigger `argument list too long`; recent-try summaries are concatenated
   with no character budget.

The `laps done`-from-subdirectory root cause was a separate `laps` bug and is
already fixed upstream (laps v0.4.6); it is out of scope here.

## What Changes

- **Lap-ID pinning (state integrity).** Pin the assigned lap ID at run start; on
  completion, verify the recorded completed lap(s) match the pinned lap. A
  mismatch fails the run with a distinct reason (`wrong_lap_consumed` /
  `multi_lap_consumed`) and does NOT advance the queue.
- **Completion file-change cross-check (opt-in).** When a lap declares expected
  file paths, verify those files were modified since the run started before
  accepting `laps done`; otherwise warn/reject.
- **Role-aware freeze-recovery.** "Files committed → success" is no longer
  sufficient for a VERIFY run; a frozen VERIFY try requires a verification
  verdict artifact before being treated as success. Implementation roles keep
  the current files-committed recovery.
- **Freeze decay + recovery (BREAKING for the resilience cascade).** `frozen` is
  no longer terminal for the remainder of the relay. A frozen agent type decays
  back to active (or probation) after a bounded duration, and the decay is
  re-evaluated on resume/start rather than re-applied verbatim.
- **`--new` resets agent status.** `rally start --new` explicitly clears
  pause/freeze state so a fresh relay starts from a clean slate by design, not
  by timing accident.
- **Infra-only failure classification feeds the breaker.** Only rate-limit,
  harness/launch errors (e.g. `argument list too long`), and API timeouts
  (network + I/O silence together) count toward pause/freeze. Ordinary agent
  errors and short no-op tries still fail the try and retry, but no longer drive
  a harness toward a permanent freeze.
- **Less timid retries.** Hourly retries get more than one attempt so a single
  transient blip does not consume a freeze life.
- **Bounded prompt context.** Cap recent-try context by a configurable run count
  (default ~5) plus per-summary and overall character budgets with sensible
  truncation, so the assembled prompt cannot grow unbounded. (Per-source prompt
  size telemetry lands with the Sentry sink in `tidy-rally-runtime-data-storage`.)

## Capabilities

### Modified Capabilities
- `relay-runner`: failure detection now classifies failures and only infra-class
  failures drive the resilience cascade; the cascade's freeze is no longer
  terminal (adds decay/recovery); hourly retries allow more than one attempt;
  try execution gains lap-ID pinning, opt-in completion file cross-check,
  role-aware freeze-recovery, and bounded prompt context.
- `store`: the agent status store records and honors freeze expiry/decay and
  supports an explicit reset, rather than treating frozen as permanent across
  relays.

## Impact

- **Code**: `internal/relay/runner.go` (lap pinning, file cross-check,
  freeze-recovery verdict, retry/classification gating, prompt-context budget),
  `internal/relay/resilience.go` (freeze decay, what increments the counter),
  `internal/relay/route_runtime.go` (re-evaluate vs re-apply on resume),
  `internal/reliability/patterns.go` (classify infra vs agent failures),
  `internal/store/store.go` (agent-status decay/reset), `cmd/rally/main.go`
  (`--new` reset, lap-expected-files config surface if added).
- **Behavior**: a harness can no longer be permanently bricked for a repo;
  `rally resume`/`start` recover after a freeze window; phantom lap completions
  are rejected instead of silently advancing the queue.
- **Coordination with `tidy-rally-runtime-data-storage`**: that change reworks
  `agent_status.jsonl` location (`state/`) and the try/summary record shapes —
  per-try commit-list and laps-attempted fields (QA R10/R11) land there, and
  prompt-size telemetry rides its Sentry sink. Sequence so this change's record
  needs are reflected in that change's shapes.
- **Out of scope**: stdin prompt transport (deferred; argv stays), a
  `rally reconcile` command (rejected — correctness should be intrinsic), and
  Prayer-app target-repo remediation (tracked separately).
