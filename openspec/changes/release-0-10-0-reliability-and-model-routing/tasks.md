## 1. Lap pin mismatch warning + handoff behavior

- [ ] 1.1 Split mismatch outcomes in `internal/relay/runner.go`:
  - keep reason values (`wrong_lap_consumed`, `multi_lap_consumed`),
  - stop treating mismatches as issue-worthy operator failures,
  - still record `fail_reason`/`category` metadata in run/try records.
- [ ] 1.2 Keep mismatch as non-retry terminal run result (advance to next scheduler entry) and document warning path in inline logs and telemetry.
- [ ] 1.3 Add/adjust run-level test(s) verifying no Sentry issue capture for mismatch-only runs, while run/try records remain complete.
- [ ] 1.4 Add run-state preflight guard for already-complete pinned lap before/just after mismatch detection.
- [ ] 1.5 Add telemetry assertions that mismatch events gain `event_kind=lap_pin_mismatch` and `failure_category=lap_pin_mismatch` tags without error-level issue capture.

## 2. Cancelled state for operator-controlled exits

- [ ] 2.1 Add a first-class cancelled outcome/status in the runner/store path:
  - status value `cancelled`,
  - source values `skip`, `graceful_stop`, `quit_now`,
  - compatibility behavior for existing `Completed`/success booleans.
- [ ] 2.2 Track explicit operator cancellation source when Ctrl+S skip, graceful stop cancellation/drain, or quit-now cancels an active attempt.
- [ ] 2.3 Make cancelled source override normal executor exit handling after the attempt drains:
  - no `failed: harness error`,
  - no failure taxonomy classification,
  - no retry scheduling,
  - no infra/freeze/pause counter increments,
  - no Sentry failure capture.
- [ ] 2.4 Preserve existing route semantics:
  - Ctrl+S still advances to the next scheduler candidate,
  - graceful stop still halts after the current cancellation/drain point,
  - quit-now still aborts the relay after recording the cancelled attempt.
- [ ] 2.5 Render cancelled outcome in muted/grey styling through the style layer, not red failure or green success.
- [ ] 2.6 Add tests:
  - Ctrl+S cancelled try is persisted as cancelled and not failed,
  - graceful stop cancellation overrides context/process cancellation and records cancelled,
  - quit-now cancellation overrides harness error handling and records cancelled,
  - cancelled attempts do not retry, freeze, pause, or emit failure telemetry,
  - style tests prove cancelled output uses muted formatting.

## 3. Run/role header semantics (`run:` labels)

- [ ] 3.1 Update `internal/style/style.go`:
  - rename/format non-laps run counter to `run: X/Y`,
  - add role label support in header (`VERIFY` / task assignee),
  - include model on same-line role header path and preserve attempts.
- [ ] 3.2 Update `internal/relay/runner.go` header call site to pass role/agent context.
- [ ] 3.3 Update `internal/style/style_test.go` assertions from `[N/M]` to `run: N/M`.
- [ ] 3.4 Add snapshot-style assertion that no-laps header includes role + model in one line.

## 4. Reasoning variant aliases and role resolution

- [ ] 4.1 Extend config schema with `[reasoning] map[string]string` in `internal/config/config_v2.go` and `rawConfig`.
- [ ] 4.2 Add resolver helper for role-level model alias fallback in route resolution (lane-aware, preserving explicit model tokens).
- [ ] 4.3 Extend resolved route/runtime data to carry `{model, reasoning_effort}` where applicable, while preserving model-alias-only behavior.
- [ ] 4.4 Wire harness-specific effort injection:
  - codex: pass `-c model_reasoning_effort=<value>`,
  - claude: pass `--effort <value>`,
  - opencode: pass `--variant <value>`,
  - gemini: warn and skip,
  - antigravity: resolve effort via model aliases/model names only.
- [ ] 4.5 Add validation and tests:
  - alias missing -> clear error,
  - route explicit model still wins,
  - role alias resolves to existing harness model alias,
  - unknown role alias/value warns rather than hard-fails,
  - unsupported harness effort warns and skips injection.
- [ ] 4.6 Update `.rally/config.toml` sample and `README.md` routing/model sections with role-variant examples (`verify = "g55-xh"`, `junior = "g55-l"`) and harness-specific effort notes.

## 5. Syntax highlighting for `rally tail`

- [ ] 5.1 Add `--highlight` flag to `cmd/rally/tail.go` (`off|heuristic|chroma`, default `off`).
- [ ] 5.2 Add lexer/highlighter pass in output loop for `heuristic` mode (no new dependency).
- [ ] 5.3 Add optional rich mode behind dependency and gate with `--highlight=chroma`.
- [ ] 5.4 Preserve deterministic behavior under plain mode.

## 6. `rally tail` active try targeting

- [ ] 6.1 Extend progress run-state with active try metadata (`active_try_id`, `active_log_path`, and run ID where needed).
- [ ] 6.2 Write active try metadata at try start before the executor runs, and clear it after the try is appended to the store.
- [ ] 6.3 In `tailTarget`:
  - if `--try 0` and active run metadata exists, use its active try log,
  - if no active metadata, fall back to newest completed try,
  - retain explicit historical `--try N` semantics.
- [ ] 6.4 Add fallback checks for stale/missing active files (warn, then use newest completed try when available).
- [ ] 6.5 Add targeted tests:
  - fresh workspace with active metadata tails the active log instead of erroring,
  - active metadata beats an older completed try,
  - stale/missing active path falls back with a warning,
  - historical `--try N` remains 1-based and unchanged.

## 7. Research and telemetry closure

- [ ] 7.1 Ensure short rate-limit remains non-error (`info`) and retains existing tags (`event_kind=limit_signal`, `failure_category=short_rate_limit`) without reclassifying as crash/failure event.
- [ ] 7.2 Add/adjust release notes with exact Sentry IDs from the current incident set (`RALLY-2`, `RALLY-3`, `RALLY-4`, `RALLY-6`, `RALLY-8`, `RALLY-9`, `RALLY-B`, `RALLY-C`).
- [ ] 7.3 Add release checklist verification: no alert regression for routine rate-limit categories, corrected run header text in default relay output, cancelled output is muted, and `rally tail` defaults to active output rather than old completed relays.
