## Why

A black-box review of a stalled `rally` relay against the sibling `Prayer-app`
repo (preserved in `qa-report/`, `qa-report-2/`, `qa-suggestion/`) surfaced a
set of run-lifecycle defects. The code the relay produced was substantively
complete and green, yet rally's recorded state said otherwise and the relay
could not be cleanly closed or recovered. Three classes of defect stand out:

1. **State drift** — a `laps done` retry consumed the *next* lap as "done" with
   zero code written (a phantom completion), because rally never checks that the
   lap completed matches the lap the run was assigned. Separately, a stalled
   VERIFY agent's recovery was blessed as success purely because files were
   committed, even though VERIFY did no actual verification work.
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
  `multi_lap_consumed`) and does NOT advance the queue. Record attempted lap IDs
  (with timestamps) on the try record so multi-lap consumption is traceable.
- **Role-aware stall-recovery.** "Files committed → success" is no longer applied
  to a VERIFY run; a VERIFY try killed by the liveness stall detector is NOT
  auto-accepted on the basis of commits (VERIFY may legitimately commit only a
  trivial fix), so it stays a retry-eligible failure and is retried/resumed.
  Implementation roles keep the current files-committed recovery. (No verdict
  artifact is introduced — see design Decision 7 for why it was rejected.)
- **Naming disambiguation.** Rename so "freeze" stops meaning three things: the
  liveness detector freeze→**stall**, the scheduler `EntryState.Frozen`→**`Benched`**;
  the persisted per-agent-type `frozen` keeps its name. Pure rename, no behavior
  change. (`FallbackConfig`→`FreeRunPrompt` is a related but separate cleanup
  owned by `cli-polish`.)
- **Freeze decay + recovery (BREAKING for the resilience cascade).** `frozen` is
  no longer terminal for the remainder of the relay. A frozen agent type decays
  to probation (a new tentative-active state) after a bounded duration (default
  5h), and the decay is re-evaluated on resume/start rather than re-applied
  verbatim. A probationary agent retries with cautious semantics: one run at a
  time; success promotes to active, failure re-freezes.
- **`--new` explicitly resets agent status.** `rally start --new` truncates
  agent status history so all harnesses start active by design, not by timing
  accident.
- **Failure classification feeds the breaker (per-harness-model granularity).**
  Failures are classified as infra-class (rate-limit, harness/launch errors such
  as `argument list too long`, API timeouts, stall detection) or agent-class
  (ordinary agent errors, short no-ops). A harness-model pair is paused only
  after >1 infra-class failure within a run; a single transient infra failure
  retries without escalation. Agent-class failures and the new "incomplete"
  class (agent made file changes but did not finalize the lap) fail the try and
  retry but do NOT count toward pause/freeze. Rate-limit flags are tracked per
  harness-model pair so that an opencode runner using multiple providers does
  not freeze wholesale when only one provider hits its limit.
- **Less timid retries.** Hourly retries get up to 3 attempts (was 1) so a
  couple of transient blips do not burn a freeze life.
- **Bounded prompt context.** Cap recent-try context by a configurable run count
  (default ~5) plus per-summary and overall character budgets with sensible
  truncation, so the assembled prompt cannot grow unbounded. (Per-source prompt
  size telemetry lands with the Sentry sink in `tidy-rally-runtime-data-storage`.)

## Capabilities

### Modified Capabilities
- `relay-runner`: failure detection now classifies failures (infra/agent/incomplete)
  at per-harness-model granularity; only >1 infra-class failure drives the cascade;
  the cascade's freeze is no longer terminal (adds probation+decay); hourly retries
  allow up to 3 attempts; try execution gains lap-ID pinning with attempted-lap
  recording, role-aware stall-recovery (VERIFY excluded from files-committed
  recovery), and bounded prompt context.
- `store`: the agent status store records and honors freeze expiry/decay, supports
  probation state and an explicit reset via `--new`.

## Impact

- **Code**: `internal/relay/runner.go` (lap pinning with attempted-lap recording,
  role-aware stall-recovery, failure classification gating with >1 infra threshold,
  "incomplete" class, prompt-context budget), `internal/relay/resilience.go`
  (freeze decay to probation, per-harness-model tracking, what increments the
  counter), `internal/relay/route_runtime.go` (re-evaluate vs re-apply on resume,
  probation handling), `internal/reliability/{patterns,freeze}.go` (classify
  infra/agent/incomplete failures; freeze→stall rename), `internal/routing/scheduler.go`
  (`Frozen`→`Benched` rename), `internal/store/store.go` (agent-status decay/reset
  with probation), `cmd/rally/main.go` (`--new` explicit
  reset).
- **Behavior**: a harness can no longer be permanently bricked for a repo;
  `rally resume`/`start` recover after a freeze window; phantom lap completions
  are rejected instead of silently advancing the queue; a single transient
  infra failure no longer pauses a harness.
- **Coordination with `tidy-rally-runtime-data-storage`**: that change reworks
  `agent_status.jsonl` location (`state/`) and the try/summary record shapes.
  This change adds a laps-attempted field to the try record (a simple list of
  lap IDs with timestamps); `tidy` may later restructure it but the field ships
  here. Prompt-size telemetry rides tidy's Sentry sink.
- **Out of scope**: stdin prompt transport (deferred; argv stays), a
  `rally reconcile` command (rejected — correctness should be intrinsic), and
  Prayer-app target-repo remediation (tracked separately).
