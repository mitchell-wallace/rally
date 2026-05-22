# Findings

## Framing
This report is intentionally written from a black-box, outside perspective.

I did not try to become a rally implementation expert before writing it. I used:

- Rally's own terminology and flow notes from `AGENTS.md`
- prayer-app's `.rally/*` and `.laps/*` files
- prayer-app git history and current code state
- try/relay logs preserved in the reported container

Where I move beyond directly observed evidence, I label that as a suspicion or assumption rather than a confirmed fact.

## How I currently understand rally to work
Based on `rally/AGENTS.md`, my working model is:

- A **relay** is the overall campaign.
- A **run** is a role-assigned unit of work for a specific lap.
- A **try** is one concrete harness invocation for that run.
- Laps are the source of truth for the work queue.
- Rally maps a lap's assignee/role to a configured route and runner.
- Agents are expected to finish with `laps done` or `laps handoff`, and hooks then drive `laps wrapup ...` to persist state or create follow-up work.

That model was sufficient to interpret the observed Prayer-app history, but it is still an outsider's model rather than a source-code-level guarantee.

## Sources reviewed
From the sibling `Prayer-app` repo:

- `.laps/laps.json`
- `.rally/progress.yaml`
- `.rally/tries.jsonl`
- `.rally/relays.jsonl`
- `.rally/agent_status.jsonl`
- `.rally/hook-audit.jsonl`
- `openspec/changes/auth-email-delivery-hardening/tasks.md`
- relevant frontend/backend implementation and tests
- recent git commits on `staging`

From the reported container session:

- relay log under `/home/agent/.local/share/rally/relays/...`
- try logs under `/home/agent/.local/share/rally/tries/...`

## High-confidence observations

### 1. VERIFY found a real blocker, not a fake one
At the time of `a8df3c1` (`rally: run 18 attempt 1 (codex)`), the blocker reported by VERIFY appears to have been real:

- `forgot_password_controller.ts` was still using inline email dispatch
- `register_controller.ts` was still dispatching verification mail inline
- functional tests still allowed the queue-integration gap to slip through

That means the key VERIFY complaint was not just review churn or aesthetic disagreement. It was catching a real mismatch between the intended change and the checked-in code at that moment.

### 2. The blocker was fixed later, but the state stayed stale
Later, `c7a841f` (`fix auth email queue controller wiring`) fixed the reported queue-wiring issue.

In the current Prayer-app checkout:

- `apps/backend/app/controllers/auth/forgot_password_controller.ts` enqueues via `authEmailQueue.enqueuePasswordReset`
- `apps/backend/app/controllers/auth/register_controller.ts` enqueues via `authEmailQueue.enqueueEmailVerification`
- related functional tests now assert queue behavior rather than only dispatcher behavior

So the stale blocker recorded in `work-c905` no longer represents pending work.

### 3. Laps bookkeeping drifted from the actual work
One of the clearest signals of state drift is the mismatch between recorded lap IDs and the summaries attached to them.

Example:

- `hook-audit.jsonl` records `pray-43a5` as done at `2026-05-21T20:36:13Z`
- but the paired wrapup summary immediately after that clearly describes the **worker tick** work:
  - 5s tick
  - mutex
  - `FOR UPDATE SKIP LOCKED`
  - mark-sending-then-commit
  - retry/backoff/dead-state logic

That summary matches the worker task (`§4.3`-style work), not the controller-wiring task (`§4.4`).

From an outside perspective, this looks like rally/hook state recorded the wrong lap as complete, or otherwise let a completion signal attach to the wrong unit of work.

### 4. OpenSpec task state became detached from implementation state
`Prayer-app/openspec/changes/auth-email-delivery-hardening/tasks.md` remains fully unchecked even though most of the implementation appears to exist and current automation is green.

That has two consequences:

1. Any verifier that relies on tasks.md for completeness will continue to report the change as incomplete.
2. The operator now has to mentally reconcile three different truth sources:
   - code state
   - laps state
   - OpenSpec task state

This is not just cosmetic drift. It actively makes later verification noisier and less trustworthy.

### 5. The final state looks much healthier than the recorded queue suggests
On the current Prayer-app checkout I observed:

- backend tests passing
- frontend tests passing
- typecheck passing
- lint passing

That does not prove the final manual smoke has been completed, but it does mean the recorded state in `laps.json` materially understates the health of the current branch.

### 6. Claude failures became an infrastructure/process blocker
The relay data shows repeated Claude-lane failures. The container logs include a server-side 429 response with a message equivalent to:

- org monthly usage limit hit
- five-hour rate-limit classification
- overage disabled until reset

From an outside perspective, that means:

- this was not simply a subjective "I don't think I hit a rate limit"
- the Claude harness was receiving a concrete upstream rejection
- rally treated that as repeated try failure / pause / hourly retry rather than a special class of operator-visible escalation

Later failures changed shape and included:

- `fork/exec /usr/bin/claude: argument list too long`

That appears separate from the quota/rate-limit issue. It suggests the Claude lane was not only quota-blocked earlier, but also susceptible to a prompt/context-size launch failure later.

### 7. Freeze recovery may be too optimistic in some cases
Relay log output suggests one VERIFY attempt was freeze-recovered and then treated as success because files were committed, even though the attempt also carried a harness-error outcome.

From an outside perspective, that recovery policy looks risky:

- it can preserve useful work
- but it can also bless partially-complete or partially-verified state

This is especially concerning in a VERIFY lane, where "some files changed" is not itself evidence that verification succeeded.

### 8. There does not appear to be a clear escalation path for "automation is blocked"
I did not see a clear, structured path for rally to say:

- the job is now mostly code-complete
- the next blocker is operator action or environment access
- here is the exact escalation reason
- here is the minimal next human step

Instead, the observed behavior was closer to:

- repeated retries
- pauses
- stale blocker work remaining in the queue

That is workable for transient harness glitches, but it is not a great fit for mixed states like:

- blocker found
- blocker fixed outside the stalled lane
- final verify still pending
- harness lane unhealthy

## Timeline summary

### Earlier phase
- Multiple change chunks completed across frontend and backend.
- Mid-task VERIFY correctly found SW and timing issues, which then got fixed.

### Queue/worker phase
- queue schema/service work landed
- worker work landed
- docs landed

### Final verify phase
- VERIFY reported a real queue-wiring blocker
- blocker lap `work-c905` was created
- controller/test fix landed later in `c7a841f`
- another fix landed in `0f4a8b7`
- state did not reconcile
- Claude lane then failed repeatedly with quota/harness issues
- final verification never cleanly closed

## What I believe is still actually pending in Prayer-app
This is my best outside-observer assessment of the real remaining work:

1. Final verification / smoke, especially the broken-SMTP manual path
2. Any final E2E rerun desired on the post-fix state
3. State reconciliation:
   - stale blocker closure
   - OpenSpec task checkbox update
   - final summary / handoff reflecting the actual finished implementation

I do **not** believe the controller queue-wiring blocker is still pending in the current checkout.

## Assumptions and uncertainty

### Assumptions
- `rally/AGENTS.md` accurately reflects the intended control flow for the observed run
- the inspected container logs correspond to the same relay state captured in the prayer-app repo
- later commits on `staging` are part of the same workstream and not unrelated manual edits

### Things I did not prove
- the exact internal cause of the lap-ID mismatch
- whether freeze-recovery success classification is fully intended or a bug
- the exact root cause of the `argument list too long` failure
- whether any rally source-level safeguards already exist but were bypassed in this run

## Bottom line
My outside view is:

- VERIFY was useful and caught a real issue.
- The implementation later recovered.
- The workflow/state layer did not recover with it.
- Then harness failures prevented a clean final pass.

So the primary failure mode was not "LLM review always finds something wrong." It was:

- one real blocker,
- followed by stale state,
- followed by infrastructure/harness failure before closure.
