## Draft: 0.10.0 Release — Reliability, Visibility, and Model Variant Routing

## Intent

Ship 0.10.0 with six grouped behaviors:

1. **Reasoning/variant support** (role-aware model aliasing)
2. **Lap pin mismatch downgrade** from hard failure to warning with immediate handoff behavior
3. **Run-number visibility and role + runner headers** aligned to `run`/state terminology
4. **Operator cancellation semantics** for skip, graceful stop, and quit-now paths
5. **Tail usability improvements**, including live run targeting and optional syntax highlighting
6. **opencode usage-limit detection & reset parsing** so subscription-provider 5h/weekly/monthly limits bench the quota scope instead of burning the run budget

The default in this batch is explicit warning/cancelled visibility without treating operator-driven exits or lap mismatches as hard terminal failures.

## Current signal quality (research)

Sentry evidence using `sentry` confirms the observed shape:

- `wrong_lap_consumed`: `RALLY-4`, `RALLY-6`, `RALLY-C` (`error`, user-facing event message: `wrong_lap_consumed`)
- `multi_lap_consumed`: `RALLY-8`, `RALLY-9` (`error`, user-facing event message: `multi_lap_consumed`)
- `provider limit signal`: `RALLY-2` (`info`, tags include `failure_category=short_rate_limit`, `event_kind=limit_signal`)

The mismatch events currently carry the standard relay/run/try tags but do not
carry `event_kind` or `failure_category`; those tags are part of the intended
fix. The `rally tail` spike also showed that default tail output can come from
previous completed tries because active tries are not persisted until completion
and `run-state.json` has no active try pointer today.

Spike 2 (`spike-2-report.md`) further confirmed that opencode subscription
providers hitting a usage limit are misclassified as `agent_error`, not
`usage_limit`, so the quota scope is never benched:

- `agent_error` terminal failures (no bench): `RALLY-Q`
  (`opencode:zai-coding-plan/glm-5.2`), `RALLY-K`
  (`opencode:opencode-go/kimi-k2.7-code`), `RALLY-D`
  (`opencode:opencode-go/deepseek-v4-pro`) — all `level=error`, no `event_kind`.
- Live provider signatures (opencode server log): opencode-go emits
  `AI_APICallError: Monthly usage limit reached. Resets in 7 days. …` (and an
  `AI_RetryError: Failed after 3 attempts. Last error: …` wrapper); zai emits
  `AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at
  2026-06-16 18:29:51`.

Three compounding causes: (A) opencode retries provider errors internally and
emits nothing to the JSON stream meanwhile, so the 180s stall kill usually fires
before any limit text appears; (B) the parser keys on opencode-native error
names the real provider errors never use (they arrive as `AI_APICallError` /
`AI_RetryError` under opencode's `UnknownError` catch-all); and (C) the
"Resets in 7 days" / "reset at <timestamp>" phrasings do not parse, so even a
correctly categorized limit benches for the wrong default-5h window. The
`QuotaScope` benching machinery is already correct and provider-agnostic — only
the upstream classification, reset parsing, and signal observability are wrong.

## Decisions to lock in for 0.10.0

1. **Lap pin mismatch remains classified for observability but is not treated as an operator-grade failure.**
   - Keep the mismatch reason values and include them in evidence.
   - The run should proceed as a warning path and hand off to next scheduler entry (no retry-as-trying-to-cleanup path in this release).
   - User action is to fix state in follow-up work; no inline auto-resume cleanup.

2. **`-i` / run counter output must reflect runs, not laps.**
   - Non-laps mode header uses `run: X/Y` with run index.
   - Lap title remains visible where available; lap totals remain in dedicated lap fields only.
   - The wording and tests should use `run` terminology to avoid “run looks like lap count” confusion.

3. **Header line should include role + harness/model context.**
   - Example format: `VERIFY: codex - <model> - started 16:40`.
   - Keep concise, one per-run summary line.
   - Preserve fallback for no role/lap context.

4. **Reasoning levels / variants**
   - Add configurable role-to-variant aliases (for example `verify = "g55-xh"`, `junior = "g55-l"`).
   - Resolve per route role lane; keep existing route model resolution semantics and aliases.
   - Keep model aliases compatible with current harness model alias behavior.
   - Harness-specific effort injection is opt-in where supported: codex uses `-c model_reasoning_effort=<value>`, claude uses `--effort`, opencode uses `--variant`, gemini has no usable effort flag, and antigravity encodes effort in model names.
   - Unknown or unsupported reasoning values should warn during Rally config/runtime validation, not hard-fail before the harness runs.

5. **Operator exits should render as cancelled, not failed.**
   - Ctrl+S skip, graceful stop that cancels/drains an attempt, and quit-now must override ordinary executor error handling.
   - The stored try/run outcome should extend the existing `TryOutcome` model with `cancelled` plus an explicit source (`skip`, `graceful_stop`, `quit_now`) rather than `failed: harness error`.
   - Console output should use muted/grey styling, not red failure or green success.
   - Cancelled attempts should not count as infra/agent failures, freeze signals, retryable failures, or operator-worthy failure captures.

6. **`rally tail` should track active try reliably.**
   - Keep `--try N` explicit behavior unchanged.
   - Default `--try 0` should prefer active-in-flight context instead of last persisted completed try if one is running.

7. **Highlighting for tail output is enabled via mode flag, default-off for zero-cost start.**
   - Default remains plain output.
   - Add at least a low-cost syntax-aware heuristic mode.
   - Add optional richer mode behind a dependency where feasible.

8. **opencode subscription-provider usage limits must bench the quota scope.**
   - Broaden `ParseOpencodeError` to recognize the `AI_APICallError` /
     `AI_RetryError` / `UnknownError`-wrapped usage-limit signatures for
     `zai-coding-plan`, `opencode-go`, and generic providers.
   - Parse opencode's reset phrasings ("Resets in N days/hours/minutes" and
     absolute "reset at <timestamp>") into `ResetAfter`/`ResetAt` so the bench
     lasts until the real reset instead of the default 5h.
   - Make the limit observable despite opencode's internal retry: when a try
     stalls or errors without a usable result, surface usage-limit evidence from
     opencode's server-log tail for that session.
   - No bench-side change: `QuotaScope` + `benchResetDeadline` already do the
     right thing once the failure carries `usage_limit` with reset timing.

## Resolved review items before implementation

1. Role-level alias entries live in `[reasoning]`, with harness-scoped aliases resolved after harness selection.
2. Lap mismatch events are logged as telemetry `LevelWarning` diagnostics with `event_kind=lap_pin_mismatch` and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed`, but do not become operator-worthy failures by default and do not attach `failure_category` unless the try has a real failed lifecycle outcome.
3. Syntax highlighting remains opt-in; default tail output stays plain.
4. Default non-laps headers put model text on the same line with role/harness.
5. Ctrl+X graceful stop cancels/drains the active attempt, records `cancelled` with source `graceful_stop`, then stops the relay.
