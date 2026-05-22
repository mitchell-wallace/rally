# Process Issues & Improvement Suggestions

## Issue 1: Laps done is CWD-dependent

**What happened**: `laps done` from `apps/backend/` failed because `.laps/hooks/rally/laps-done-hook.sh` doesn't exist relative to that directory. Claude retried from `/workspace/`, which consumed the NEXT lap in the queue.

**Severity**: Critical — caused a task to be marked "done" with zero code written.

**Suggestion**: Resolve hook paths relative to the repo root (git root or `.laps/` directory), not CWD. If `laps` must remain CWD-aware, it should emit a clear error when run from a subdirectory and refuse to operate, rather than failing silently on the hook and succeeding on retry.

---

## Issue 2: No "task done = files changed" cross-check

**What happened**: `pray-43a5` was marked done, but the git working tree showed zero changes to `forgot_password_controller.ts` or `register_controller.ts` — the files the task explicitly named.

**Severity**: Critical — allowed a phantom completion to propagate through the pipeline.

**Suggestion**: Laps task descriptions should declare expected file paths (either explicitly or by parsing the description). The `laps done` hook should verify those files have been modified since the task began. On mismatch, fail the done call. This could be a configurable level: warn, prompt, or reject.

---

## Issue 3: Rate limits burn retry budget identically to harness errors

**What happened**: The relay's `retry_budget: 5` treats all failures the same. A monthly API rate limit (Anthropic org cap, `overageStatus: rejected`) burns a retry attempt just like a harness signal loss. The rate limit is not recoverable within the same session, so spending retries on it is wasteful.

**Severity**: High — delayed progress by hours as the relay kept retrying against a hard cap.

**Suggestion**: Classify failure reasons into categories:
- `rate_limit_monthly` → immediate human escalation, do not retry automatically
- `rate_limit_short_term` → wait for reset window, then retry (lightweight — don't count against retry budget)
- `harness_error` → retry up to budget, then escalate
- `agent_error` → retry up to budget, then escalate

---

## Issue 4: Harness argument list overflow (`E2BIG`)

**What happened**: 17 of 34 tries (50%) failed with `fork/exec /usr/bin/claude: argument list too long` or subprocess death in <150ms with 0 tool calls. This began mid-relay and worsened over time, suggesting cumulative state growth.

**Severity**: Critical — made Claude completely unusable for the final 8 runs.

**Suggestion**:
- Cap the argument size passed to subprocesses.
- Use file-based handoff for large context (write state JSON to a temp file, pass the file path as the argument).
- Add periodic context cleanup between runs.
- Monitor argument size and warn when approaching `getconf ARG_MAX` (typically 2MB on Linux).

---

## Issue 5: No human escalation trigger

**What happened**: The relay cycled through 9 pause/retry cycles over 9 hours (11:53 to 21:15) without any human being notified. The final 4 hourly retries (Runs 12–15) all failed identically in <5 seconds. No agent or harness component surfaced the stall.

**Severity**: High — the relay would have continued indefinitely until manually stopped.

**Suggestion**:
- After N consecutive pause cycles (e.g., 3) where no progress is made, emit a notification.
- Notification could be: write a file to a watched directory, send a webhook, or log to stderr with a distinctive prefix.
- Rally could also expose a `/status` endpoint or a `rally status` command for human monitoring.
- The relay should distinguish between "agent is working, slow progress" (file changes, tool calls happening) and "infrastructure is broken, nothing is happening" (0 tool calls, sub-second exit).

---

## Issue 6: VERIFY role boundary creates an unnecessary serial dependency

**What happened**: Run 18 (VERIFY/Codex) found the controller-wiring gap. It documented it as `work-c905` and then (during freeze recovery) fixed it anyway. If it had strictly respected its role, the fix would have waited for Run 19 (Claude/SENIOR) — which failed with harness errors. The role boundary was violated, but this was the **correct outcome** for the codebase.

**Severity**: Medium — the current design creates a serial dependency (VERIFY finds → waits for next SENIOR run → SENIOR fixes → waits for next VERIFY). This doubles the minimum number of successful runs needed to close a blocker.

**Suggestion**:
- Option A: Allow VERIFY to fix small/moderate blockers it finds, with a flag indicating "I fixed this myself, skip the SENIOR assignment."
- Option B: When VERIFY creates a blocker, rally should immediately queue a SENIOR run (in the same relay loop, not waiting for the next `laps done` cycle) and then automatically requeue a VERIFY run after the fix.
- Option C: Combine VERIFY + SENIOR into a single "fix and verify" agent role that does both in one session.

---

## Issue 7: tasks.md checkboxes never updated

**What happened**: `openspec/changes/auth-email-delivery-hardening/tasks.md` has 39 unchecked boxes. Despite all work being code-complete, no agent ever checked a single box. The `work-c905` blocker task description explicitly called this out: "openspec/changes/auth-email-delivery-hardening/tasks.md remains 39/39 unchecked despite lap queue entries being marked done."

**Severity**: Low (cosmetic) but symptomatic — it shows that tasks.md is dead weight in the current workflow. Agents track work via laps, not tasks.md.

**Suggestion**: Either:
- Make tasks.md the source of truth and have `laps` sync to it (laps are derived from tasks.md sections), OR
- Drop tasks.md checkboxes as a requirement and treat laps.json as the canonical task list. If tasks.md is kept for human readability, add a script that syncs checkboxes from laps.json state.

---

## Issue 8: Relay does not auto-resume after manual stop

**What happened**: The relay was stopped during a 60m pause at 21:15. The stop was intentional (the user saw it was stuck), but there's no mechanism to resume from where it left off. The laps state persists, but a new relay would need to be started manually.

**Severity**: Low — manual restart is acceptable for now.

**Suggestion**: Allow `rally resume` to pick up from the last saved state (laps.json, progress.yaml) and continue the relay.

---

## Issue 9: Multiple laps consumed per run without traceability

**What happened**: Run 2 consumed two laps (`pray-7a80` + `pray-dc54`) in a single run. Run 16 consumed `pray-b893` AND `pray-43a5`. There's no per-run record of WHICH laps were attempted, only which were completed. This makes it hard to trace which run did which work.

**Severity**: Low — but made debugging the `pray-43a5` phantom completion harder.

**Suggestion**: Log every `laps done` call with timestamp and lap ID, regardless of success/failure, to enable post-hoc tracing. The `tries.jsonl` or `progress.yaml` should record `laps_attempted` in addition to `laps_completed`.
