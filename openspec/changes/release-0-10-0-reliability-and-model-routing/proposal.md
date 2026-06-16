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

6. **opencode usage-limit detection**
   - Classify opencode subscription-provider (`zai-coding-plan`, `opencode-go`)
     and generic usage limits as `usage_limit`, not `agent_error`.
   - Parse the 5h/weekly/monthly reset timing so the quota scope benches until
     the real reset rather than the default 5h fallback.
   - Surface the limit even when opencode's internal retry stalls the run before
     the JSON error event is emitted.

## Scope

- in-scope for this change: `internal/config`, `internal/routing`, `internal/relay`, `internal/reliability`, `internal/agent` (opencode executor evidence), `internal/style`, `internal/store` (if needed for tail targeting), `cmd/rally`, `README.md`, plus targeted tests and changelog notes.
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

6. **opencode usage-limit detection (spike-2)**
   - opencode subscription-provider limits arrive as `AI_APICallError` /
     `AI_RetryError` under opencode's `UnknownError` catch-all — not the
     opencode-native `UsageLimitError`/`QuotaExceededError` the parser keys on —
     so `ParseOpencodeError` must match the wrapper signatures and the human text.
     Confirmed third-pass: the structured error reaches only the server log as a
     **flat `error.error="<Wrapper>: <message>"` field** (never a nested
     `data.message` on stdout), so the matcher runs against that flat value.
   - Reset windows are phrased as space-separated spans ("Resets in 7 days") and
     absolute timestamps ("will reset at 2026-06-16 18:29:51", local time / no TZ
     marker); add opencode reset parsing rather than overloading the gemini regex.
   - opencode retries provider errors internally and emits nothing to the JSON
     stream meanwhile, so the 180s stall kill usually fires before the limit text
     appears; surface usage-limit evidence from opencode's server-log tail
     (`opencode.log` only) so the signal is observable despite the stall. The
     session is correlated without stdout via `message=created … directory=
     <WorkspaceDir>` (provider+window fallback).
   - Verified live: `RALLY-Q` (zai-coding-plan), `RALLY-K`/`RALLY-D` (opencode-go)
     are all tagged `agent_error` with no quota bench. No bench-side change is
     needed — `QuotaScope` + `benchResetDeadline` already bench correctly once the
     failure carries `usage_limit` with parsed reset timing.

## Resolved review points

1. This release does **not** attempt an in-place resume tidy pass for lap mismatches.
2. Role-level variant mapping belongs in `[reasoning]`.
3. Tail highlighting remains opt-in with `off` as the default.
4. Ctrl+X graceful stop changes semantics in this release: it cancels/drains the active attempt, records `cancelled` with source `graceful_stop`, then stops the relay.
5. Lap mismatch diagnostics use telemetry `LevelWarning` without becoming Sentry Issues by default.
6. opencode usage-limit observability uses opencode's **server-log tail** for the session as the evidence source when the JSON stream stalls (preferred over guessing usage-limit from a silent stall). Finding A is **resolved** (third-pass live log re-inspection, 2026-06-16 20:58): stdout stays empty through the internal-retry stall and the structured error reaches only the server log as the flat `error.error="<Wrapper>: <message>"` field under `AI_APICallError`/`AI_RetryError`; the matcher list is finalized and the server-log-tail path is required, not optional.
