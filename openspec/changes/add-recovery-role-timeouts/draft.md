## Draft: Add RECOVERY Role and Try Timeout Handoffs

## Why

Rally currently lets a single agent try run for too long when the agent is
stuck in a failing-test/debug loop. A recent sync-robustness relay had one
opencode try run for roughly 96 minutes, leave useful but dirty work behind,
and never finalize the lap. Subsequent retries inherited the same tangled state
and alternated between incomplete finalization, harness errors, and short rate
limits.

This is the wrong lifecycle shape. Laps should target units of work small enough
that a single try can complete or make a coherent handoff well before an hour.
When an implementing agent is out of its depth, Rally should preserve context
and route the task to a fresh, more capable recovery session instead of letting
the same attempt grind indefinitely.

## Intent

Add first-class recovery behavior for stuck or overlong tries:

- Enforce a hard 60-minute upper limit for every try.
- If the timed-out harness has a resumable session, resume it briefly with a
  handoff-only prompt and a hard 5-minute limit.
- Treat a successful `laps handoff` + `laps wrapup` as a successful
  `handoff_requested` try outcome, not a harness or usage-limit failure.
- Treat a timeout without successful handoff as `handoff_timeout`, an incomplete
  lifecycle outcome rather than infra failure.
- Route the next run to a new `RECOVERY` role.
- Teach implementing agents to voluntarily hand off when they are stuck after
  five serious non-progress debugging iterations.
- Give the RECOVERY role a prompt and route distinct from SENIOR and VERIFY:
  it is reasoning-heavy like VERIFY, but has authority and coding ability to
  reconcile dirty state and continue work.

This is a small feature upgrade and should ship as Rally `0.9.0`.

## RECOVERY Role

`RECOVERY` is not passive triage. It takes an incomplete or failed dirty state,
decides what to do with it, and starts recovering the task. It may add follow-up
laps when that is the right containment strategy.

The RECOVERY agent's first responsibility is to classify the prior state:

| Classification | Meaning |
| --- | --- |
| `continue` | Prior work is basically sound; continue from the dirty tree. |
| `discard` | Prior work is misleading, overfit, or too tangled; reset/replace it at the agent's discretion. |
| `course_correct` | Prior work has useful pieces, but the path is wrong or mixed; preserve useful work, revert/replace bad pieces, then continue. |
| `repair_plan` | The task assumptions are wrong: wrong API, wrong test target, wrong abstraction, missing fixture contract, etc. Fix the plan before continuing. |
| `needs_user` | Reluctant escape hatch for risky product/scope/destructive choices that should not be made autonomously. |

`needs_user` should be rare. It is appropriate when the cleanest solution
requires a major scope shift, risky refactor, destructive reset, or product
decision outside the lap's authority.

After classification, the RECOVERY agent should act. It should not stop at a
diagnosis unless the correct classification is `needs_user`.

## Follow-Up Laps

Any RECOVERY classification may add follow-up laps if doing so reduces risk or
creates a cleaner work split. Examples:

- Add a VERIFY lap for high-risk correctness work that needs independent review.
- Add a JUNIOR lap for straightforward mechanical build-out, such as updating a
  function signature across many call sites and aligning simple tests.
- Add a SENIOR lap for a newly isolated hard bug that is distinct from the
  current recovery.
- Add docs/ops laps if the recovery finds process or operating gaps.

Adding follow-up laps is not a way to dodge recovery. The RECOVERY agent should
still leave the current tree in a coherent state, or clearly hand off a coherent
next slice.

## Implementing-Agent Handoff Guidance

All normal implementation roles should get explicit prompt language:

> If you are stuck on the same bug or failing test and after five serious
> debugging iterations you are not making real progress, stop trying to grind it
> out. Use `laps handoff`, then `laps wrapup`, and explain the blocker,
> hypotheses tried, evidence gathered, changed files, and what a fresh agent
> should decide next.

A "debugging iteration" should be defined for agents as one loop of:

1. form a hypothesis,
2. inspect, change, or run a check,
3. observe the failure,
4. choose the next hypothesis.

This is intentionally based on the agent's own judgment rather than Rally
spying on the session transcript. Lack of diff movement, dependency failures,
or full-suite failures are not separate triggers. They can matter as evidence,
but the core check is whether the agent is honestly stuck on a stubborn issue,
cascading failures, or symptom patching without root-cause progress.

## Timeout Lifecycle

Every try gets a hard 60-minute limit.

When the limit is hit:

1. Stop the running agent attempt.
2. If the harness captured a resumable session id, resume the same session with
   a handoff-only prompt.
3. The handoff-only phase has a hard 5-minute limit.
4. In handoff-only mode, the agent must not continue implementation. It should
   summarize the blocker and call `laps handoff` followed by `laps wrapup`.
5. If handoff succeeds, record the try as `handoff_requested`.
6. If the harness cannot resume, or the handoff-only phase fails/times out,
   record the try as `handoff_timeout`.
7. Route the next run to RECOVERY.

`handoff_requested` is a successful try outcome. It does not count as harness,
agent, usage-limit, rate-limit, or infra failure.

`handoff_timeout` is incomplete. It means the agent did work but did not
finalize correctly, even after Rally attempted the bounded handoff recovery. It
should not feed the infra freeze counter and should not be treated as a usage
limit or harness failure.

## State and Telemetry

Add or normalize lifecycle categories:

- `handoff_requested`
- `handoff_timeout`

Do not add an `escalated_triage` exit condition. RECOVERY is a role/route, not a
separate terminal state. RECOVERY tries should otherwise exit using normal
success/failure categories: completed, incomplete finalization, usage limit,
short rate limit, harness launch, agent error, and so on.

Telemetry and Sentry grouping should recognize the handoff categories so timeout
and handoff events do not collapse into unrelated rate-limit or harness issues.

The run/try records should make it clear that a RECOVERY route occurred through
the role/assignee and run context. A separate `recovery_started` event may be
useful for logs or UI, but it is not required as a try exit condition.

## Routing

Add a distinct `RECOVERY` role prompt and role routing. It should default to a
senior-class model, but it should not simply reuse SENIOR instructions.

RECOVERY is functionally between VERIFY and SENIOR:

- VERIFY-like: reason carefully about the state, evidence, plan validity, and
  risk.
- SENIOR-like: modify code, clean up dirty state, and continue the task when
  appropriate.

Default routing should prefer a stronger model than ordinary implementation
roles where available.

## Candidate Work

- Add default try timeout and handoff timeout settings:
  - try timeout: 60 minutes
  - handoff-only timeout: 5 minutes
- Add timeout enforcement to the runner for every try.
- Add a handoff-only resume path for harnesses with resumable sessions.
- Define the exact handoff-only prompt.
- Add `handoff_requested` and `handoff_timeout` categories/classes.
- Ensure `handoff_requested` is considered successful/non-failure.
- Ensure `handoff_timeout` is incomplete and does not increment infra freeze
  counters.
- Route `handoff_timeout` and successful handoffs that still leave the lap
  incomplete to RECOVERY on the next run.
- Add `RECOVERY` role prompt and route defaults.
- Update general implementation role instructions with the five-iteration
  voluntary handoff rule.
- Add telemetry/Sentry coverage for the new categories.
- Add tests for:
  - 60-minute timeout stops a try,
  - resumable harness gets a 5-minute handoff-only continuation,
  - successful handoff records `handoff_requested`,
  - no-resume or failed handoff records `handoff_timeout`,
  - timeout handoff outcomes do not count as infra or usage-limit failures,
  - next run routes to RECOVERY,
  - RECOVERY prompt contains the classification contract,
  - RECOVERY may add follow-up laps.
- Bump `internal/buildinfo/VERSION` to `0.9.0`.

## Open Questions

- Should the timeout values be configurable in `.rally/config.toml`, or fixed
  defaults for the first release?
- If a try voluntarily uses `laps handoff`, should Rally always route the next
  run to RECOVERY, or only when the lap remains incomplete with dirty state?
- Should `handoff_requested` require both `laps handoff` and `laps wrapup`, or
  is a durable handoff marker enough if wrapup fails?
- Should RECOVERY classification be written to structured state, or only appear
  in summaries/wrapup text for now?
