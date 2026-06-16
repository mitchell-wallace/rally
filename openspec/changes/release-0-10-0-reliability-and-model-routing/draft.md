## Draft: 0.10.0 Release — Reliability, Visibility, and Model Variant Routing

## Intent

Ship 0.10.0 with five grouped behaviors:

1. **Reasoning/variant support** (role-aware model aliasing)
2. **Lap pin mismatch downgrade** from hard failure to warning with immediate handoff behavior
3. **Run-number visibility and role + runner headers** aligned to `run`/state terminology
4. **Operator cancellation semantics** for skip, graceful stop, and quit-now paths
5. **Tail usability improvements**, including live run targeting and optional syntax highlighting

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
   - The stored try/run outcome should be `cancelled` with an explicit source (`skip`, `graceful_stop`, `quit_now`) rather than `failed: harness error`.
   - Console output should use muted/grey styling, not red failure or green success.
   - Cancelled attempts should not count as infra/agent failures, freeze signals, retryable failures, or Sentry failure captures.

6. **`rally tail` should track active try reliably.**
   - Keep `--try N` explicit behavior unchanged.
   - Default `--try 0` should prefer active-in-flight context instead of last persisted completed try if one is running.

7. **Highlighting for tail output is enabled via mode flag, default-off for zero-cost start.**
   - Default remains plain output.
   - Add at least a low-cost syntax-aware heuristic mode.
   - Add optional richer mode behind a dependency where feasible.

## Review items before implementation

1. Do we want role-level alias entries to be separate from route entries (recommended) or inline in route tokens only?
2. Should lap mismatch events remain always logged with `event_kind=lap_pin_mismatch`/`failure_category=lap_pin_mismatch`, or should they only be in log lines + warning footers?
3. Should syntax highlighting default to `heuristic` and keep richer mode behind `--tail-highlight=chroma` only?
4. For default non-laps behavior, do we want model text on the same line with role or as a separate `model:` dim line?
