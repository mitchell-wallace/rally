## 1. Baseline

- [ ] 1.1 Confirm the working tree is green before refactoring: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go test -count=1 ./...`, `go run ./tools/archguard --ci` (exit 0). If red, STOP — do not fold unrelated fixes into this refactor.
- [ ] 1.2 Capture the current exported surface of `internal/agent` (types, funcs, consts) so the relocation can be checked for parity — the same identifiers must survive, only their package/type names change (`agent.ClaudeExecutor` → `claude.Executor` via `claude.New`, `agent.DefaultAntigravityModel` → `antigravity.DefaultModel`, etc.).

## 2. Phase 1 — `internal/harnessapi` contract + re-point consumers

- [ ] 2.1 Create `internal/harnessapi` with the contract: `Executor` (five-method interface, unchanged), `RunOptions`, `TryResult`, `ResolvedAgent` (unchanged field sets), and the bounded-final-text helper **exported** as `BoundedFinalText` (was unexported `boundedExecutorFinalText`; claude calls it across the new boundary). Keep only the current leaf imports (`internal/agent_prompt`, `internal/reliability`, `internal/textutil`).
- [ ] 2.2 Move the shared `BuildPrompt(RunOptions) string` (exported already) and the internal `isVerifyRole` into `internal/harnessapi` (`prompt.go`).
- [ ] 2.3 Move the reasoning-effort helpers into `internal/harnessapi` (`reasoning.go`), **exporting** the two adapters call across the boundary — `ApplyReasoningEffort` (was `applyReasoningEffort`) and `EmitReasoningWarning` (was `emitReasoningWarning`) — while keeping `IsKnownReasoningEffort` exported and `unknownEffortWarning`/`sortedEffortValues`/`knownReasoningEfforts` unexported.
- [ ] 2.4 Re-point every consumer of the contract from `internal/agent` to `internal/harnessapi` (`agent.` → `harnessapi.`): `internal/config`, `internal/routing`, `internal/relay`, `internal/relay/runner` (including `run_one.go`'s `BuildPrompt` call), `internal/app`, `internal/cli` (`routes_check.go`'s `IsKnownReasoningEffort`), and their tests. Confirm `go build ./...` after this step.

## 3. Phase 2 — `internal/harness/process` support

- [ ] 3.1 Create `internal/harness/process` with `SetProcessGroup` (exported) and `WriteTryLog` (exported), plus the three helpers **exported** for adapter use across the boundary: `RunLoggedCommand` (was `runLoggedCommand`), `OpenTryLog` (was `openTryLog`), and `TailString` (was `tailString`). Import only `internal/reliability` and the standard library. Keep it small — no parser helpers.
- [ ] 3.2 Re-point the still-in-`agent` adapters to `internal/harness/process`; confirm `go build ./...` and `go test -count=1 ./internal/agent`.

## 4. Phase 3 — move adapters into `internal/harness/<name>` (deep-module move)

- [ ] 4.1 Move `generic` → `internal/harness/generic`: expose `generic.New(...) harnessapi.Executor` over a concrete `generic.Executor`; move `generic_test.go`. Import only `harnessapi`, `harness/process`, `reliability`.
- [ ] 4.2 Move `fixture` → `internal/harness/fixture`: expose `fixture.New(...)`/`fixture.Executor`; update its test consumers (`internal/relay/runner/git_test.go`, `helpers_test.go`, and the relocated agent tests) to `fixture.New`.
- [ ] 4.3 Move `claude` → `internal/harness/claude` (with `claude_sessionlog_test.go`), `codex` → `internal/harness/codex` (with `codex_sessionlog.go` + `codex_sessionlog_test.go`), and `antigravity` → `internal/harness/antigravity` (with `antigravity_glog_test.go`). Relocate `DefaultAntigravityModel` to `antigravity.DefaultModel`; update `internal/cli/init_roles.go` and the real-backend test.
- [ ] 4.4 Move `opencode` → `internal/harness/opencode` last (largest), splitting `opencode.go` into responsibility-named files within the package (execution vs server-log evidence: `attachOpenCodeFailureEvidence` / `openCodeServerLogFailureEvidence` / `readOpenCodeServerLogTail` / `openCodeEvidenceFromServerLog`), so no single file exceeds the #3 production budget and the grandfather cap can be dropped.
- [ ] 4.5 After each move, run that adapter's package tests plus `go test -count=1 ./internal/relay/... ./cmd/rally`; keep per-harness parsing local (no cross-adapter helper leakage) and confirm each adapter package imports only `harnessapi`, `harness/process`, and `reliability`.
- [ ] 4.6 Carve the 2,812-line `internal/agent/agent_test.go` monolith as its subjects move: contract/`BuildPrompt`/reasoning cases → `internal/harnessapi` (joining the relocated `prompt_test.go` and `reasoning_test.go`); each executor's cases → its `internal/harness/<name>` package (joining the relocated `*_sessionlog_test.go` / `*_glog_test.go` / `generic_test.go`). No `agent_test.go` remains once `internal/agent` is removed. Keep each relocated `_test.go` under #3's 1,000-line cap, splitting by concern (e.g. `opencode_evidence_test.go` vs `opencode_exec_test.go`) rather than adding a grandfather entry; coordinate with #8.

## 5. Phase 4 — registry + app mapper

- [ ] 5.1 Add `internal/harness.BuildExecutors(Config) map[string]harnessapi.Executor` (package `harness`, `registry.go`) constructing the four built-in adapters by canonical name plus a `generic.New(...)` per `Config.Custom` entry. Define `harness.Config` (built-in model strings + `Custom map[string]GenericConfig`); do not import `internal/config`.
- [ ] 5.2 Rewrite `internal/app.BuildExecutors(cfg config.V2Config)` as a thin mapper: translate `config.V2Config` (built-in models + `cfg.Harnesses` command specs) into `harness.Config` and delegate. Preserve the exact executor set/keys the pre-change map produced.
- [ ] 5.3 Remove the now-empty `internal/agent` package (no shim). Confirm `go build ./...` and no remaining `internal/agent` import anywhere.
- [ ] 5.4 Unit-test the registry: four built-in canonical names present; a generic harness configured with a command registers a generic adapter; configured model defaults reach the right adapter; `app.BuildExecutors` yields the same set as before for a representative config.

## 6. Phase 5 — architecture guardrails

- [ ] 6.1 Update `tools/archguard` policy tables: add the new-package internal allow-lists (`harnessapi`; `harness/process`; each adapter; the `harness` registry) and swap `agent` → `harnessapi` in the `config`/`routing`/`relay`/`relay/runner`/`app` allow-lists, per `design.md` Decision 8. Give the adapter-confinement diagnostic an architectural reason.
- [ ] 6.2 Regenerate the grandfather map with `go run ./tools/archguard --report` against HEAD; confirm the `internal/agent/opencode.go` (801) entry is gone and no `internal/harness/**` file needs a new grandfather entry. `go run ./tools/archguard --ci` exits 0.

## 7. Phase 6 — durable guidance

- [ ] 7.1 Update the README architecture section: `internal/agent` → `internal/harnessapi` (contract) + `internal/harness/*` (adapters + registry), and update the import-chain description.
- [ ] 7.2 Update the `add-new-harness` skill so "add a harness" is "add an `internal/harness/<name>` module (with `New`/`Executor` + local parsing) and register it in `internal/harness.BuildExecutors`," and refresh any `internal/agent` references it and `test-driving-rally`/`phone-a-friend` carry.

## 8. Verification

- [ ] 8.1 `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .` empty, `go mod tidy` no diff (no dependency added/removed), `go run ./tools/archguard --ci` exit 0, `just check` green.
- [ ] 8.2 `go test -race -shuffle=on -count=1 ./internal/harness/... ./internal/relay/... ./internal/app` to catch relocation-induced races in the moved subprocess/log code.
- [ ] 8.3 Confirm behaviour preservation: no CLI flag, config-schema/semantic, telemetry-field/activation, prompt-content, store-shape, or agent-commit-message change; `internal/buildinfo/VERSION` untouched; no release implied.
