# Draft: Harden outing failure handling (phase B)

Status: scoped 2026-07-05 as the evidence base for the phase-B reliability
fixes — crew-chief queue laps `rall-178b` (unauthenticated harnesses),
`rall-2730` (recovery fallback on repeated retries), and `rall-7fe9` (codex
premature sidelining + land phase B to dev). Supersedes the evidence role of
`improve-harness-consistency` (complete 89/89, shipped in 0.12.0, archived
2026-07-05); the shapes below were all captured **after** those fixes landed,
so they are the current failure surface, not stale pre-0.12 noise.

**Evidence sources.** New Relic is not reachable from this container (no
`newrelic` CLI, no user API key — the AGENTS.md NR instructions describe the
operator's own machine). Everything below is grounded in primary sources that
live here:

- `.rally/state/tries.jsonl` (committed) — 171 tries across relays 1–9,
  2026-07-01 → 2026-07-05, driven by 0.12.0+ builds.
- `.rally/state/agent_status.jsonl`, `hook-audit.jsonl`, `.rally/summary.jsonl`.
- Codex on-disk session logs `~/.codex/sessions/2026/07/…` (62 MB, intact) —
  per-second correlated with the failing tries.
- A live probe of the unauthenticated `agy` CLI run 2026-07-05 with rally's
  exact print-mode invocation.

A New Relic refresh (post-0.12 `RallyFailure`/`RallyTry`/`RallyDiagnostic`,
appName `Rally CLI`, account 8182741) should be backfilled from a credentialed
host to confirm these shapes hold across operators, but the local corpus is
sufficient to scope the fixes.

## Corpus summary (local, relays 1–9)

| agent | outcome / fail_reason | count |
|---|---|---:|
| opencode | completed | 58 |
| antigravity | completed | 43 |
| codex | completed (clean) | 16 |
| codex | failed: no changes made | 12 |
| codex | failed: harness launch error | 10 |
| codex | failed: agent error | 5 |
| codex | completed but fail_reason "harness error" | 5 |
| codex | failed: rate limit / usage limit | 6 |
| claude | failed: usage limit (resets in 5h) | 3 |
| opencode | failed/timeout/cancelled (various) | 10 |

Codex is the outlier: 38 of its 54 tries carry a failure label, and the
sections below show a large share of those labels are wrong.

## Shape A — unauthenticated harness, antigravity first (`rall-178b`)

`agy` in this container is currently **unauthenticated** (auth expired some
time after relay 5 on 2026-07-02, where all 43 antigravity tries completed).
Live probe with rally's exact invocation
(`agy --log-file=… --print-timeout=… --print "…"`):

- stdout: `Authentication required. Please visit the URL to log in:` followed
  by a full Google OAuth URL, then `Waiting for authentication (timeout
  30s)... Or, paste the authorization code here and press Enter:`, then
  `Error: authentication timed out.`
- **Exit code 0.** No `execErr`, no parseable result. Each try silently burns
  ~30 s blocked on interactive input that can never arrive.
- glog (`--log-file`) shape, the clean fixture for the parser:
  `E0705 … error getting token source: You are not logged into Antigravity.`
  (repeated), plus `W … Singleflight refresh failed: …` variants.
- Parser hazard: the same glog also contains `I … Auth succeeded, refreshing
  features and managers` *while unauthenticated* — do not key on "Auth
  succeeded".

None of the current regexes match any of this text:
`geminiAuthOrEligibilityRe` (`internal/reliability/antigravity.go:17`) wants
`IneligibleTierError|UNSUPPORTED_CLIENT|no longer supported…|Error
authenticating`; claude/codex want `authentication failed|invalid api key|
unauthorized`. So the try falls through to the generic classifier — with exit
0 and no changes it lands in `agent error` / `no changes made`, retries burn,
and nothing tells the operator to re-auth.

What already exists and what is missing:

- `CategoryAuthOrProxy` exists, maps to `FailureAgent` (does not freeze), and
  `run_retry_decide.go:99` bounds it to a single attempt. Good — **when it is
  detected**.
- Nothing sidelines the provider relay-wide. Auth does not self-heal; the next
  lap on the same route pays the 30 s tax again. Live config makes this acute:
  `senior = ['ag:opus', …]` puts unauthenticated antigravity first, so *every*
  senior outing starts with a doomed try. (Benching exists for usage limits
  with `reset_at`; auth needs the no-reset equivalent with an operator-facing
  "run `agy` and log in" message.)

Fix scope (per lap): detect auth-failure evidence per harness (antigravity
first, from the captured stdout + glog shapes), classify as `auth_or_proxy`,
sideline the provider for the relay with a clear message + telemetry, add
parser fixtures from the real text above.

## Shape B — repeated retries of the same lap without progress (`rall-2730`)

Two distinct sub-shapes in the corpus:

1. **Retrying a lap that is already done.** `rall-3cc4` (verify lap, relay 7):
   codex attempt 1 completed the verification and its `laps done` hook fired —
   `hook-audit.jsonl` records `laps-done rall-3cc4` at 07:08:03 and the lap
   record shows `isDone: true, completedAt: 07:08:03`. Rally nevertheless
   recorded attempt 1 as failed (`harness launch error`, see Shape C), then
   ran attempts 2–5 against the already-done lap (all failed:
   launch-error/rate-limit/no-changes). `rall-00ca` is identical
   (hook fired 08:25:02; attempts 2–5 burned afterwards). The retry decision
   does not re-consult lap/claim doneness after a "failed" try.
2. **Genuine burn with no fallback.** `rall-e7b0` (relay 3): five straight
   codex `agent error` tries, budget exhausted, lap stranded. A `recovery`
   route exists in config (`recovery = ['cx:g54', 'cx:g55', 'cl:opus',
   'ag:opus', 'op:ds']`) and roles v2 defines the recovery role, but nothing
   ever falls back to it.

Fix scope (per lap): before retrying, re-check the pinned lap's recorded/claim
state (a fired `laps done` hook must end the outing as success even if try
classification said otherwise); when the same lap accumulates repeated failed
tries/outings across drivers, fall back to the recovery route or halt with
actionable state instead of spinning; respect retry budgets; emit telemetry;
tests for both sub-shapes.

## Shape C — codex misclassification sidelines it prematurely (`rall-7fe9`)

The 0.11.2 corpus blamed silent exit-1; the post-0.12 shape is different and
worse: **codex tries that completed real work are recorded as failures.**
Every failed codex try in relay 7 has an on-disk rollout at the exact start
second, and the rollouts end in `task_complete` with `completed: true`:

- try-154 (`rall-3cc4` #1) — recorded `harness launch error`; rollout
  `2026-07-03T07-04-47` ends `task_complete`, summary "No findings for tasks
  2.1-2.3. I verified the stable-package slice…". This is the try whose
  `laps done` hook fired (Shape B.1).
- try-157 (`rall-3cc4` #4) — recorded `no changes made`; rollout ends
  `task_complete`, "No findings. The stable-package verification slice…
  remains solid".

Consequences chain: `harness_launch` maps to `FailureInfra`
(`internal/reliability/category.go`) → feeds the freeze counter →
`agent_status.jsonl` shows codex **paused** at 08:49:48 for "3 consecutive try
failures" — at the same timestamp `summary.jsonl` (relay-7-run-9) records its
successful verification summary. Codex sidelined for work it did correctly.

Leads for the fix lap (root cause not yet pinned; investigate before coding):

1. **"no changes made" punishes verify-role laps.**
   `run_attempt_classify.go:70`: no file changes + runtime < 3 min + no
   handoff ⇒ failed. A verify/review/architect lap's *correct* outcome is
   often zero file changes. 12 of the corpus failures carry this label. The
   heuristic needs role/completion awareness (structured result said
   `completed: true`; `laps done` may have fired).
2. **`harness launch error` on tries with a completed session.** The
   session-log fallback shipped in 0.12.0
   (`internal/harness/codex/codex_sessionlog.go`) classifies
   exit-nonzero-with-no-matching-log as `harness_launch`, and matching
   requires first-line `session_meta.cwd == WorkspaceDir`. The relay-7
   rollouts record `cwd: /workspace`. If the matcher's workspace comparison
   missed (path mismatch/normalisation), a healthy codex session is invisible
   and every failure becomes `harness_launch` → infra → freeze. Reproduce the
   matcher against the preserved rollout files
   (`~/.codex/sessions/2026/07/03/rollout-2026-07-03T07-04-47-*` etc.).
3. **`completed` tries carrying fail_reason `harness error`** (5 in corpus,
   `run_attempt_classify.go:56` sets it when `execErr != nil`): explain how a
   try both completes and has an exec error; likely the same capture problem.

Fix scope (per lap): fix classification so codex stays in rotation when the
failure is transient or not its fault; a try whose structured output or
session log shows completion must not be recorded as a launch failure; tests
with the preserved rollout files as fixtures. Then land all phase-B work to
dev.

## Cross-cutting telemetry gap (small, fold into whichever lap touches it)

Relays 6, 8, 9 ended instantly with `completed_iterations: 0` and no recorded
cause — `relays.jsonl` has no end-reason field, so an instant-exit relay is
indistinguishable from a healthy no-op. Record an end reason (config error,
empty queue, cancelled, …) on the relay record.

## Out of scope

- Route-order policy (e.g. whether `ag:opus` should lead `senior`) — operator
  config, not code.
- Re-opening `improve-harness-consistency` workstreams; that change is
  archived. Its still-relevant design notes (codex session-log event layout,
  opencode disk-log filtering) are cited above where they apply.
- New harness integrations, resilience-cascade rewrites, TUI work.
