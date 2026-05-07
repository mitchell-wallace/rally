## 1. Adapter capability methods

- [x] 1.1 Add `ResumeSupported() bool`, `RotateSupported() bool`, `LivenessProbeSupported() bool`, `CharsPerToken() float64` to the executor adapter interface
- [x] 1.2 Add `RotateModel(newModel string) error` and `ProbeLiveness(ctx) (bool, error)` to the executor adapter interface
- [x] 1.3 Default implementations on existing adapters return `false` / `0` / not-supported errors
- [x] 1.4 Add `SessionID string` field to `TryResult`
- [x] 1.5 Unit tests: each adapter returns expected defaults; interface compiles without changes to call sites that don't use the new methods

## 2. Per-harness adapter wiring (incremental)

- [x] 2.1 Claude adapter: declare `ResumeSupported() = true`, capture session-id from CLI output, pass `--resume <session-id>` flag on retry; declare `LivenessProbeSupported() = false` (interrupts on second prompt)
- [x] 2.2 Codex adapter: declare resume support per the harness's session model; declare `LivenessProbeSupported() = true` (tolerates parallel prompts)
- [x] 2.3 Opencode adapter: declare resume support; implement `RotateModel` for in-place model swap (the cheap-rotation target); declare probe support per current behaviour
- [x] 2.4 Gemini adapter: declare resume support per harness behaviour; probe declared as untested (start with `false`, revisit)
- [x] 2.5 Per-adapter unit tests with fixture sessions

## 3. Resume-aware retry path

- [x] 3.1 Update relay-runner's retry loop to capture `TryResult.SessionID` and stash it for the duration of the run
- [x] 3.2 On retry, if `ResumeSupported()` and a session-id exists, pass resume parameters to `Execute`
- [x] 3.3 Preserve `.rally/run-state.json` on resume retries; clear on fresh-start retries
- [x] 3.4 Unit tests: resume path preserves state, fresh-start path clears state, mid-handoff crash + resume preserves handoff flag, mid-handoff crash + fresh-start clears flag

## 4. Cheap-rotation path

- [x] 4.1 Update the v0.6.0 scheduler to expose `prev` and `current` on each `Next()` return value
- [x] 4.2 Relay-runner detects `prev.harness == current.harness` and calls `RotateModel(current.model)` on the existing adapter
- [x] 4.3 Adapters declaring `RotateSupported() = false` (or returning a non-nil error from `RotateModel`) trigger fall-back to teardown/respawn
- [x] 4.4 Unit tests: same-harness advance uses RotateModel, cross-harness advance tears down, RotateModel error falls back to teardown

## 5. Freeze detection

- [x] 5.1 Add `internal/reliability/freeze.go` consuming the v0.3.0 monitoring signals
- [x] 5.2 Detector trips when log mtime stale ≥ threshold AND zero conns (Linux only) AND IO unchanged
- [x] 5.3 macOS path: log mtime alone (conn/IO clauses treated as satisfied)
- [x] 5.4 Windows path: detector disabled
- [x] 5.5 Graceful-kill: SIGTERM → 5s drain → SIGKILL on the agent process group
- [x] 5.6 Emit `OnAgentFailed(entry, "freeze")` to the scheduler; route through resume-aware retry
- [x] 5.7 Unit tests: each platform path; threshold tunability via config; graceful-kill timing

## 6. Liveness probe

- [x] 6.1 Add `internal/reliability/probe.go` with the side-channel prompt logic
- [x] 6.2 Gated by `[reliability].liveness_probe = true` AND adapter `LivenessProbeSupported()`
- [x] 6.3 Bounded timeout per probe (e.g. 30s); failure or timeout confirms freeze
- [x] 6.4 Successful probe clears the freeze flag for that evaluation; try continues
- [x] 6.5 Probe heuristic for "ambiguous freeze" defined as a starting-point ("log mtime advancing but no IO progress for 60s"); tunable later
- [x] 6.6 Unit tests: probe disabled by config (never runs), probe enabled but adapter unsupported (silent skip), probe success/failure/timeout outcomes

## 7. Error classification

- [x] 7.1 Add `internal/reliability/patterns.go` with the documented pattern→strategy table
- [x] 7.2 Match against the last N lines of the try log post-failure (deterministic)
- [x] 7.3 Strategy dispatch: `rotate`, `resume + retry`, `wait + resume`, `no-op`, `fresh restart`
- [x] 7.4 For `wait + resume`, extract cooldown duration from error message when available; else use a default
- [x] 7.5 Unknown failure falls through to fresh restart (safe default)
- [x] 7.6 Integration tests: fixture log content for each pattern triggers the correct strategy

## 8. Config schema (`[reliability]`)

- [ ] 8.1 Extend the v0.5.0 schema with `[reliability]` table: `freeze_threshold_secs` (int, default 180), `liveness_probe` (bool, default false), `retry_budget` (int, default 5)
- [ ] 8.2 Add `[reliability].chars_per_token` map for per-harness divisor overrides (used by v0.3.0 token estimator)
- [ ] 8.3 Defaults applied when fields absent
- [ ] 8.4 Unit tests: each field default, each field overridable

## 9. Live-monitor extensions

- [ ] 9.1 Render `❄ frozen` (or similar) when freeze is flagged
- [ ] 9.2 Render `⚠ slowing` when ≥ 60% of threshold has elapsed without log activity (pending-freeze)
- [ ] 9.3 Render `↻ recovered` on the next tick after a freeze-driven resume succeeds; clear after one steady-state tick
- [ ] 9.4 Token estimator uses `[reliability].chars_per_token` override when set, falls back to adapter default
- [ ] 9.5 Snapshot tests for each indicator state

## 10. Documentation

- [ ] 10.1 Update README with the new `[reliability]` config section and example values
- [ ] 10.2 Document the error-pattern table as the single update point for new harness errors
- [ ] 10.3 v0.7.0 release notes: resume-aware retries, cheap rotation, freeze detection, opt-in liveness probe, error classification, retry budget bumped to 5
- [ ] 10.4 Document Windows freeze-detection limitation and the macOS log-mtime-only path

## 11. Verification

- [ ] 11.1 End-to-end: long opencode relay with GLM → Kimi advance — verify cheap rotation (no process teardown observed in logs)
- [ ] 11.2 End-to-end: claude rate-limit error mid-relay — verify wait + resume strategy (cooldown observed, session resumed, no fresh restart)
- [ ] 11.3 End-to-end: simulated freeze (process pause) — verify graceful-kill at threshold, retry via resume path, recovery indicator displayed
- [ ] 11.4 Liveness probe E2E (with codex): verify probe success clears freeze flag; probe failure confirms freeze
- [ ] 11.5 Each error pattern in the table produces its documented strategy in integration tests
- [ ] 11.6 Confirm `.rally/run-state.json` lifecycle: preserved on resume, cleared on fresh-start, cleared at relay-runner start of each run
- [ ] 11.7 Confirm Windows path: freeze detection disabled, retry-budget-exhaustion still works as the only failure-driven advance trigger
