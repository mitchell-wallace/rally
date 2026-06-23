## 1. Cut the `gemini` CLI harness (BREAKING, 0.12.0 blocker)

- [ ] 1.1 Delete `internal/agent/gemini.go` and remove `GeminiExecutor` references from `cmd/rally/main.go`
- [ ] 1.2 Remove `TestGeminiExecutor_*` and other gemini-executor tests from `internal/agent/agent_test.go`
- [ ] 1.3 Rename `reliability.ParseGeminiError` → `reliability.ParseAntigravityError` in `internal/reliability/antigravity.go` and its callers (`internal/agent/antigravity.go:106`); update `internal/reliability/antigravity_test.go` and `regression_test.go`
- [ ] 1.4 Remove the `gemini-cli exit 1` and `gemini auth or unsupported client` patterns from `ErrorPatterns` (`internal/reliability/patterns.go:224-248`); keep the antigravity-scoped eligibility pattern
- [ ] 1.5 Drop the `ge`/`gemini` alias entries from `internal/relay/route_runtime.go:715`, `internal/relay/mix.go:97`, `internal/relay/route_runtime_test.go`, and `internal/relay/runner_test.go`
- [ ] 1.6 Remove `GeminiModel` from `internal/config/config_v2.go` (defaults + harness sections + alias map + harness-canonical set) and from the config-loader tests in `internal/config/config_v2_test.go`
- [ ] 1.7 Remove `gemini` from the built-in harness list in `internal/config/providers.go:511` and from the `routes_check` harness allowlist in `internal/cli/routes_check.go:441`
- [ ] 1.8 Remove the `gemini_model` `huh.NewInput` prompt from `internal/cli/config.go:111` and its test fixtures
- [ ] 1.9 Remove the `Route: []string{"gemini"}` default in `cmd/rally/init_roles.go:65`
- [ ] 1.10 Scrub gemini fixtures from the routing/style/store tests: `internal/routing/{override_test,quota_scope_test,provider_test,parse_test}.go`, `internal/style/style_test.go`, `internal/store/store_test.go`, `internal/reliability/{patterns_test,category_test}.go`, `cmd/rally/main_test.go`
- [ ] 1.11 Delete the gemini real-backend smoke test block at `internal/relay/runner_real_backend_test.go:618-680`
- [ ] 1.12 Remove gemini from the README harness list, command examples, defaults tables, and the `internal/cli/routes_check.go` operator-facing output
- [ ] 1.13 Add a one-time resolution-failure warning when an operator route resolves to `ge`/`gemini`, naming the role + route entry + alias and recommending `antigravity` (see spec: cli-config "Removed gemini harness alias warning")
- [ ] 1.14 Verify `go build ./...`, `go vet ./...`, `go test ./...` all pass with the gemini surface removed

## 2. `ResolvedModel` on `TryResult` + runner-tag fix

- [ ] 2.1 Add `ResolvedModel string` field to `agent.TryResult` in `internal/agent/agent.go`
- [ ] 2.2 Set `ResolvedModel` inside `CodexExecutor.Execute` (`internal/agent/codex.go:170-173`) from the `model` local after the `opts.Model` fallback
- [ ] 2.3 Set `ResolvedModel` inside `ClaudeExecutor`, `OpenCodeExecutor`, and `AntigravityExecutor` from their respective resolved-model locals
- [ ] 2.4 Update the three `runner`-tag construction sites (`internal/relay/runner.go:2057, 2370, 2947`) to call `telemetry.RunnerLabel(picked.Harness, firstNonEmpty(result.ResolvedModel, picked.Model))` via a small helper
- [ ] 2.5 Tests: a route with a bare alias and an executor that populates `ResolvedModel` emits `runner = "<harness>:<resolved-model>"` (not the bare harness); a route with explicit model emits the same value as before; an empty `ResolvedModel` falls back to `picked.Model`

## 3. Populate `failure_evidence` on every classification path

- [ ] 3.1 Add an optional `Evidence *FailureEvidence` field to `StrategyDecision` in `internal/reliability/patterns.go`
- [ ] 3.2 In `ClassifyError` Priority 1 (executor evidence, `patterns.go:302`): pass through the existing evidence and set `Source = "executor_evidence"` if unset
- [ ] 3.3 In `ClassifyError` Priority 3 (dirty-tree incomplete, `patterns.go:346`): build an Evidence with `Category = CategoryIncompleteFinalization`, `Message = "agent exited without finalizing"`, `Source = "dirty_tree"`, and `RawSignal` populated from a bounded changed-paths list passed via `ClassifyContext`
- [ ] 3.4 Extend `ClassifyContext` with a `ChangedPaths []string` field; populate it from `filesChangedList` in the runner before calling `ClassifyError`
- [ ] 3.5 In `ClassifyError` Priority 4 (text patterns, `patterns.go:357`): re-scan `logLines` after a match to extract the matching line(s); set `Source = "text_pattern"`, `Message = pattern.Name`, `RawSignal` from the bounded matching tail
- [ ] 3.6 In `ClassifyError` Priority 5 (default agent_error, `patterns.go:387`): set `Source = "unmatched"`, `Message = "no recognised provider signal"`, `RawSignal` from a bounded log tail
- [ ] 3.7 At `runner.go:2479`, fall back to `decision.Evidence` when `result.Evidence` is nil, so `applyEvidenceToFailureState` is called for every classification path
- [ ] 3.8 Demote `applySafeExecErrorEvidence` (`runner.go:256`) to a last-resort fallback that fires only when both `result.Evidence` and `decision.Evidence` are nil
- [ ] 3.9 Tests: every Priority (1, 3, 4, 5) emits a `RallyFailure` whose `failure_evidence.source` is the expected value; `failure_evidence.raw_signal` is non-empty; `failure_evidence.message` matches the path's contract

## 4. Codex silent-exit enrichment (session-log fallback)

- [ ] 4.1 Reproduce the codex exit-1-with-no-output failure mode in a test fixture: confirm `runCodexCommand` can lose stderr written near process exit
- [ ] 4.2 Refactor `runCodexCommand` (`internal/agent/codex.go:223`) to use a dedicated `io.Pipe` + goroutine for stderr capture, independent of the stdout pipe's lifecycle
- [ ] 4.3 Add a codex session-log locator that scans `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl` (default `~/.codex/sessions/`) for a file whose first-line `session_meta.cwd` matches `opts.WorkspaceDir` and whose `session_meta.timestamp` is within the try window
- [ ] 4.4 Read only the first line (`session_meta`) for top-level scalars (`cwd`, `git.commit_hash`, `git.branch`, `model`, `cli_version`); explicitly skip `base_instructions`, `token_count`, and `response_item` payloads
- [ ] 4.5 Read the last `event_msg` line of any subtype; use its `payload.type` as the diagnostic message
- [ ] 4.6 In `CodexExecutor.Execute` (`internal/agent/codex.go:195-201`): when the in-band buffer has no parser-matchable signal and a session-log file matched, populate `FailureEvidence` with `Source = "codex_session_log"`, `Message` from the last event's subtype, and a 256-rune-bounded `RawSignal`
- [ ] 4.7 When no session-log file matches, populate `FailureEvidence` with `Source = "codex_no_session_log"`; runner classification (task 5) maps this to `harness_launch`
- [ ] 4.8 Tests: (a) codex writing late stderr after stdout closure → stderr still captured; (b) codex exit-1 with a matching session log → `Source = "codex_session_log"` and expected `Message`; (c) codex exit-1 with no matching session log → `Source = "codex_no_session_log"`; (d) `base_instructions` and `token_count` payloads never appear in `RawSignal`

## 5. Runner-side try-budget exhaustion classification

- [ ] 5.1 In the runner attempt loop (`runner.go:1849`-adjacent), when `loopOut.timedOut` is true and `result.Evidence` is nil and `decision.Evidence` is nil, override the category to `transient_infra` and set `failReason = "try budget exhausted; no output"`
- [ ] 5.2 In the same branch, override `failureClass` to `FailureAgent` so the freeze counter does not increment
- [ ] 5.3 Guard the override with a "real evidence produced" check so a timeout that DID produce evidence (e.g. codex session log) is not relabelled
- [ ] 5.4 Tests: (a) budget kill with no evidence → category `transient_infra`, fail reason `try budget exhausted; no output`, `FailureClass = agent`; (b) budget kill with executor evidence → existing path, no override; (c) freeze counter does not increment on a budget-kill try

## 6. OpenCode try-budget disk-log fallback

- [ ] 6.1 In `OpenCodeExecutor.Execute`, capture the opencode session id at startup from the `message=created id=… directory=<WorkspaceDir>` line in `$XDG_DATA_HOME/opencode/log/opencode.log` (default `~/.local/share/opencode/log/opencode.log`); keep the existing `providerID=<provider>` + try-window fallback per the existing opencode usage-limit requirement
- [ ] 6.2 When the try is killed by the runner-side budget without producing a parseable result, filter the server log by the session id and keep only `level=WARN` and `level=ERROR` lines plus the structural `message=created` / `message="loop session.id=…"` / `message=stream` markers, bounded to 16 lines max
- [ ] 6.3 Explicitly skip per-token, per-tool-call, and per-permission log lines (the verbosity hazard)
- [ ] 6.4 Populate `FailureEvidence` with `Source = "opencode_disk_log"`, `Message` from the last error line (or `"try budget exhausted; no parseable output"` when no error line is present), `RawSignal` from the bounded filtered tail
- [ ] 6.5 Tests: (a) budget-killed try with WARN/ERROR lines → `Source = "opencode_disk_log"`, `RawSignal` includes the error lines; (b) budget-killed try with only structural `loop`/`stream` lines → `Message = "try budget exhausted; no parseable output"`; (c) per-token log lines never appear in `RawSignal`; (d) the 256-rune bound holds

## 7. `RallyRoute` event + NULL-outcome cleanup

- [ ] 7.1 Add `EmitRouteEvent(ctx context.Context, fields map[string]interface{})` to the telemetry `Sink` interface (`internal/telemetry/sink.go`) and to `NoopSink`
- [ ] 7.2 Implement `NewRelicSink.EmitRouteEvent` (`internal/telemetry/newrelic.go`) to call `app.RecordCustomEvent("RallyRoute", buildFlatAttributes(fields))`
- [ ] 7.3 Add `RallyRoute` to the New Relic fixed event-name set in `internal/telemetry/attributes.go`
- [ ] 7.4 Move the route-fallback emission at `internal/relay/runner.go:794` from `EmitTryLog` to `EmitRouteEvent`; carry `event = "route_fallback"`, `from_runner`, `to_runner`, `role`, `lap_id`, `repo`, `repo_name`, `relay_id`, `run_id`, and fallback-cause fields
- [ ] 7.5 Move the recovery-cap-hit emission at `runner.go:804` (or its current location) from `EmitTryLog`/`CaptureFailure` route-pollution to `EmitRouteEvent`; keep the `needs_user` `RallyFailure` capture as a separate, parallel event
- [ ] 7.6 Assert at the telemetry boundary that every `RallyTry` emission carries a non-empty `outcome` field: add a guard in `NewRelicSink.EmitTryLog` that logs a warning and fills `outcome = "unknown"` if missing, so the failure mode cannot regress silently
- [ ] 7.7 Audit every `EmitTryLog` site (`runner.go:794, 2057, 2524, 2996`) and confirm each one sets `outcome` from a non-empty `TryOutcome`
- [ ] 7.8 Tests: (a) route-fallback produces a `RallyRoute` event (not `RallyTry`); (b) every `RallyTry` event has a non-empty `outcome`; (c) the telemetry boundary guard fires when a caller forgets `outcome`

## 8. Documentation and release notes

- [ ] 8.1 Update the README telemetry section to list `RallyRoute` alongside `RallyTry`, `RallyFailure`, and `RallyDiagnostic`
- [ ] 8.2 Update the README harness list to drop `gemini`; note `antigravity` as the only Google-owned harness
- [ ] 8.3 Draft a 0.12.0 release-notes entry covering: (a) BREAKING removal of `gemini`/`ge` aliases; (b) `runner` tag now includes the resolved model (NRQL alerts keyed on bare `runner = 'codex'` need widening to `LIKE 'codex%'`); (c) `RallyRoute` replaces NULL-outcome `RallyTry` entries (dashboards filtering `RallyTry WHERE outcome IS NOT NULL` will see the routing events disappear); (d) silent harness failures now carry structured `failure_evidence`

## 9. Regression coverage from observed New Relic data

- [ ] 9.1 A codex exit-1 with a matching session log produces `failure_evidence.source = "codex_session_log"` (regression: the 0.11.2 burst had `safe_exec_error`)
- [ ] 9.2 A codex exit-1 with no matching session log classifies as `harness_launch`, not `agent_error` (regression: the 0.11.2 burst burned 5 attempts per run)
- [ ] 9.3 An opencode try-budget-exhaustion try produces `failure_evidence.source = "opencode_disk_log"` (regression: the deepseek/minimax clusters had no evidence)
- [ ] 9.4 A bare-alias codex route produces `runner = "codex:<model>"` (regression: the 0.11.2 burst recorded `runner = "codex"`)
- [ ] 9.5 A route fallback produces a `RallyRoute` event with no `RallyTry` counterpart (regression: 39 NULL-outcome `RallyTry` events over 30 days)
- [ ] 9.6 A Priority-3 dirty-tree failure produces `failure_evidence.source = "dirty_tree"` (regression: today's dirty-tree failures have no evidence context)
- [ ] 9.7 A Priority-4 text-pattern failure produces `failure_evidence.source = "text_pattern"` with the matching line in `raw_signal` (regression: today's text-pattern failures have no evidence context)
