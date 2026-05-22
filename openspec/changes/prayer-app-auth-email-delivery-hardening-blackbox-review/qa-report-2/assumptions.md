# Assumptions & Limitations

This report is a **black-box analysis**. It was produced by reading log files, git history, laps.json, progress.yaml, relay logs, and agent session transcripts from the container. No source code of rally or laps was read, and no rally internals were inspected. The following assumptions and limitations apply.

## Assumptions

1. **Rally role assignments follow `.rally/config.toml` routes**: JUNIOR → OpenCode, SENIOR → Claude, VERIFY → Codex, UI → Gemini. Observed in tries.jsonl `agent_type` and `lap_assignee` fields, which consistently matched this mapping.

2. **Laps task queue is FIFO**: Each `laps done` call consumes the next `isDone: false` task in `laps.json`. This is inferred from the relay behavior (tasks complete in order) and the Run 16 double-consumption bug. Not confirmed by reading laps source.

3. **`laps done` from a subdirectory triggers a hook failure**: The `laps-done-hook.sh` is resolved relative to CWD. When Claude ran it from `apps/backend/`, the hook wasn't found (exit 127). When retried from `/workspace/`, it succeeded. This is inferred from the Claude session transcript showing two `laps done` calls with different CWDs and different exit codes.

4. **Run 18 freeze recovery committed `c7a841f` and `0f4a8b7`**: The relay log shows Run 18 attempt 2 as a freeze detection with recovery, 31-minute runtime, 11 files changed, commit `0f4a8b7`. The git log shows `c7a841f` committed at 20:52 (during Run 18 attempt 2's window). I assume both commits were produced during this freeze-recovery cycle. The exact timing (which commit happened when within the 31-minute window) is not confirmed.

5. **Codex violated its VERIFY role to fix controllers**: Run 18 had 60 tool calls in attempt 1 and 0 tool calls in attempt 2 (freeze — tool call count may not have been reported). The commits changed controller files. I infer Codex performed the fix rather than stopping at diagnosis.

6. **`fork/exec /usr/bin/claude: argument list too long` indicates cumulative state growth**: This error (E2BIG) on Linux means the sum of argument + environment exceeded `ARG_MAX` (~2MB). Given it appeared late in the relay and worsened (150ms → 50ms exit times), I suspect state accumulated across runs without being freed.

7. **The monthly rate limit was Anthropic's org-level cap**: The try-29 log shows `rateLimitType: five_hour, overageStatus: rejected`. I interpret this as the monthly usage limit rather than a short-term rate limit, meaning no amount of waiting would resolve it within the same billing period.

## Limitations

1. **Claude session transcripts were not exhaustively reviewed**: The Claude JSONL sessions total ~38MB across 60+ files. I sampled the largest and most recent files. The exact conversation flow in every session (especially Run 16 double-consumption) may contain additional nuance.

2. **OpenCode sessions were not reviewed**: I did not locate or read OpenCode session transcripts. The OpenCode agent's behavior in Runs 1, 3, 4, 10, and 17 is inferred from commits and relay logs only.

3. **Codex sessions were not reviewed**: I did not locate or read Codex session transcripts. Run 18's behavior is inferred from git commits, relay logs, progress.yaml, and laps.json.

4. **Rally source code was not read**: All statements about rally's behavior (retry logic, freeze detection, agent spawning) are inferred from observed logs. Internal mechanisms may differ.

5. **Test results were not independently verified**: I did not run the test suite. All statements about test results are from agent summaries in progress.yaml and git commit messages.

6. **Laps source code was not read**: All statements about how `laps done` and `laps wrapup` work are inferred from session transcripts and observed behavior.

7. **Container may have additional state not discovered**: I searched common paths (`/workspace`, `/home/agent`, `/tmp`) but there may be rally/laps state in other directories.
