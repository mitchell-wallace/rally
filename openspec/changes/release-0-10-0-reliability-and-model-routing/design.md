# Design: 0.10.0 Reliability, Routing Semantics, and CLI Visibility

## 1) Lap pin mismatch as warning + handoff-only for this release

### Current behavior

- `validatePinnedLap` sets `lapPinMismatch=true` and marks the attempt as failed.
- this feeds `issueWorthy` and can emit Sentry/failure pathways.
- `runOne` currently exits with a terminal failure path and telemetry tags indicate an error-class outcome.

### 1.1 target behavior

- Keep mismatch reason capture (`wrong_lap_consumed`, `multi_lap_consumed`) for diagnostics.
- Route outcome should be warning only:
  - remove lap mismatch from issue-worthy capture for now
  - keep as non-terminal mismatch signal for scheduler handoff and operator visibility
  - preserve retry budget behavior (no extra in-run retries)

### 1.2 implementation steps

1. In `runOne`, split `lapPinMismatch` handling from failure taxonomy:
   - do not force operator escalation on mismatch.
   - keep fail/failure record (`FailReason` includes mismatch reason) for audit.
   - keep `FailureClass` as a non-`FailureInfra` value to avoid Sentry alerting.
2. In mismatch branch, allow normal route fallback as the “next action” by leaving scheduler state as failed but not escalating as infra/agent failure.
3. Add optional post-run state preflight before next cycle:
   - if pinned lap already completed elsewhere, mark the current run as complete and route next.
   - if a different single lap appears active/recorded incorrectly, keep warning path but still advance route.
4. Update telemetry tags/comments:
   - include `mismatch=wrong_lap_consumed|multi_lap_consumed` tags.
   - include warning text in run log and run summary.

### 1.3 docs and tests

- Add tests for warning-only behavior and unchanged retry behavior.
- Regression test should keep previous short/no-op mismatch coverage and only remove escalation condition.

## 2) Run indexing wording and role-aware headers

### Current behavior

- `style.RenderHeader` uses `[run/total]` without explicit labeling for non-laps and shows model line separately.

### 2.1 target behavior

- Non-laps header renders something equivalent to: `run: 2/18`.
- Header should prefer role and runner context:
  - `VERIFY: codex - g55-xh - started 16:40`
  - then include attempts inline where useful (`attempt N`).

### 2.2 implementation steps

1. Add `RoleLabel` to `style.HeaderOptions` and include it in render.
2. Change non-laps counter formatting from bare `[N/M]` to `run: N/M`.
3. Ensure tests in `internal/style/style_test.go` are updated from bracket token assertions to `run:` assertions.
4. Update run context in `runner` header call to pass role when available (`task.Assignee`).
5. Keep lap header behavior as-is for lap-backed paths (title + `laps: X/Y`) to avoid regressing laps semantics.

## 3) Cancelled state for operator-controlled exits

### Current behavior

- Ctrl+S skip cancels the current attempt and currently reaches normal executor error handling, so the try can render as `failed: harness error`.
- Quit-now and any graceful-stop path that cancels/drains a running attempt can similarly inherit process/context cancellation as a harness failure.
- Failed styling and failure telemetry are misleading for these paths because the operator, not the harness, chose the outcome.

### 3.1 target behavior

- Add a first-class cancelled outcome for attempts/runs:
  - source values: `skip`, `graceful_stop`, `quit_now`
  - display label: `cancelled`
  - styling: muted/grey via the existing style layer
- Cancellation source overrides normal executor exit handling after the attempt drains:
  - do not classify as `harness error`
  - do not run failure taxonomy or retry classification
  - do not increment retry, infra, pause, or freeze counters
  - do not emit Sentry failure captures
- Persist cancellation status and source in try/run records so summaries, tail context, and future resume logic do not need to infer it from text.

### 3.2 implementation steps

1. Track explicit operator action on the runner when Ctrl+S, Ctrl+X graceful-stop cancellation, or Ctrl+C quit-now cancels the active attempt.
2. After draining `tryCh`, derive attempt outcome in this order:
   - successful completion/finalization
   - explicit operator cancellation
   - lap-pin mismatch warning
   - ordinary executor/failure classification
3. Extend store/result structures with an outcome/status field if the existing `Completed` boolean is insufficient; keep compatibility by deriving `Completed=false` for cancelled records while exposing `status=cancelled`.
4. Add muted footer/header rendering for cancelled outcomes and ensure summaries print `cancelled` rather than `failed`.
5. Keep skip route semantics unchanged: Ctrl+S advances to the next scheduler candidate, but the skipped try is recorded as cancelled.

### 3.3 docs and tests

- Add unit tests covering Ctrl+S, graceful stop cancellation, and quit-now cancellation overriding a context/process error.
- Add telemetry/store tests verifying cancelled attempts do not produce failure captures, freeze increments, or retry attempts.
- Add style tests verifying cancelled output uses muted/grey formatting and not success/failure colors.

## 4) Reasoning/variant support with aliases

### Design goal

Keep existing `[routes]` parsing and harness model aliases, and layer role-level variant defaults plus harness-specific effort injection where the CLI supports it.

### 4.1 configuration

Add top-level config table:

```toml
[reasoning]
verify = 'g55-xh'
junior = 'g55-l'
```

- Values are role keys (case-insensitive).
- Values are model alias tokens, resolved through the same model alias path as route `harness:alias` values. This covers antigravity, where effort is encoded in the model name.
- Existing `[defaults.*_model]` and `[harness.<name>.models]` continue unchanged.
- Where a role maps to an effort value for the same model, executor-specific injection is:
  - codex: `-c model_reasoning_effort=<value>`
  - claude: `--effort <value>`
  - opencode: `--variant <value>`
  - gemini: unsupported; warn and skip
  - antigravity: unsupported as a flag; use model aliases such as high/low model names
- Unknown reasoning values should warn, not hard-fail. The spike confirmed claude/opencode ignore many unsupported values and codex ultimately rejects invalid values at API time.

### 4.2 runtime resolution

1. On route entry resolution for a named lane, apply role-level variant fallback only when the entry has no explicit model token.
2. Preserve explicit route models with higher precedence.
3. Resolve role fallback to named model alias before accepting raw model strings.
4. Allow the resolver to return both the selected model and optional reasoning/variant effort so each executor can inject it using its own mechanism.
5. Add clear errors for unresolved role alias and include role name in diagnostics.

### 4.3 compatibility

- Support existing built-in aliases (`cc`, `cx`, `ge`, etc.) unchanged.
- Existing `--agent` override behavior remains unchanged unless a role context can be inferred from route mode.

## 5) Tail reliability and live behavior

### Current behavior

- `rally tail --try 0` selects `AllTries()[last]`, which is stale while a run is in-flight.
- On a fresh workspace with only an in-flight try, `rally tail` errors with `no tries recorded in this workspace` because the active try is not appended to the store until the executor returns.
- The active log file exists and is written incrementally, but `run-state.json` currently has only run/lap bookkeeping and no active try/log pointer.

### 5.1 target behavior

1. Keep explicit `--try N` behavior.
2. For `--try 0`, pick the current active try/log when available.
   - prefer run-state metadata indicating an active run/session and its latest try path, when present.
   - fallback to newest try only when no active run is present.
3. If active target points at missing path, fall back to newest completed try with warning output.

### 5.2 syntax highlighting options

- Add optional `--highlight` mode on tail:
  - `off` (default): existing plain copy
  - `heuristic`: token-aware coloring for errors, JSON-like lines, timestamps, commands, URLs
  - `chroma` (optional): rich coloring behind dependency path when enabled and available

### 5.3 persistence/integration

- Extend progress/run-state with `active_try_id` and `active_log_path`, written at try start before the executor runs and cleared after the try is recorded.
- Add tests around active-run tail selection when both active and completed streams exist.

## 6) Rollout plan and review

This release is intentionally staged:

1. Core mismatch warning behavior and run/header updates first.
2. Cancelled state plumbing and muted output for operator-controlled exits.
3. Reasoning alias/config path with compatibility checks and executor-specific injection.
4. `tail` active-run detection and highlighting modes.

### Items that require operator confirmation

1. Whether role-variant resolution should be hard-fail when alias missing, or fall back to route default silently.
2. Whether `run` header should include model on the same line or remain as a second `model:` line.
3. Whether richer syntax highlighting should be opt-in only in a v1 release or behind a `--highlight=auto` default.
