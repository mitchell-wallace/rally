## 1. Lap pin mismatch warning + handoff behavior

- [x] 1.1 Split mismatch outcomes in `internal/relay/runner.go`:
  - keep reason values (`wrong_lap_consumed`, `multi_lap_consumed`),
  - stop treating mismatches as issue-worthy operator failures,
  - still record `fail_reason`/mismatch metadata in run/try records without adding a `FailureCategory`.
- [x] 1.2 Keep mismatch as non-retry terminal run result (advance to next scheduler entry) and document warning path in inline logs and telemetry.
- [x] 1.3 Add/adjust run-level test(s) verifying no operator-worthy failure capture for mismatch-only runs, while run/try records remain complete.
- [x] 1.4 Add run-state preflight guard for already-complete pinned lap before/just after mismatch detection.
- [x] 1.5 Add telemetry assertions that mismatch events gain `event_kind=lap_pin_mismatch` and `mismatch_reason=wrong_lap_consumed|multi_lap_consumed` tags without `failure_category` or error-level issue capture.
- [x] 1.6 Add `telemetry.LevelWarning` support and map it through the active telemetry sink so mismatch diagnostics are warning-level events without becoming operator-worthy failures (`RallyDiagnostic level=warning` through New Relic after 0.9.1).

## 2. Cancelled state for operator-controlled exits

- [x] 2.1 Add concrete try outcome fields (the `TryOutcome` enum lives in `internal/reliability/outcome.go`; the new store field goes in `internal/store/records.go`):
  - extend the `reliability.TryOutcome` enum (`internal/reliability/outcome.go`) with `cancelled` and persist it via the existing `TryRecord.Outcome`,
  - `CancellationSource string json:"cancellation_source,omitempty"` with values `skip`, `graceful_stop`, `quit_now`,
  - compatibility behavior deriving `Completed=false` for cancelled records.
- [x] 2.2 Track explicit operator cancellation source in `internal/relay/runner.go`:
  - add a typed source to `actionLoopResult`,
  - set `skip` for Ctrl+S,
  - change Ctrl+X graceful stop to cancel/drain the current attempt, set `graceful_stop`, then stop the relay,
  - set `quit_now` for Ctrl+C.
- [x] 2.3 Make cancelled source override normal executor exit handling after the attempt drains:
  - no `failed: harness error`,
  - cancelled wins even if the subprocess exits cleanly after SIGINT,
  - no failure taxonomy classification,
  - no retry scheduling,
  - no infra/freeze/pause counter increments,
  - no operator-worthy failure capture.
- [x] 2.4 Preserve existing route semantics:
  - Ctrl+S still advances to the next scheduler candidate,
  - graceful stop still halts after the current cancellation/drain point,
  - quit-now still aborts the relay after recording the cancelled attempt.
- [x] 2.5 Update downstream consumers so cancelled records are not counted or displayed as failed:
  - `tallyRuns`,
  - final relay summary rendering,
  - recent-try context generation,
  - telemetry try log fields,
  - failure capture guards.
- [x] 2.6 Render cancelled outcome in muted/grey styling through the style layer, not red failure or green success.
- [x] 2.7 Make post-loop unfinalized-run handling cancellation-aware:
  - cancelled laps-backed attempts must not call the `incomplete_finalization` failure-capture path,
  - active run-state cleanup for cancellation must not erase lap/handoff/session fields needed by follow-up handling.
- [x] 2.8 Add tests:
  - Ctrl+S cancelled try is persisted as cancelled and not failed,
  - Ctrl+X graceful stop cancels/drains, records `graceful_stop`, and stops the relay,
  - quit-now cancellation overrides harness error handling and records cancelled,
  - cancelled attempts do not retry, freeze, pause, or emit failure telemetry,
  - cancelled laps-backed attempts do not emit `incomplete_finalization` telemetry,
  - cancelled attempts are not counted as failed runs in summaries/tallies,
  - style tests prove cancelled output uses muted formatting.

## 3. Run/role header semantics (`run:` labels)

- [x] 3.1 Update `internal/style/style.go`:
  - rename/format non-laps run counter to `run: X/Y`,
  - add role label support in header (`VERIFY` / task assignee),
  - include model on same-line role header path and preserve attempts.
- [x] 3.2 Update `internal/relay/runner.go` header call site to pass role/agent context.
- [x] 3.3 Update `internal/style/style_test.go` assertions from `[N/M]` to `run: N/M`.
- [x] 3.4 Add snapshot-style assertion that no-laps header includes role + model in one line.

## 4. Reasoning variant aliases and role resolution

- [x] 4.1 Extend config schema with `[reasoning] map[string]string` in `internal/config/config_v2.go` and `rawConfig`.
- [x] 4.2 Add resolver helper for role-level model alias fallback after route selection, when the task role and selected harness are both known (lane-aware, preserving explicit model tokens).
- [x] 4.3 Extend resolved route/runtime data to carry `{model, reasoning_effort}` where applicable, while preserving model-alias-only behavior:
  - `agent.ResolvedAgent`,
  - `agent.RunOptions`,
  - `relay.Config.Resolver` call sites,
  - `routeSelection`.
- [x] 4.4 Wire harness-specific effort injection in executor argument builders and tests:
  - codex: pass `-c model_reasoning_effort=<value>`,
  - claude: pass `--effort <value>`,
  - opencode: pass `--variant <value>`,
  - gemini: warn and skip,
  - antigravity: resolve effort via model aliases/model names only.
- [x] 4.5 Add validation and tests:
  - missing harness-scoped model alias -> clear `rally routes check` error,
  - route explicit model still wins,
  - role alias resolves to existing harness model alias,
  - unknown unsupported effort value warns rather than hard-fails,
  - unsupported harness effort warns and skips injection.
- [x] 4.6 Update `.rally/config.toml` sample and `README.md` routing/model sections with role-variant examples (`verify = "g55-xh"`, `junior = "g55-l"`) and harness-specific effort notes.

## 5. Syntax highlighting for `rally tail`

- [x] 5.1 Add `--highlight` flag to `cmd/rally/tail.go` (`off|heuristic|chroma`, default `off`).
- [x] 5.2 Add lexer/highlighter pass in output loop for `heuristic` mode (no new dependency).
- [x] 5.3 Add optional rich mode behind dependency and gate with `--highlight=chroma`.
- [x] 5.4 Preserve deterministic behavior under plain mode.

## 6. `rally tail` active try targeting

- [x] 6.1 Extend progress run-state with active try metadata (`active_relay_id`, `active_run_id`, `active_try_id`, `active_log_path`, `active_started_at`).
- [x] 6.2 Write active try metadata at try start before the executor runs, and clear only the active metadata fields after the try is appended to the store:
  - add a helper that preserves `RunID`, `PinnedLapID`, `RecordedLaps`, `LapsAttempted`, `HandoffState`, and `SessionID`,
  - do not call `progress.ClearRunState` for active-tail cleanup.
- [x] 6.3 In `tailTarget`:
  - if `--try 0` and active run metadata exists, use its active try log,
  - if no active metadata, fall back to newest completed try,
  - retain explicit historical `--try N` semantics.
- [x] 6.4 Add fallback checks for stale/missing active files (warn, then use newest completed try when available).
- [x] 6.5 Add targeted tests:
  - fresh workspace with active metadata tails the active log instead of erroring,
  - active metadata beats an older completed try,
  - stale/missing active path falls back with a warning,
  - stale active metadata from a crashed/finished relay is ignored,
  - historical `--try N` remains 1-based and unchanged.

## 7. Research and telemetry closure

- [x] 7.1 Ensure short rate-limit remains non-error (`info`) and retains existing tags (`event_kind=limit_signal`, `failure_category=short_rate_limit`) without reclassifying as crash/failure event.
- [x] 7.2 Add/adjust release notes with exact historical telemetry incident IDs from the legacy incident set (`RALLY-2`, `RALLY-3`, `RALLY-4`, `RALLY-6`, `RALLY-8`, `RALLY-9`, `RALLY-B`, `RALLY-C`) while describing 0.10.0 behavior in backend-neutral/New Relic terms.
- [x] 7.3 Add release checklist verification: no alert regression for routine rate-limit categories, corrected run header text in default relay output, cancelled output is muted, and `rally tail` defaults to active output rather than old completed relays.

## 8. opencode usage-limit detection and reset parsing

- [x] 8.1 Broaden `reliability.ParseOpencodeError` (`internal/reliability/opencode.go`) to classify subscription-provider usage limits as `usage_limit`:
  - match across opencode's `AI_APICallError` / `AI_RetryError` / `UnknownError` wrappers, checking `error.name`, `error.data.message`, **and the flat server-log `error.error="<Wrapper>: <message>"` value** (confirmed third-pass: the structured error reaches only the server log as this flat field, never a nested `data.message` on stdout),
  - add substrings `usage limit reached`, `monthly usage limit`, `usage limit reached for` (matcher list finalized — see 8.5),
  - keep `usage_limit` winning over `short_rate_limit` when both could match (preserve the existing priority test).
- [x] 8.2 Add opencode-specific reset parsing (do not overload the gemini regex):
  - space-separated spans: `Resets in 7 days`, `… 5 hour(s)`, `… 30 minutes` (the gemini `(\S+)` grabs only the number; capture `(\d+)\s+(day|hour|minute|second)s?`),
  - absolute timestamps: `reset at 2026-06-16 18:29:51`, `will reset at …` — **no timezone marker; parse in `time.Local`** (layout `2006-01-02 15:04:05`) and treat as approximate (bench slightly long is the safe direction),
  - feed `FailureEvidence.ResetAfter` / `ResetAt` so `benchResetDeadline` uses the real reset instead of `BenchDefaultDuration`.
- [x] 8.3 Make the limit observable despite opencode's internal retry (finding A, confirmed third-pass):
  - when an opencode try stalls or errors without a usable result, have `OpenCodeExecutor` (`internal/agent/opencode.go`) read the tail of opencode's server log (`~/.local/share/opencode/log/opencode.log` — only this file carries `message="stream error"` lines; the per-run timestamped `*.log` files do not) and surface any usage-limit signature as `FailureEvidence`,
  - correlate the session **without relying on stdout** (which is empty during the stall): match `message=created … directory=<WorkspaceDir>` to recover the `session.id=`, then scan that session's `level=ERROR message="stream error"` lines; fall back to `providerID=<provider>` (the prefix of the `--model` arg) within the try's wall-clock window, and narrow by `--session` id when already known (resumed runs),
  - match either `agent=build` (small=false) or `agent=title` (small=true) lines — both carry the limit text,
  - prefer this over inferring usage-limit from a silent stall.
- [x] 8.4 Add tests:
  - opencode fixtures for the exact zai (`Usage limit reached for 5 hour. Your limit will reset at <ts>`) and opencode-go (`Monthly usage limit reached. Resets in 7 days.`) signatures in the flat `error.error="<Wrapper>: …"` server-log form, both `AI_APICallError` and `AI_RetryError` wrappers, asserting `CategoryUsageLimit` + correct `ResetAfter`/`ResetAt`,
  - the `UnknownError` generic-message + server-log-tail evidence path classifies `usage_limit`,
  - server-log-tail session correlation by `directory=<WorkspaceDir>` (and provider+window fallback) picks the right session's limit line,
  - a quota-scope bench is triggered for `opencode:zai-coding-plan` / `opencode:opencode-go` on these failures (not `agent_error`).
- [x] 8.5 Confirm the precise emitted error shape for these limits before finalizing the 8.1 matcher, per spike-2 finding A. **Resolved (third-pass live log re-inspection, 2026-06-16 20:58):** stdout stays empty through opencode's internal-retry stall; the structured provider error reaches only the server log as the flat `error.error="<Wrapper>: <message>"` field under `AI_APICallError` / `AI_RetryError` (no native `UsageLimitError`/`QuotaExceededError`). The 8.1 matcher list is finalized and the 8.3 server-log-tail path is required, not optional. See `spike-2-report.md` §"Third-pass confirmation".
- [x] 8.6 Update release notes / historical incident list with the opencode usage-limit issues (`RALLY-Q`, `RALLY-K`, `RALLY-D`) while using backend-neutral/New Relic telemetry terminology for the implemented behavior.

## 9. Release versioning

- [x] 9.1 Bump `internal/buildinfo/VERSION` from the current `0.9.x` (`0.9.3` at planning time) to `0.10.0` as part of the release change.
- [x] 9.2 Confirm `main.Version` remains `"dev"` and leave tag creation to the existing `auto-tag` workflow.

## 10. 0.10.1 New Relic telemetry follow-up

- [x] 10.1 Document the New Relic-derived telemetry follow-up plan in `telemetry-followup-plan.md` and commit it before implementation.
- [ ] 10.2 Enrich `route_fallback` events with trigger try/run, outcome, fail reason, failure class/category, lap id, route name, and route-entry exhausted reason.
- [ ] 10.3 Attach bounded non-limit failure evidence for generic `agent_error` / launcher / parser failures without changing retry or freeze semantics.
- [ ] 10.4 Extend lap-pin mismatch diagnostics with expected lap id, consumed lap count, and consumed lap ids; lap ids are safe to emit.
- [ ] 10.5 Add timeout and handoff-timeout telemetry context: timeout kind/budget, session capture, resume support, handoff-only attempt state, blocker reason, and last-output age where available.
- [ ] 10.6 Prefer compact provider error evidence over transcript-shaped raw fallback text while keeping bounded scrubbed raw evidence for parser normalization.
- [ ] 10.7 Add focused telemetry tests for fallback trigger fields, non-limit evidence, lap ids, timeout context, and provider evidence shaping.
- [ ] 10.8 Bump `internal/buildinfo/VERSION` to `0.10.1`, validate, run local gates, and release through the standard workflow.
