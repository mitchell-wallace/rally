# QA Report 2: auth-email-delivery-hardening — Rally Execution Post-Mortem

## Scope

This report analyses the rally relay execution of the `auth-email-delivery-hardening` OpenSpec change against `mycode/prayer-app`, running in Docker container `7c249b16a7e6` (dune-prayer-app-5d-default-agent-1) on 2026-05-21.

**Source data**: `.rally/tries.jsonl`, `.rally/progress.yaml`, `.rally/relays.jsonl`, `.rally/agent_status.jsonl`, `.laps/laps.json`, `~/.claude/projects/-workspace/*.jsonl`, git history on host.

---

## Executive Summary

The code is **substantively complete**. All 9 planned laps wrote the intended code, and the two blocker fixes (controller wiring, version comparison) were committed during Run 18's freeze-recovery cycle. However, **two laps tasks remain `isDone: false`** in `laps.json` and the final VERIFY automation (full test suite + broken-SMTP smoke) never ran.

The relay stalled at 21:15 UTC and was manually stopped. Root causes: a `laps done` CWD bug consumed a task without work done; rate limits blocked Claude; harness stdin/stdout failures killed subprocesses in <150ms; and rally had no human escalation path for these compound failures.

---

## Timeline

All times UTC, 2026-05-21.

| Time | Run | Agent | Assignee | Lap | Outcome |
|------|-----|-------|----------|-----|---------|
| 10:56 | 1 | OpenCode | JUNIOR | pray-f623 | Completed. Reset-password token input removed. |
| ~11:02 | 2 | Claude | SENIOR | pray-7a80 + pray-dc54 | Completed. Constant-time forgot-password + SW v8. Two laps consumed in one run. |
| ~11:18 | 3 | OpenCode | JUNIOR | pray-a536 | Completed. checkForUpdates() extracted. |
| ~11:25 | 4a | OpenCode | JUNIOR | pray-9a1b | **Rate-limit interrupt**, then retry succeeded. Settings "Check for updates" button. |
| ~11:40 | 5 | Codex | VERIFY | pray-5ed1 | Completed. Mid-task verify found SW cache/version-aware holes → created work-e03d. |
| 11:53 | 6 | Claude | SENIOR | work-e03d | **FAILED**. Rate-limit + 4 harness errors. Relay paused 60m. |
| 12:53 | 7 | Claude | SENIOR | work-e03d | **FAILED**. Harness error. Paused 60m. |
| 13:53 | 8 | Claude | SENIOR | work-e03d | **FAILED**. Harness error. Paused 60m. |
| 15:06 | 9 | Claude | SENIOR | work-e03d | Completed. Fixed SW v8 + updater + timing parity. |
| ~15:20 | 10b | OpenCode | JUNIOR | pray-0fda | Completed (retry after freeze). Migration + queue service. |
| 15:26 | 11 | Claude | SENIOR | pray-b893 | **FAILED**. Rate-limit + 4 harness errors. Paused 60m. |
| 16:26 | 12 | Claude | SENIOR | pray-b893 | **FAILED**. Harness error (153ms). Paused 60m. |
| 17:26 | 13 | Claude | SENIOR | pray-b893 | **FAILED**. Harness error. Paused 60m. |
| 18:26 | 14 | Claude | SENIOR | pray-b893 | **FAILED**. Harness error. Paused 60m. |
| 19:27 | 15 | Claude | SENIOR | pray-b893 | **FAILED**. Harness error. Paused 60m. |
| 20:36 | 16 | Claude | SENIOR | pray-b893 | Completed. Worker tick. **`pray-43a5` wrongly consumed.** |
| ~20:39 | 17 | OpenCode | JUNIOR | pray-1368 | Completed. Docs. |
| 20:42 | 18a | Codex | VERIFY | pray-a349 | **Rate-limit interrupt** after 3m. Found controller-wiring gap, created work-c905. |
| 20:42-21:13 | 18b | Codex | VERIFY | (freeze recovery) | **Freeze → recovery → treated as success.** Committed c7a841f (controller wiring) and 0f4a8b7 (version comparison). 11 files changed. |
| 21:15 | 19 | Claude | SENIOR | work-c905 | **FAILED**. 5 harness errors in 150ms. `fork/exec /usr/bin/claude: argument list too long`. Relay paused → **stopped during wait**. |

---

## Detailed Blow-by-Blow

### Runs 1–5: Smooth progress (10:56–11:48)
Five laps completed with only one rate-limit hiccup (Run 4a, recovered on retry). All code landed: reset-password UI, constant-time forgot-password, SW v8, checkForUpdates(), Settings button. Run 5 (VERIFY) found two gaps: SW cache/version-awareness not fully wired and forgot-password timing parity was weak. It correctly created blocker task `work-e03d` rather than proceeding.

### Runs 6–9: Harness instability + rate limits (11:53–15:06)
The first major stall. Run 6 hit rate-limit then 4 consecutive harness errors (subprocess died in 4s each with 0 tool calls). The hourly retry cycle ran 3 times (Runs 7, 8) with harness errors each time. Run 9 (at 15:06) finally succeeded — 12 files changed, 95 tool calls. The `work-e03d` blocker was cleared.

### Runs 10–15: The deep stall (15:20–19:27)
Run 10 succeeded on retry (queue migration). Then Runs 11–15 all failed: rate-limit + harness errors on Run 11, then 4 consecutive hourly retries all dying in 4–5s with 0 tool calls. The agent_status.jsonl shows 4 consecutive `retry_failed` events. This is when Claude was completely unable to start.

### Run 16: The critical bug (20:36)
Claude finally recovered and completed the worker tick (`pray-b893`). But during completion, the `laps done` call from a subdirectory caused the NEXT task (`pray-43a5` — controller wiring) to be consumed as "done" without any code being written. See [Root Cause #1](#root-cause-1-laps-done-cwd-bug) below.

### Run 17: Docs (20:39)
OpenCode completed docs cleanly.

### Run 18: VERIFY finds the hole, then fixes it (20:42–21:13)
Codex started the final verify (`pray-a349`). It immediately found that the controllers still called `authEmailDispatcher` inline — the queue infrastructure existed but was never wired. Codex created blocker task `work-c905` documenting the exact fix needed.

Then something unusual happened: Codex proceeded to **fix the controllers itself** despite its VERIFY role. Attempt 1 was rate-limited after 3m. Attempt 2 was a 31-minute freeze-recovery cycle where Codex committed **two fixes**:
- `c7a841f` (20:52): Fixed controller wiring in forgot_password_controller and register_controller, updated 7 files including tests
- `0f4a8b7` (21:12): Fixed app version update comparison in the updater, 4 files

The relay log records attempt 2 as `fail_reason: harness error` but `freeze recovery: files committed, treating as success`. Rally correctly captured the commits despite the harness freeze.

**However**, neither `work-c905` nor `pray-a349` was marked done in `laps.json`. The Codex session ended without calling `laps done` / `laps wrapup`.

### Run 19: Final death (21:15)
Claude was assigned SENIOR with `work-c905` as the next lap. Five attempts, all dying in 50–153ms with `fork/exec /usr/bin/claude: argument list too long`. The relay paused for 60m, then a stop was requested during the wait. The relay halted.

---

## Root Causes

### Root Cause #1: `laps done` CWD bug

In Run 16, Claude ran `laps done` from `apps/backend/` (the current working directory at the time). The hook script at `.laps/hooks/rally/laps-done-hook.sh` was not found relative to that directory (exit 127). Claude then retried from `/workspace`, which successfully ran `laps done`. But the first call had already consumed `pray-b893` (the worker tick), and the second call consumed `pray-43a5` (controller wiring) — a task for which **zero code was written**.

The `progress.yaml` for Run 16 records `laps_completed: [pray-43a5]`, not `pray-b893`. The worker code was committed, but the laps tracking recorded the wrong task as complete.

**Impact**: The controller-wiring task was closed prematurely. No agent was assigned to wire the controllers until VERIFY discovered the gap in Run 18.

**Suggested fix**: `laps done` should resolve hook scripts relative to the repo root (or `.laps/` directory), not the CWD. Alternatively, laps should be idempotent — calling `laps done` twice for the same task should be safe.

### Root Cause #2: No verification that a "done" task's files were changed

When `pray-43a5` was marked done, the git working tree had no changes to `forgot_password_controller.ts` or `register_controller.ts`. If `laps wrapup` (or the done hook) had checked whether the files named in the task description had been modified in the working tree, it would have caught this.

**Suggested fix**: Laps task descriptions should optionally declare expected file paths. On `laps done`, verify those files have been modified since the task was started. On mismatch, fail the done call or warn with a prompt.

### Root Cause #3: Rate limits + no circuit breaker

Anthropic rate limits (both short-term and monthly usage caps) hit Claude hard:
- Try 4a, 6a, 11a, 18a: `claude rate-limit interrupt`
- Try 18a specifically hit the **monthly org usage limit** with `overageStatus: rejected`

Each rate-limit interrupt burned one retry attempt in the relay's `retry_budget: 5`. When rate limits and harness errors combined (e.g., Run 6: rate-limit → 4 harness errors), the 5-retry budget was exhausted immediately, triggering a 60m pause.

**Suggested fix**: Distinguish between recoverable and non-recoverable failures. Rate limits on monthly caps are not recoverable within the same relay session — escalate immediately rather than burning retries. The harness should also detect "rate limit" in the failure reason and use a different retry strategy (e.g., wait for rate-limit reset window).

### Root Cause #4: Harness signal loss (`argument list too long`, subprocess crash)

From Run 6 onward, a pattern emerged where the rally harness would spawn a Claude subprocess but stdin/stdout would immediately disconnect. The subprocess would exit in 50ms–5s with 0 tool calls and 0 files changed. The final error was `fork/exec /usr/bin/claude: argument list too long`.

This suggests one of:
- The harness was passing too large a context/argument to the Claude CLI
- The accumulated state (progress.yaml, laps.json, handoff notes) grew beyond E2BIG limits
- A resource leak in the relay loop (cumulative context from prior runs not being freed)

**Impact**: 17 out of 34 tries (50%) failed with harness errors, including all of Runs 7, 8, 12–15, and 19. This was the dominant failure mode.

**Suggested fix**: Cap the argument size passed to subprocesses. Use a file-based handoff (write state to a temp file, pass the file path) instead of command-line arguments. Investigate whether state accumulates across runs and add periodic cleanup.

### Root Cause #5: No human escalation path

When the relay hit compound failures (rate-limit + harness errors), the only behavior was: burn retries → pause 60m → retry. After 9 consecutive pauses/retries spanning 9 hours, the relay was manually stopped. At no point did any agent or the harness itself attempt to surface the stall to a human.

**Suggested fix**: After N consecutive pause cycles (e.g., 3), emit a human notification (webhook, Slack, file in a watched directory, etc.). The relay should distinguish between "task is hard, agent is working" and "infrastructure is broken, nothing can happen."

### Root Cause #6: VERIFY role boundary ambiguity

Codex (assigned VERIFY) was told to verify only. When it found the controller-wiring gap, it correctly documented it as `work-c905`. But then, during the freeze-recovery cycle, it also **fixed the code**. This was the right outcome for the codebase but violated the role separation.

If Codex had strictly adhered to its role, the gap would have been documented but unfixed, and Claude (assigned SENIOR in Run 19) would have needed to pick it up — but Claude was killed by harness errors. The fact that Codex fixed it is what saved the codebase, but it shouldn't have been necessary.

**Suggested fix**: When VERIFY creates a blocker task, rally should auto-assign it to a SENIOR/JUNIOR run immediately (in the same relay loop), rather than hoping the next `laps done` call picks it up. Alternatively, allow a VERIFY agent to optionally fix small/moderate blockers it finds, with a flag to indicate "I fixed this myself."

### Root Cause #7: Laps tracking never updated after the fix

After Codex committed `c7a841f` (fixing the controllers), neither `work-c905` nor `pray-a349` was marked done in `laps.json`. The Codex session likely ended during the freeze recovery without a clean `laps wrapup` call. The `progress.yaml` for Run 18 shows `laps_completed: none` — rally correctly recorded that no lap was formally completed.

But the code WAS fixed. The disconnect between code state and task tracking is the key staleness issue.

**Suggested fix**: A post-relay reconciliation step should compare committed file changes against open laps task descriptions and flag tasks where the code matches the requirement but the task is still `isDone: false`.
