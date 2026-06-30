## 1. Baseline & checksums

- [x] 1.1 `go test -count=1 ./cmd/rally ./internal/cli ./internal/config ./internal/app ./internal/gitx` green. If red, capture failures and STOP — do not fold unrelated fixes into this refactor.
- [x] 1.2 Capture the exported-identifier set of `internal/config` (`go doc ./internal/config` or a grep of exported decls) to compare against the post-split package: the file split must change source files only, not the exported surface.
- [x] 1.3 Capture the current `internal/app` exported surface, a direct-import snapshot (`go list -f '{{.Imports}}' ./internal/app`), and a `go list -deps ./internal/app` snapshot. After the change, assert `internal/app` still does **not directly** import `internal/user_prompt` or `internal/laps`, and that `cmd/rally → internal/cli → internal/app → internal/relay/runner` has no cycle.
- [x] 1.4 Record the `cmd/rally` test/helper inventory: `(file, Test* name)` for `commitSetupFiles`, `chooseRelayAgentSpecs`, `syncRoleFolders`, telemetry-config, init, `tail`, hidden `init-roles`, and command-registration tests. Keep a migration checklist so every pre-change test appears exactly once after the moves.
- [x] 1.5 Confirm `.goreleaser.yaml` ldflags target `main.Version` / `main.DefaultNewRelicLicenseKey` / `main.DefaultNewRelicAppName`; these must remain valid after the change (build vars stay in `package main`).

## 2. Phase 1 — same-package splits (mechanical; no symbols change package)

- [x] 2.1 Split `cmd/rally/main.go` into responsibility-named `package main` files (e.g. `commands_relay.go`, `commands_init.go`, `commands_instructions.go`, `commands_update.go`, `templates.go`, plus the slim `main.go`). Verbatim moves; no logic change. `go test -count=1 ./cmd/rally` green.
- [x] 2.2 Split `internal/config/config_v2.go` into `types.go`, `load.go`, `decode.go`, `validate.go`, `resolve.go`, `save.go` per the design manifest (`providers.go` unchanged). Verbatim moves; keep `ReliabilityConfig` timeout methods beside their type. No config name/error/message edits. `go test -count=1 ./internal/config ./cmd/rally` green.
- [x] 2.3 Confirm the 1.2 exported-identifier set of `internal/config` is unchanged (file moves only) and `go build ./...` compiles.

## 3. Phase 2 — extract the presentation-neutral `internal/app` seam

- [x] 3.1 Break the `release → app` metadata edge before adding any `app → runner` import: move `BinaryName`, `ReleaseOwner`, `ReleaseRepo`, and `EnvNoUpdateCheck` from `internal/app` to `internal/release`, update main/CLI callers to use `release.*`, and confirm `internal/release` no longer imports `internal/app`. `go test -count=1 ./internal/release ./cmd/rally` green.
- [x] 3.2 Add `app.BuildExecutors(cfg config.V2Config) map[string]agent.Executor` (`internal/app/executors.go`): the built-in (`antigravity`/`claude`/`codex`/`opencode`) + generic-harness registry, lifted verbatim from `runRelay`. Point the relay command at it. `go test -count=1 ./internal/app ./cmd/rally` green.
- [x] 3.3 Relocate `commitSetupFiles` → `internal/gitx` as `gitx.CommitSetupFiles` (path-scoped `--no-verify` setup commit). Move its tests from `cmd/rally/main_test.go` into `internal/gitx`. Update both CLI-side call sites (init setup files, laps-hook install). `go test -count=1 ./internal/gitx ./cmd/rally` green.
- [x] 3.4 Add `app.InspectResume(workspaceDir) (app.ResumeInfo, error)` (`internal/app/relay_start.go`): peek at the most recent unfinished relay (`HasUnfinished`, `RelayID`, `CompletedIterations`, `TargetIterations`, `AgentMix`) using the same `store.NewStore(store.RallyDir(workspaceDir))` initialization/migration path as current startup. Treat “non-mutating” as no relay/status mutation beyond the existing layout migration, and unit-test with both a temp workspace + seeded current-layout store and a legacy top-level `.rally/relays.jsonl` layout.
- [x] 3.5 Add `app.RelayStartOptions`, `app.TelemetryBuild`, and `app.StartRelay(ctx, opts) error` (`internal/app/relay_start.go`). Move the lower-level runtime mechanics out of `runRelay` verbatim: rally-initialized check/store open, new-batch/discard handling from resolved booleans, mix-overwrite application, `BuildExecutors`, `telemetry.InitWithIdentity` (with `telemetryConfigForRelay` moved alongside, fed by `opts.Telemetry`), `BuildProviderIndex`, `runner.Config` assembly + resolver closure + override-route validation + instructions read, `runner.NewRunner`/`SetTelemetry`, double-Ctrl+C signal handling, `Runner.Run`. Keep telemetry result local to `StartRelay`: pass `telemetryResult.MachineID` into `runner.Config`, call `r.SetTelemetry(telemetryResult.Sink)`, and do not rely on main-package `activeTelemetry` / `activeMachineID` globals after extraction. **`internal/app` must not directly import `internal/user_prompt` or `internal/laps`.**
- [x] 3.6 Rewire the (still `package main`) relay command to: parse flags → resolve workspace → `config.LoadV2` + print deprecation notes → laps warning/detect → default iterations → `chooseRelayAgentSpecs` → `cli.ValidateRelayStartupRoutes` → initialized workspace check → `syncRoleFolders` → CLI-side `laps.InstallHooks` + `gitx.CommitSetupFiles` when laps is enabled → `app.InspectResume` + resume/mix `user_prompt` decisions → build `RelayStartOptions` → `app.StartRelay`. Preserve current resume semantics exactly: `--new` completes unfinished relay and resets agent status; interactive "Start new" completes the unfinished relay but does **not** reset agent status; non-interactive `--resume --agent ...` does **not** set `OverwriteMixOnResume` unless existing behavior is intentionally changed in a separate change.
- [x] 3.7 Add unit tests for `app.StartRelay` using a temp workspace and captured `Out`/`Err` writers (no stdin prompting reaches `app`). Keep existing `cmd/rally` relay tests green. `go test -count=1 ./internal/app ./cmd/rally` green; `go test -race -count=1 ./internal/app` green (signal-handling goroutine).
- [x] 3.8 Assert the 1.3 invariant: `go list -f '{{.Imports}}' ./internal/app` contains neither `internal/user_prompt` nor `internal/laps`; `go list -deps ./internal/app` is allowed to contain `internal/laps` transitively through `runner`, but must complete without an import cycle.

## 4. Phase 3 — promote command construction into `internal/cli`

- [x] 4.1 Add `cli.RootOptions` (carrying `Version` + `NewRelic{LicenseKey,AppName,HostDisplayName}` build vars) and `cli.NewRootCommand(opts) *cobra.Command`, registering all commands (`start`/`relay`, `init`, hidden `init-roles`, `instructions`, `routes`, `hooks`, `config`, `version`, `update`, `progress`, `tail`) and the `--version`/`-v` flag and the laps-aware dynamic help func. Thread `opts` build vars into the telemetry config passed to `app.StartRelay`.
- [x] 4.2 Move the relay command + handler into `internal/cli/start.go`; move `expandRelayFlag`/`chooseRelayAgentSpecs` into `internal/cli/relay_flags.go`; move `resolveWorkspaceDir` and the telemetry-config mapping into `internal/cli`. Move their tests verbatim. `go test -count=1 ./internal/cli ./cmd/rally` green.
- [x] 4.3 Move `init` + role bootstrap/sync (`init.go`, role bootstraps, `syncRoleFolders`, `migrateLegacyRoleFiles`) into `internal/cli`; move `instructions`, `version`, `update`, `tail`, and the hidden `init-roles` alias into `internal/cli`. Move `roles_sync_test.go` / relevant `main_test.go` / `update_test.go` / `relay_flags_test.go` / `tail_test.go` blocks with them. `go test -count=1 ./internal/cli ./cmd/rally` green.
- [x] 4.4 Reduce `cmd/rally/main.go` to: `Version` + `DefaultNewRelic*` build vars, `main()`, `startBackgroundUpdateCheck`, `cli.NewRootCommand(RootOptions{…}).Execute()`, and exit handling. Confirm the only remaining `package main` files are entry + the update-check + any tests that must stay (e.g. ldflag/version smoke). `go build ./...` compiles; `go test -count=1 ./...` green.

## 5. Phase 4 — config templates out of `main.go`

- [x] 5.1 Move `repoConfigTemplate`, `userConfigSeed`, and the `.rally/README.md` body string into `internal/cli/init_templates.go` beside the `init` command. Byte-for-byte identical template content (verified by an existing or added golden test on `rally init` output).

## 6. Phase 5 — docs & spec

- [x] 6.1 Update the README architecture section to the layered entry-point model: `cmd/rally` (entry/build vars) → `internal/cli` (commands + prompts + laps hook install) → `internal/app` (`StartRelay` seam) → `internal/relay/runner` (orchestrator) → `internal/relay` (primitives) → `internal/agent` (executors). State the `app` presentation-neutral / no-`user_prompt` / no-`laps` rule.
- [ ] 6.2 Confirm the `composition-root-structure` spec scenarios hold (layering, neutral seam, config split, slim main, no behaviour/telemetry/release change).

## 7. Verification

- [ ] 7.1 `go test -count=1 ./...` green.
- [ ] 7.2 `go test -race -shuffle=on -count=1 ./internal/app ./internal/cli` green.
- [ ] 7.3 `go build ./...` compiles; `go list -f '{{.Imports}}' ./internal/app` does not include `internal/user_prompt` or `internal/laps`; `go list -deps ./internal/app` completes without an import cycle.
- [ ] 7.4 Exported surface of `internal/config` unchanged vs the 1.2 baseline (source-file moves only).
- [ ] 7.5 Build/release unchanged: `.goreleaser.yaml` untouched; `go build -ldflags "-X main.Version=test -X main.DefaultNewRelicLicenseKey=k -X main.DefaultNewRelicAppName=n"` succeeds and `rally version` reflects the injected value.
- [ ] 7.6 Zero behaviour-surface change: no command-name/flag/help, config-schema/semantics, telemetry-field/activation, laps-hook, store-shape, or git-message edits; `internal/buildinfo/VERSION` untouched (no version bump).
- [ ] 7.7 `just check` if available.
