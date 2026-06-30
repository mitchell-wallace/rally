## Context

`cmd/rally/main.go` (864 lines) and `internal/config/config_v2.go` (993 lines)
are the two remaining catch-all files in the tree after #1 extracted the runner.
`main.go` is a `package main` that imports `agent`, `app`, `cli`, `config`,
`gitx`, `laps`, `progress`, `relay`, `relay/runner`, `release`, `routing`,
`store`, `telemetry`, and `user_prompt` — it is the composition root, and it has
accreted not just *wiring* but the *implementation* of every command. The relay
lifecycle is one ~295-line `RunE` (`runRelay`).

This is change #2 in the sequence. #1 proved the discipline this change reuses:
when the dependency graph already shows the boundary, draw it now rather than
hedging behind a same-package split "until boundaries are obvious." Here the graph
is unambiguous — `cmd/rally` is the sole importer of `internal/cli`, and
`internal/app` already exists (today a leaf of path/env helpers consumed by
`internal/release`). The only judgement call is *how the interactive seam is
drawn*, which the rest of this document settles.

### What "progressive disclosure" means here

An agent answering "where does X live?" should be able to stop at the first layer
that owns X, without reading the layer beneath:

| Question | Layer to read |
| --- | --- |
| process entry, build-time values | `cmd/rally/main.go` (~40 lines) |
| what commands/flags exist; what a prompt asks | `internal/cli` |
| how a relay boots from resolved inputs | `internal/app` (`StartRelay`) |
| how relay/run/try actually execute | `internal/relay/runner` (#1) |
| relay-record / resilience / mix primitives | `internal/relay` (#1) |

The dependency direction is strictly downward:
`cmd/rally → internal/cli → internal/app → internal/relay/runner → internal/relay`.
Because `runner` already imports `internal/laps`, and `internal/laps` imports
`internal/release`, this change first removes the current `release → app` metadata
edge so adding `app → runner` does not create `app → runner → laps → release → app`.

## Goals / Non-Goals

**Goals**

- Reduce `cmd/rally/main.go` to build vars + `main()` + root construction + exit.
- Give relay startup a reusable, presentation-neutral home (`app.StartRelay`)
  that both the CLI and a future TUI can call.
- Split `internal/config/config_v2.go` into responsibility-named files in the
  same package.
- Keep it boring Go: no framework, no dependency-injection container, no new
  abstraction the call sites do not already need.

**Non-Goals**

- No behaviour, CLI-output, config-schema, telemetry, release, or laps change.
- No in-flight runtime/presentation boundary (runtime events, operator control
  during a run) — that is #5, attached to the `terminal.go` / `action_loop.go` /
  `liveness.go` seams #1 isolated.
- No final harness-registry home for executors — that is #4. `BuildExecutors`
  here is deliberately a thin helper.
- No import-boundary CI — that is #3.
- No version bump, no release.

## The interactive seam (the one real decision)

The draft sketched `app.StartRelay(ctx, RelayStartOptions{In, Out, Err, Resume,
NewBatch, …})` — the app layer owning the relay lifecycle *including* the
interactive resume/mix prompts via injected IO. Two facts make a stricter
boundary the better shape:

1. **Import cycle.** The start path already calls the interactive
   `cli.ValidateRelayStartupRoutes` (it prompts on stdin and returns validated
   routes). If `app.StartRelay` owned the full lifecycle it would need to call
   that function → `app` imports `cli`, while `cli` imports `app` for
   `NewRootCommand`'s handler → cycle. Breaking it would mean relocating the
   interactive route validation out of `cli` — churn that fights the layering.
2. **Reuse.** `internal/app` is a *low* layer (`internal/release` already imports
   it). Making it import `internal/user_prompt` would couple the seam to
   CLI-style stdin prompting — exactly what #5 and the TUI must not inherit.

**Decision: the CLI resolves every interactive decision; `app` is
presentation-neutral.** `internal/cli` parses flags, loads config, runs route
validation, runs the resume-vs-new and keep-vs-overwrite-mix prompts, and produces
a fully-resolved `app.RelayStartOptions`. `internal/app` never imports
`internal/user_prompt`; it may *write* progress to `Out`/`Err` but never *reads*
to branch. This is the natural extension of the pattern already in the tree —
`cli.ValidateRelayStartupRoutes` already resolves an interactive concern CLI-side
and hands the runtime a resolved value (`validRoutes`). The start-of-run prompts
are not #5's territory: #5 owns the *in-flight* boundary; these are pre-run
decisions that have always belonged with flag parsing.

The seam the CLI uses:

```go
// internal/app
type RelayStartOptions struct {
    WorkspaceDir   string
    Config         config.V2Config // already loaded; Routes already validated
    TaskPrompt     string          // joined positional args, may be empty
    AgentMixSpecs  []string        // resolved selected specs
    UsedOverride   bool
    TargetIters    int             // already defaulted (laps-aware)
    LapsEnabled    bool
    DataDir        string
    Telemetry      TelemetryBuild  // license/app/host from main build vars

    // Resolved start-of-run decisions (no prompting inside app):
    DiscardUnfinishedRelay bool // --new, or user chose "start new" at the prompt
    ResetAgentStatus       bool // --new only; interactive "start new" must not clear status
    OverwriteMixOnResume   bool // current prompt path only; do not change --resume semantics

    Out, Err io.Writer
}

func StartRelay(ctx context.Context, opts RelayStartOptions) error

type ResumeInfo struct {
    HasUnfinished       bool
    RelayID             int
    CompletedIterations int
    TargetIterations    int
    AgentMix            string
}
func InspectResume(workspaceDir string) (ResumeInfo, error) // non-mutating store peek

func BuildExecutors(cfg config.V2Config) map[string]agent.Executor
```

The CLI `start` handler reads top-to-bottom as a script: parse flags → resolve
workspace → load config (print deprecation notes) → laps warning/detect → default
iterations → `chooseRelayAgentSpecs` → `ValidateRelayStartupRoutes` → initialized
workspace check → role-folder sync → laps hook install/auto-commit when laps is
enabled → `InspectResume` + resume/mix prompts → build `RelayStartOptions` →
`app.StartRelay`. Every interactive line is visible in `internal/cli`; every
composition-only line that cannot cross into `app` without a cycle (notably laps
hook install) stays in `internal/cli`; every lower-level runtime line is in
`internal/app`.

> Rejected alternatives. **(A) App owns IO** (draft's literal shape): forces the
> route-validation relocation above and the `user_prompt` coupling — more churn,
> weaker reuse. **(C) Prompts behind an injected `StartDecider` interface**:
> avoids the cycle and keeps a single store open, but adds an abstraction the
> call sites do not need yet and that #5 would likely reshape. The chosen split
> is the most boring and the most reusable; the only cost is `InspectResume`
> re-opening the store via the existing `store.NewStore` initialization/migration
> path so the CLI can prompt before `StartRelay` opens the same state for the run.

> Import-cycle constraint. Before `app.StartRelay` imports `runner`, the existing
> `release → app` metadata edge must move, because `runner` already imports
> `internal/laps` and `internal/laps` imports `internal/release`. `laps.InstallHooks`
> still stays in `internal/cli`: even after the cycle is broken, hook installation
> is start-command setup work and `StartRelay` only needs the resolved
> `LapsEnabled` value.

## Package & file manifest

### `cmd/rally` (`package main`)

- `main.go`: `Version` + `DefaultNewRelic{LicenseKey,AppName,HostDisplayName}`
  build vars, `main()`, `startBackgroundUpdateCheck`, the
  `cli.NewRootCommand(RootOptions{…}).Execute()` call, exit handling. ~40 lines.
- The Phase-1 same-package split files are an intermediate step; their contents
  drain into `internal/cli` in Phase 3 and the files are removed.

### `internal/cli`

- `root.go`: `RootOptions`, `NewRootCommand(opts) *cobra.Command` — registers
  `start`, `init`, hidden `init-roles`, `instructions`, `routes`, `hooks`,
  `config`, `version`, `update`, `progress`, and `tail`; the `--version`/`-v`
  flag; the dynamic help func that hides `progress` when laps is detected.
- `start.go`: the `start`/`relay` command + its handler (flag parse, config load,
  interactive resolution, `app.StartRelay`); `telemetryConfig` mapping from
  `RootOptions` build vars; `resolveWorkspaceDir`.
- `relay_flags.go`: `expandRelayFlag`, `chooseRelayAgentSpecs` (moved verbatim).
- `init.go`: `init` command, `runInit`, role bootstrap (`initRolesCmd`,
  `initAllCmd`, `roleBootstrap`, `defaultRoleBootstraps`, `runRolesSetup`,
  `syncRoleFolders`, `migrateLegacyRoleFiles`, `bootstrapInstructionsFor`).
- `init_templates.go`: `repoConfigTemplate`, `userConfigSeed`, the `.rally/README`
  body (section D).
- `instructions.go`: `instructions edit`/`show`.
- `update.go`: `version` + `update` commands.
- `tail.go`, `tail_highlight.go`: the existing live-tail command and highlighting
  helpers, moved with `tail_test.go`.
- Existing `config.go`, `hooks.go`, `routes_check.go`, `routes_startup.go` stay.

### `internal/app`

- `app.go`: existing path/env helpers (`ContainerDataDir`, `SessionDir`, …) +
  `ContainerDataRoot`/runtime env constants — unchanged, except release-facing
  constants move out before `app` imports `runner`.
- `relay_start.go`: `RelayStartOptions`, `ResumeInfo`, `TelemetryBuild`,
  `StartRelay`, `InspectResume`, `telemetryConfigForRelay`, the runner.Config
  assembly, resolver closure, override-route validation, instructions read, and
  signal handling (double-Ctrl+C window). It deliberately does not import
  `internal/laps`; hook install remains in the CLI layer so the app seam only
  receives resolved runner inputs.
- `executors.go`: `BuildExecutors`.

### `internal/release`

- `release.go`: additionally owns `BinaryName`, `ReleaseOwner`, `ReleaseRepo`, and
  `EnvNoUpdateCheck` (moved from `internal/app`) so release/update metadata does
  not require importing the lower app layer.

### `internal/gitx`

- `CommitSetupFiles(workspaceDir string, paths []string, message string) (bool,
  error)`: the relocated `commitSetupFiles` (path-scoped `--no-verify` setup
  commit), shared by CLI workspace setup and CLI laps-hook install.

### `internal/config` (file split, same package)

- `types.go`: schema-version const, name patterns/regexes, built-in alias maps,
  `Defaults`/`Laps`/`FreeRun`/`Reliability`/`Harness`/`Telemetry`/`V2Config`
  structs (+ the `ReliabilityConfig` timeout methods, kept beside their type),
  `RemovedGeminiAliasError` and its helpers, `rawConfig`/`rawFallbackAlias`.
- `load.go`: `V2Path`, `LoadV2`, `LoadV2File`, `readConfigFile`, `emptyV2Config`,
  `mergeTOMLDocuments`, `deepMergeMaps`.
- `decode.go`: `decodeV2`, `harnessConfigWarnings`, deprecation-note assembly.
- `validate.go`: `validateHarnesses`, `validateBuiltInHarness`, `validateRoutes`,
  `normalizeReasoning`, `ValidateRoutesTable`, the validation regexes.
- `resolve.go`: `defaultModelForHarness`, `ResolveAgent`, `ResolveRoleReasoning`,
  `canonicalHarnessName`, `lookupModelAlias`, `modelAliasNamesForHarness`,
  `harnessLookupKeys`, `modelNamesForHarness`, `didYouMean`, `levenshtein`, `min`,
  `effectiveModel`, `isRemovedGeminiAlias`, `RemovedGeminiAliasWarning`.
- `save.go`: `SaveV2`, `SaveV2File`, `providersToRaw`, `toAnySlice`.
- `providers.go`: unchanged.

Exact const/regex placement is design discretion as long as the package compiles
and no identifier is renamed; the table above is the intended grouping.

## Decisions

1. **Draw the package boundaries now, not after a same-package hedge.** The graph
   supports it (`cmd/rally` is the sole `cli` consumer; `app` exists). The Phase-1
   same-package split is a risk-reduction *first commit within this change*, not a
   reason to leave the boundary undrawn — matching #1's carried-over principle.
2. **CLI resolves interactivity; `app` is presentation-neutral.** See "The
   interactive seam." `internal/app` must not directly import
   `internal/user_prompt` or `internal/laps`; enforced by review here and
   available for #3 to codify as an import rule. Laps hook install stays CLI-side
   because it is start-command setup work, while `app.StartRelay` only needs the
   resolved `LapsEnabled` value.
3. **Build vars stay in `package main`, threaded via options.** GoReleaser targets
   `main.Version` / `main.DefaultNewRelicLicenseKey` / `main.DefaultNewRelicAppName`.
   They flow `main → RootOptions → RelayStartOptions.Telemetry`. `.goreleaser.yaml`
   is untouched; telemetry stays no-op until the relay path; baked credentials
   remain reachable only there. This is a hard correctness constraint, not a
   preference.
4. **`BuildExecutors` lives in `internal/app` for now.** It is composition wiring
   whose `map[string]agent.Executor` output feeds `runner.NewRunner`, not harness
   domain logic. #4 relocates it into a harness registry; keeping it a thin
   `app` helper now is a low-risk readability win and avoids pre-committing #4's
   home.
5. **Role-folder sync stays CLI-side.** `syncRoleFolders`/`migrateLegacyRoleFiles`
   are used by both `init roles` and the relay-start prep; with both commands in
   `internal/cli`, the start handler calls `syncRoleFolders` before `app.StartRelay`.
   `app` never needs it, so no shared `roles` package is introduced.
6. **`commitSetupFiles` → `internal/gitx`.** Both workspace setup and laps-hook
   install need the same path-scoped, `--no-verify` commit helper; `gitx` is the
   natural home for that Git-specific behavior even though both call sites remain
   CLI-side after the hook-install correction.
7. **Break `release → app` before `app → runner`.** `runner` legitimately imports
   `internal/laps`, and `laps` imports `release`. Move the release-facing constants
   (`BinaryName`, `ReleaseOwner`, `ReleaseRepo`, `EnvNoUpdateCheck`) from
   `internal/app` to `internal/release` before `StartRelay` imports `runner`; then
   update main/CLI callers to use `release.*`. This keeps `app.StartRelay` as the
   reusable seam without choosing a different package home to dodge the cycle.
8. **Config split is a same-package move only.** No config name, error string, or
   deprecation message changes unless a test proves a move forces it. Exported
   identifiers keep names and signatures; `go doc ./internal/config` before/after
   differs only in source file, not surface.

## Risks / Trade-offs

- **Largest mechanical surface is the Phase-3 promotion** of `package main`
  handlers into `internal/cli`. Mitigation: Phase 1 lands the same-package split
  green first; Phase 3 moves whole files/symbols verbatim and re-points imports;
  the compiler plus the existing `cmd/rally` test suite (which moves with its
  symbols) enforce correctness.
- **`InspectResume` re-opens the store** so the CLI can prompt before `StartRelay`
  opens it for the run. It must use the same `store.NewStore` migration path as
  startup so legacy state is visible before prompting; “read-only” means it does
  not complete relays or reset status, not that it bypasses the existing layout
  migration. Accepted to keep `app` free of `user_prompt`. If the duplicate open
  ever shows up, the fix is a single shared store handle threaded through options
  — not a boundary change.
- **Tests that assert on `package main` helpers/commands** (`commitSetupFiles`,
  `chooseRelayAgentSpecs`, `syncRoleFolders`, telemetry config, `tail`, command
  registration) move to the new homes. Risk is misclassifying a test; mitigation
  is moving whole `func TestXxx` blocks verbatim and keeping `go test ./...` green
  at every phase boundary.
- **Partial reuse until #5.** `app.StartRelay` is presentation-neutral for
  *startup*, but in-flight rendering still lives in `runner`. That is by design —
  #5 owns the in-flight boundary; this change only guarantees the start seam does
  not have to be re-derived.

## Migration Plan

Phases land in order, each green before the next (see `tasks.md`):

1. Same-package split of `main.go`, then `config_v2.go`. No package moves.
2. Break the `release → app` metadata edge, then add `app.BuildExecutors`,
   `app.InspectResume`, and `app.StartRelay`; the relay command (still in
   `package main`) delegates to them; `commitSetupFiles` → `gitx`.
3. `cli.NewRootCommand` + promote all handlers/helpers into `internal/cli`;
   `main.go` shrinks to entry.
4. Move config/init templates beside the `init` command.
5. README architecture section + `composition-root-structure` spec.

Each phase is independently revertible; the boundary-drawing phases (2–3) are
gated on a green `go test ./...` from the phase before.

## Open Questions

Resolved during proposal work:

- **Relay-start seam name** → `internal/app` (`app.StartRelay`), as the draft's
  working default; it is the low, presentation-neutral layer #5 reuses.
- **Interactive ownership** → CLI-side; `app` does not directly import
  `user_prompt` or `laps` (cycle + reuse, see Decisions 2).
- **Executor registry now vs #4** → small `app.BuildExecutors` now; #4 re-homes.

None remaining that block implementation.
