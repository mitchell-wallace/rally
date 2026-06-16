## Proposal: 0.10.0 — Reasoning Levels, Run Semantics, and Relay Visibility

## Problem statement

Rally currently conflates some run/lap signals:

- lap-pin mismatch (`wrong_lap_consumed`, `multi_lap_consumed`) is treated as hard terminal failure, which over-escalates transient/clean-up cases.
- operator cancellation paths can be reported as failed harness errors even when the operator explicitly skipped, stopped, or quit.
- run counters shown in headers are interpreted as lap counters in active runs.
- role + model context is under-shown during run execution.
- `rally tail` defaults to the most recently completed try, and on a fresh workspace errors before the active try is persisted.
- log highlighting is plain text only.

This release introduces warning-first mismatch handling, first-class cancelled outcomes, role-aware variant selection, and cleaner visibility with run-oriented output and active-tailing.

## Target outcomes

1. **Release-ready behavior and diagnostics**
   - Lap pin mismatch remains observable but is **not** a hard failure.
   - It routes to the next scheduler candidate as part of normal handoff behavior.

2. **State clarity in UI and logs**
   - Replace run counters with `run` semantics (`run: X/Y`) for non-laps flow.
   - Add role-first header output with harness/model context.
   - Render operator-cancelled attempts as muted/grey `cancelled` outcomes, never red failures or green successes.

3. **Config-driven reasoning variants**
   - Add role-level variant aliases for model-level routing preferences.
   - Support harness-specific effort injection where available: codex via `-c model_reasoning_effort=<value>`, claude via `--effort`, opencode via `--variant`; gemini is unsupported and antigravity uses model-name aliases.
   - Maintain compatibility with existing `[routes]` and `[harness.*.models]` syntax.

4. **`tail` usability with active run detection**
   - Default `--try 0` selects active run target when available.
   - Preserve explicit historical `--try N` semantics.

5. **Syntax highlighting options**
   - Offer opt-in line-highlighting and optional richer highlighter mode.

## Scope

- in-scope for this change: `internal/config`, `internal/routing`, `internal/relay`, `internal/style`, `internal/store` (if needed for tail targeting), `cmd/rally`, `README.md`, plus targeted tests and changelog notes.
- out-of-scope for 0.10.0: introducing a second telemetry class model for mismatches and changing retry budget semantics beyond existing behavior.

## Decisions

1. **Lap mismatch policy**
   - `wrong_lap_consumed` and `multi_lap_consumed` are warning-level observations.
   - They should still be persisted, but **not** flagged as operator errors by default.
   - Route action is handoff/next candidate, not resume-tidy-for-the-same-run.

2. **Counter model**
   - `-i` progress in non-laps mode is run-state count (`run: current/target`).
   - Lap titles remain visible and lap bookkeeping appears only where lap-backed context exists.

3. **Run header format**
   - Use role + harness/model in each block header for quick routing and model traceability.

4. **Research-backed limit handling**
   - Keep limit signals as low-severity events when `failure_category=short_rate_limit` (confirmed as info-level patterns in `sentry`).
   - Keep operator-grade `event_kind` tags available for existing alerting filters.

5. **Cancelled outcome policy**
   - Ctrl+S skip, graceful stop that cancels/drains a running attempt, and quit-now are operator intent, not harness failure.
   - These paths override normal executor exit/error handling and persist/display `cancelled` with source metadata (`skip`, `graceful_stop`, `quit_now`).
   - Cancelled attempts do not increment retry/freeze/failure counters and do not emit Sentry failure captures.

## Open review points (please confirm)

1. Confirm this release should **not** attempt an in-place resume tidy pass for lap mismatches.
2. Confirm whether role-level variant mapping belongs in `[reasoning]` (recommended) or a dedicated per-runner table.
3. Confirm preferred highlighting default/mode set for `tail` and dependency budget.
