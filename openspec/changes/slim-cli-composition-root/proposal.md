## Why

`cmd/rally/main.go` is **864 lines** and imports most of the codebase. A single
`package main` file is simultaneously the Cobra root, command registration,
release/version wiring, config template storage, workspace resolution, relay flag
interpretation, user/repo config loading, route startup validation, executor
registry construction, laps hook installation, telemetry initialization, runner
config assembly, resume/new-batch prompting, signal handling, and the command
implementations for every non-relay flow. The whole relay lifecycle is inlined
into one Cobra `RunE` (`runRelay`, ~295 lines), so the only way to learn how a
relay actually boots is to read the command body in full.

`internal/config/config_v2.go` is **993 lines** and similarly fuses TOML decode,
layering, defaults, validation, deprecation warnings, alias resolution, model and
reasoning resolution, route validation, and save logic into one file. Some of
that belongs in one *package*; little of it belongs in one *file*.

This is change **#2** in the architecture sequence (`openspec/next-up.md`). It
builds directly on **#1 decompose-relay-runner**, which already extracted
`internal/relay/runner` (`runner.Config` / `runner.NewRunner` / `Runner.Run`) and
established the one-way `runner → relay` edge. Where #1 gave the *engine* its own
package, #2 gives the *entry points* progressive disclosure: a thin process root,
a CLI layer that owns Cobra and interactive prompting, and a reusable
`internal/app` seam that composes a `runner.Runner` above config. That seam is the
same start path `separate-runtime-presentation-boundary` (#5) needs so the CLI and
a future TUI can both start a relay without re-deriving the wiring.

The target mental model — the layer an agent reads to answer "where does X live" —
becomes:

- `cmd/rally`: process entry and build-time values,
- `internal/cli`: Cobra command construction and interactive prompts,
- `internal/app`: orchestration from resolved inputs to a running relay runner,
- `internal/relay/runner`: relay/run/try execution (the orchestrator, from #1),
- `internal/relay`: relay-record/resilience/mix primitives the runner builds on.

## What Changes

The change runs in mechanical-first phases so risk only rises after the
boring work is green. The package boundaries are knowable up front — `cmd/rally`
is the only consumer of these command builders and `internal/app` already exists
as a low-level helper package — so this change draws them directly instead of
deferring behind a same-package hedge (the same correction #1 made).

**Phase 1 — same-package splits (mechanical, lowest risk):**

- Split `cmd/rally/main.go` into responsibility-named `package main` files
  (entry, relay command, init/roles, instructions, update, templates, helpers).
  No symbol moves packages yet.
- Split `internal/config/config_v2.go` into same-`package config` files by
  responsibility (`types.go`, `load.go`, `decode.go`, `validate.go`, `resolve.go`,
  `save.go`; `providers.go` stays). No config name, error, or message changes
  except where a test proves a move forces one.

**Phase 2 — extract the presentation-neutral `internal/app` seam:**

- Add `app.BuildExecutors(cfg) map[string]agent.Executor` (section C): the
  built-in + generic executor registry as composition wiring feeding
  `runner.NewRunner`. (#4 `modularize-harness-adapters` later relocates this into
  a real harness registry; a small helper here is a low-risk win now.)
- Add `app.StartRelay(ctx, RelayStartOptions) error` and a read-only
  `app.InspectResume(workspaceDir) (ResumeInfo, error)`. `StartRelay` owns the
  runtime mechanics that turn **already-resolved** inputs into a running runner:
  store open, executor build, telemetry init, provider index, `runner.Config`
  assembly, override-route validation, instructions load, signal handling, and
  `Runner.Run`.
- **`internal/app` does not import `internal/user_prompt`.** All interactive
  start-of-run decisions — resume-vs-new, keep-vs-overwrite mix, and the existing
  interactive route validation — are resolved in `internal/cli` and passed in as
  concrete values. `InspectResume` gives the CLI the unfinished-relay summary it
  needs to prompt without opening the store itself. This keeps the seam reusable
  by the future TUI and avoids a `cli ↔ app` import cycle (the start path already
  calls the interactive `cli.ValidateRelayStartupRoutes`).
- **Laps hook install stays CLI-side before `app.StartRelay`.** `internal/laps`
  currently imports `internal/release`, and `internal/release` imports
  `internal/app`; moving `laps.InstallHooks` into `internal/app` would create an
  import cycle. The CLI already owns laps warning/detect and can preserve the
  current hook-install timing while passing only `LapsEnabled` into the app seam.
- **Build-time variables stay in `package main`.** GoReleaser injects
  `-X main.Version` / `-X main.DefaultNewRelicLicenseKey` /
  `-X main.DefaultNewRelicAppName`; those targets do not move. They thread
  `main → cli.NewRootCommand(RootOptions) → app.RelayStartOptions`, so release and
  telemetry-activation behaviour is byte-for-byte unchanged and `.goreleaser.yaml`
  is untouched.

**Phase 3 — promote command construction into `internal/cli` (section A):**

- Introduce `cli.NewRootCommand(opts RootOptions) *cobra.Command`, beside the
  config/route/hooks command builders that already live there
  (`NewConfigCmd`, `NewRoutesCmd`, `NewHooksCmd`).
- Move the remaining `RunE` handlers and their helpers out of `package main`:
  the `start` command (now flag-parse → resolve interactive decisions →
  `app.StartRelay`), `init` + role bootstrap/sync, `instructions`, `version`,
  `update`, relay-flag expansion, and workspace resolution. `commitSetupFiles`
  moves to `internal/gitx` (shared by `init` workspace setup and laps-hook
  install). Tests follow their symbols.
- `cmd/rally/main.go` shrinks to build-time variables, `main()`, the
  `cli.NewRootCommand(...).Execute()` call (with the existing background
  update-check + exit handling), and process exit.

**Phase 4 — move config templates out of `main.go`** (section D): the repo and
user config seed strings move beside the `init` command in `internal/cli`.

**Phase 5 — docs & spec:** update the README architecture section to the layered
entry-point model, and add the `composition-root-structure` capability spec.

This change is **structure-only**: no command name, flag, help text, config
schema/semantics, telemetry field or activation, release behaviour, laps hook
installation, persisted-store, or runtime relay-behaviour change. **No version
bump, no release.**

## Capabilities

### New Capabilities

- `composition-root-structure`: the layered composition root
  (`cmd/rally → internal/cli → internal/app`) and its dependency direction; the
  reusable, presentation-neutral relay-start seam (`app.StartRelay` /
  `app.InspectResume` / `app.BuildExecutors`) with interactive decisions resolved
  CLI-side; the same-package responsibility split of `internal/config`; the
  slim-`main.go` and behaviour/telemetry/release/laps-preservation contracts.

### Modified Capabilities

<!-- None. This change relocates and re-homes code and changes no runtime
     behaviour requirement, so no existing capability spec is modified. -->

## Impact

- **Code**: `cmd/rally/main.go` (864 → ~40 lines) split then drained into
  `internal/cli`; `internal/config/config_v2.go` (993 lines) split into
  same-package files; new `internal/app` relay-start seam
  (`relay_start.go`, `executors.go`) beside the existing `app.go`; `commitSetupFiles`
  relocated to `internal/gitx`; new `cli.NewRootCommand` + per-command files,
  including `tail` and the hidden `init-roles` compatibility alias.
- **Callers**: only `cmd/rally` imports `internal/cli`, and `internal/cli` (plus
  `internal/release`, already a consumer) imports `internal/app`. The new
  dependency chain is `cmd/rally → internal/cli → internal/app → internal/relay/runner
  → internal/relay`. No package gains an import of `internal/user_prompt` that did
  not have one — `internal/app` deliberately does not. `internal/app` also does
  not import `internal/laps`, avoiding the existing `laps → release → app` edge.
- **Public surface**: relocated, not redesigned. Config exported identifiers keep
  their names and signatures across the file split; the runner API from #1 is
  unchanged. New surface is additive (`cli.NewRootCommand`, `app.StartRelay`,
  `app.InspectResume`, `app.BuildExecutors`, `gitx.CommitSetupFiles`).
- **Build/release**: `main.Version` / `main.DefaultNewRelic*` stay in `package
  main`; `.goreleaser.yaml` ldflags unchanged; telemetry stays no-op until the
  relay path, with baked release credentials still reachable only there.
- **Sequencing**: consumes #1's `runner` package; produces the `app.StartRelay`
  seam #5 attaches the runtime/presentation boundary to; gives #3
  `add-architecture-guardrails` its first CLI/app import edges and a smaller tree
  to budget; leaves the executor registry as a small `app.BuildExecutors` helper
  for #4 `modularize-harness-adapters` to re-home.
- **Out of scope**: changing command names, flags, defaults, or help text (except
  test imports); TUI implementation; config schema or migration behaviour; the
  in-flight runtime/presentation boundary (#5); import-boundary CI (#3); the final
  harness-registry home for executors (#4).
