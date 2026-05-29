# Recommendations

## Recommendation style
These are suggestions from a black-box operator perspective, not demands based on a full internal design review.

I am intentionally separating them from the confirmed findings. Some may already be partially addressed elsewhere in rally.

## 1. Treat VERIFY as read-only by default
From the observed run, a VERIFY lane appears capable of:

- finding blockers
- mutating state
- and, in at least one case, ending in a freeze-recovery / harness-error-success hybrid outcome

That combination is risky.

Suggested direction:

- VERIFY should default to read-only/reporting mode
- if it opens blocker work, that work should move to a non-VERIFY lane
- a VERIFY lane should need an explicit opt-in to make code changes

Why this matters:

- verification success and implementation success are different claims
- mixing them makes later reasoning about relay state much harder

## 2. Validate lap completion against the wrapup content
The observed mismatch between a recorded lap ID and the actual summary strongly suggests a missing integrity check.

Suggested direction:

- when `laps done` / `laps wrapup` fires, validate that:
  - the completed lap ID matches the active queued lap
  - the summary or associated metadata is at least consistent with that lap

Even a lightweight safeguard would help, for example:

- "lap being completed does not match current run's assigned lap"
- or "run claimed lap A but attached summary from lap B"

This would have caught the Prayer-app state drift much earlier.

## 3. Preserve a full commit list per try/run, not only one final commit hash
The post-blocker Prayer-app story depends on seeing:

- the blocker report commit
- the controller/test fix commit
- the later follow-up fix

If try metadata only keeps one final commit hash, the causal chain is too easy to lose.

Suggested direction:

- persist all commits created during a try
- or at least persist:
  - first commit
  - last commit
  - commit count
  - whether the try changed code after initially claiming to verify

## 4. Escalate quota/harness failures differently from ordinary task failures
The Claude lane experienced failures that appear to be infrastructure-level:

- upstream quota/rate-limit rejection
- launch failure (`argument list too long`)

Those should probably not be treated the same way as:

- code bug
- flaky test
- harness exit mid-work with recoverable context

Suggested direction:

- classify quota/auth/launch failures as a distinct failure family
- expose them clearly to the operator
- support explicit fallback behavior, such as:
  - alternate route/harness/model
  - pause with escalation note
  - retry only after a configured cooldown

## 5. Add a first-class "state reconciliation" flow
Prayer-app ended up in a common mixed state:

- verifier found a real issue
- fix landed later
- queue still says blocker open
- final verify still pending

Suggested direction:

- provide a structured "reconcile current HEAD with recorded state" operation
- it should compare:
  - current code evidence
  - open laps
  - recent wraps/handoffs
  - OpenSpec completeness markers

Expected output:

- stale blockers to close
- verification still required
- state files needing update

This would be especially useful after out-of-band fixes.

## 6. Give blocked automation a clearer human-escalation path
I did not see a strong operator-facing path for:

- "the code may now be fine"
- "the remaining blocker is environment/manual verification"
- "automation cannot continue cleanly"

Suggested direction:

- allow rally to emit a dedicated escalation artifact/state
- include:
  - concise reason
  - what was already completed
  - what exact human action is next
  - whether the relay should pause, downgrade, or reroute

That would be more useful than a series of retries followed by a paused lane and stale blocker work.

## 7. Defend against transcript/prompt-size blowups
The observed `argument list too long` and `bufio.Scanner: token too long` failures suggest at least some parts of the flow can accumulate too much inline context.

Suggested direction:

- compact long histories before relaunch
- avoid inlining extremely large transcripts in retry prompts
- prefer referenced artifacts/log paths over giant embedded payloads
- make "transcript too large" a recognized failure mode with an automatic compaction path

## 8. Consider automatic route fallback for unhealthy lanes
Prayer-app used:

- `senior = ['claude']`
- `verify = ['codex']`

From the outside, that looks brittle if a lane loses availability entirely.

Suggested direction:

- allow role routes to declare fallback runners
- especially for roles like `senior` or `verify`, where "do nothing until hourly retry succeeds" can stall the whole campaign

This does not have to be automatic for every failure class, but quota/auth/launch failures seem like strong candidates.

## 9. Keep OpenSpec and laps closer together when both are in use
In Prayer-app, completeness was split across:

- laps queue state
- wrapup summaries
- unchecked OpenSpec tasks

Suggested direction:

- if both systems are used together, add one narrow bridge:
  - either OpenSpec tasks get updated from lap completion
  - or verification tooling explicitly understands that laps may be ahead of `tasks.md`

Without that bridge, completeness reports will keep drifting toward false negatives.

## 10. Stronger semantics for freeze-recovery success
"Files committed, treating as success" may be reasonable for implementation work, but it feels dangerous for verification work.

Suggested direction:

- make recovery semantics role-aware
- for example:
  - implementation lanes may recover-as-success if commit + tests + wrapup are present
  - VERIFY lanes should require an explicit verification result artifact, not just file changes

## What I would prioritize first
If I were choosing the smallest high-value changes based on this single black-box case, I would start with:

1. lap/run integrity checking on completion
2. distinct handling for quota/launch failures
3. transcript/prompt compaction guardrails
4. a reconciliation command for stale blocker state

Those seem like the fastest path to preventing a repeat of the Prayer-app stall pattern without redesigning rally end to end.
