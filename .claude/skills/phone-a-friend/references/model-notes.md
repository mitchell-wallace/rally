# Model Notes

This file is the source of truth for phone-a-friend model choices. Update it immediately when the user gives feedback, when a slug changes, or when an agent failure has a recognizable signature.

## Canonical Slugs

These slugs are copied from rally's current real-backend testing guidance. If the user provides a newer slug, keep it verbatim and record the result instead of silently falling back.

| Harness | Slug | Current notes |
|---|---|---|
| `ag`/`agy` | `Gemini 3.5 Flash (High)` | Verified with `agy --print`. `agy` 1.0.0 has no CLI model flag, so Rally applies the model through Antigravity settings and restores the prior setting. |
| `cc` | `claude-haiku-4-5` | Cheapest/fastest Claude smoke-test default. Good for quick review or routine questions when depth is not critical. |
| `cx` | `gpt-5.4-mini` | Verified working for Codex relay. Good fast default for code review, planning checks, and bounded implementation questions. |
| `ge` | `gemini-3.1-pro-preview` | Verified working. Slower, useful for broad design, UI/component direction, alternative architectures, and divergent thinking. |
| `ge` | `gemini-3-flash-preview` | Verified working. Fast lighter Gemini option for quick critiques and small alternatives. |
| `op` | `opencode-go/kimi-k2.6` | Verified working when not rate-limited. Free tier can rate-limit after a few runs, roughly a 12h window. |
| `op` | `opencode/minimax-m2.5-free` | Verified working. Use exactly this prefix, not `opencode-zen/...`. |
| `op` | `zai-coding-plan/glm-5.1` | Verified working. Fast OpenCode option via the `zai-coding-plan` provider. |

Alias note: Antigravity is `ag` or `agy`; Gemini is `ge`, not `gm`. Rally rejects `gm` with `unknown agent alias`.

## User-Calibrated Strengths And Weaknesses

Record user-assigned taste and quality judgements here. Keep entries concrete.

| Date | Model | Strength / weakness | Evidence | Source |
|---|---|---|---|---|
| _none yet_ | | | | |

Example entry shape:

| 2026-05-15 | `ge:gemini-3.1-pro-preview` | Strong at first-pass UI layout ideas, but needs local polish pass | Designed settings component direction user liked; spacing needed tightening | User feedback |

## Failure Signatures

| Model / harness | Symptom | Likely cause | Response |
|---|---|---|---|
| `op` / `opencode-go/kimi-k2.6` | Silent hang for about 2 minutes, then freeze/pause; `.rally/state/agent_status.jsonl` records `paused` | Free-tier rate limit or provider stall | Let Rally pause/rotate. Try another `op` model or non-OpenCode route if the question is still useful. Record if timing/message changes. |
| Custom OpenCode harness with `command = ["opencode"]` | Starts TUI mode and does not exit cleanly | Missing `run` subcommand and JSON format | Use built-in `op` or `["opencode", "run", "$PROMPT", "--format", "json"]`. |
| `ge` / Gemini | Log may stay quiet for the whole run; `last activity` can count from start | Gemini CLI output behavior, not necessarily a freeze | For complex tasks use a longer freeze threshold if configuring Rally. Wait for clean exit when progress is plausible. |
| Gemini exit 41/55 in old notes | Workspace trust failure | Stale in current rally: Rally sets `GEMINI_CLI_TRUST_WORKSPACE=true` | Delete stale "Gemini unauthenticated/untrusted" notes if a future run confirms current behavior still works. |
| `cc` / Claude | Output includes `rate-limit` or `429 Too Many Requests` | Claude rate limit | Rally classifies as wait + resume when possible. Preserve `retry-after` details if present. |
| `cx` / Codex | `limit warning` appears with `completion generated` | Warning despite usable completion | Rally treats as success. Do not mark as failure unless output is incomplete. |
| Any harness | `unknown agent alias` | Bad alias such as `gm`, bad custom harness, or route typo | Run `rally routes check`; use canonical aliases. |

## Recording Rules

- Record exact model slugs, not family names.
- Record whether feedback came from the user, a verified run, or your own judgement.
- Prefer replacing stale status notes over stacking caveats.
- Keep subjective notes tied to task type. A model can be strong at UI ideation and weak at surgical Go patches.
- When asking the user for calibration, include the model and task type: "gemini-3.1-pro-preview designed this component direction; how do you like it?"
