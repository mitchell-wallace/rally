## Why

Rally currently lets a single try grind for too long when an agent is stuck in a
failing-test/debug loop. A recent sync-robustness relay had one opencode try run
for roughly 96 minutes, leave useful but dirty work behind, and never finalize
the lap. Subsequent retries inherited the same tangled state and alternated
between incomplete finalization, harness errors, and short rate limits — wasted
budget on a task whose lifecycle shape was wrong.

Laps should target units of work small enough that a single try can complete or
make a coherent handoff well before an hour. When an implementing agent is out of
its depth, Rally should bound the try, preserve context, and route the task to a
fresh, more capable recovery session instead of letting the same attempt grind
indefinitely. Today there is no hard upper bound per try (only the silence-based
stall detector, which a chatty stuck agent never trips), no first-class "I'm
stuck, hand off" path, and no role whose job is to reconcile dirty leftover state
and continue.

## What Changes

- **A hard per-run timeout across retries.** A run gets a configurable wall-clock
  budget measured across all of its retry attempts (default 60 minutes), so a
  struggling runner cannot grind for hours before the run resolves. A secondary
  per-attempt cap (default 45 minutes — a single agent session should finish well
  before an hour) guards against a single runaway try, while the slightly larger
  run budget leaves buffer for quick non-blocking retries (provider dropout,
  network blip). Both
  are orthogonal to the silence-based stall detector.
- **A bounded handoff-only recovery on budget exhaustion.** When the run budget is
  spent and the harness has a resumable session, Rally resumes that session once
  with a handoff-only prompt under a separate hard limit (default 5 minutes, not
  counted in the run budget) whose only job is to summarize the blocker and call
  `laps handoff` + `laps wrapup`.
- **A first-class `TryOutcome` lifecycle type** (`completed`, `handoff_requested`,
  `incomplete`, `handoff_timeout`, `failed`, `interrupted`), orthogonal to the
  `FailureCategory` failure-cause taxonomy. `handoff_requested` is a *successful*
  outcome (handoff and wrapup both completed; not a harness/usage/infra failure);
  `handoff_timeout` is a non-freezing failure outcome (the agent did work but
  Rally's bounded handoff recovery could not finalize it). `FailureCategory` is no
  longer overloaded with lifecycle labels — only a `failed` outcome carries one.
- **A `RECOVERY` role and route.** A new role, reasoning-heavy like VERIFY but
  with authority and coding ability like SENIOR, that classifies the prior dirty
  state (`continue` / `discard` / `course_correct` / `repair_plan` /
  `needs_user`), acts on it, and may add follow-up laps. It defaults to a
  stronger model than ordinary implementation roles and does not reuse SENIOR's
  prompt.
- **Rally-driven RECOVERY routing on two triggers.** The next run for a lap is
  forced onto the `recovery` route when the lap **handed off yet left meaningful
  own-uncommitted changes** (a "dirty handoff": `handoffState != 0 &&
  hasOwnUncommittedChanges` at try resolution — *not* the `incomplete`
  `TryOutcome`, which is mutually exclusive with a handoff because any handoff
  sets `finalized`), or ends in `handoff_timeout` — the two states that leave a
  half-finished, suspect tree needing reconciliation. A plain `incomplete`
  outcome (changes, no handoff) keeps its existing retry path and a clean
  `handoff` (no leftover dirt) keeps its existing follow-up flow. An
  ordinary `failed` try (usage limit, provider instability, agent error) is **not**
  a recovery trigger — it routes/benches/rotates through the existing resilience
  paths. A dirty handoff also **suppresses auto-commit** so the recovery session
  inherits the real half-finished tree to reconcile. Recovery-pending is **derived
  from the persisted try records** Rally already writes, so it survives relay
  restarts and never rewrites the work queue. Consecutive recovery runs on a lap are
  **capped at two**: if a recovery run itself times out or hands off dirty, recovery
  re-arms only up to the cap, after which the lap resolves `needs_user` (an operator
  Issue) rather than looping forever.
- **Voluntary handoff guidance.** Normal implementation roles get explicit prompt
  language to stop grinding and `laps handoff` after five serious, non-progress
  debugging iterations, with a definition of a "debugging iteration".
- **A structured recovery classification** persisted on the run/try record and
  emitted as a telemetry tag, so recovery outcomes are filterable.

This is a small, self-contained feature upgrade and ships as Rally `0.9.0`.

## Capabilities

### Modified Capabilities
- `relay-runner`: a first-class `TryOutcome` lifecycle type is introduced
  (`handoff_requested`/`handoff_timeout` live here, not in `FailureCategory`); the
  run gains a per-run wall-clock budget across retries (plus a secondary per-try
  cap) and the bounded handoff-only resume; RECOVERY routing (two triggers, derived
  from persisted records) is added to the run-to-run dispatch.
- `agent-lifecycle`: the honest-resume mechanism is reused for a new bounded,
  handoff-only resume mode; a RECOVERY role default boundary is added alongside
  the VERIFY boundary.
- `agent-prompt`: a new embedded `recovery` role snippet with the classification
  contract; the shared finalize/role guidance gains the five-iteration voluntary
  handoff rule for implementation roles.
- `telemetry`: the agent-state-on-failure tags and Issue taxonomy recognise the
  handoff categories and the recovery classification, so timeout/handoff events
  do not collapse into unrelated rate-limit or harness Issues.
- `cli-config`: `run_timeout_secs`, `try_timeout_secs`, and `handoff_timeout_secs`
  are added under `[reliability]`; the config form lists the `recovery` route.

### Added Capabilities
None — every change extends an existing capability spec. `RECOVERY` is a role and
route, not a new terminal lifecycle state.

## Impact

- **Code**: `internal/relay/runner.go` (per-run + per-try timeout timers in/around
  `runActionLoop`, handoff-only bounded resume, `TryOutcome` computation,
  recovery-routing decision and the run→run dispatch), a new `TryOutcome` type
  (`internal/reliability/` or `internal/store/`), `internal/relay/route_runtime.go`
  + `internal/routing/select.go` (recovery route override for a recovery-pending
  lap) + a store query deriving recovery-pending from `tries.jsonl`,
  `internal/agent_prompt/roles/recovery.md` (new) + `internal/agent_prompt/general/`
  and the implementation role snippets (voluntary handoff rule),
  `internal/cli/config.go` (reliability timeout fields, `recovery` in the route
  list), `internal/config/config_v2.go` (config parsing/defaults),
  `internal/store/records.go` (`Outcome`, `RecoveryClassification`, `ResolvedRoute`
  on `TryRecord`)
  + `internal/progress/store.go` (`Classification` on `HandoffEntry`),
  `internal/telemetry/` (new `outcome`/`recovery_classification` tags + `Outcome`/
  `RecoveryClassification` fields on `FailureState`, reusing the existing
  `failure_category` tag; Issue taxonomy),
  `internal/buildinfo/VERSION` (→ `0.9.0`).
- **Behavior**: a stuck run is bounded at ~60 minutes across retries and either
  hands off cleanly or is routed to a fresh RECOVERY session; the same tangled
  state is no longer ground on for hours; handoff outcomes are no longer
  mislabelled as infra or usage-limit failures.
- **Out of scope**: **fixing mid-run failover** — the run/retry model pins all of a
  run's retries to one harness+model, so repeated runner-specific failures
  (e.g. rate limits) burn the retry budget on one runner before the next run
  rotates (root-caused from `RALLY-2`). This change adds a *time* bound that caps
  the damage; rotating harness+model *within* a run is a separate routing change
  (see design "Failover gap"). Also out of scope: spying on the agent transcript to
  *detect* non-progress (the five-iteration rule is the agent's own judgment); a
  timeout-tuning UI (`build-new-tui`); changing the silence-based stall detector;
  the full harness-adapter normalization (`improve-harness-consistency`).
- **Coordination**: builds directly on `improve-error-categorisation`'s
  `FailureCategory`/evidence plumbing and `enrich-failure-telemetry`'s
  agent-state failure tags — this change adds two lifecycle values and a
  classification tag into those existing shapes rather than inventing parallel
  ones.
