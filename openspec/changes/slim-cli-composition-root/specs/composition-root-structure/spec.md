## ADDED Requirements

### Requirement: Layered composition root

The process entry, command construction, and relay-start orchestration SHALL be
separated into three layers with a strictly downward dependency direction:
`cmd/rally` (`package main`) → `internal/cli` → `internal/app`. `internal/cli`
MAY import `internal/app`; `internal/app` SHALL NOT import `internal/cli`. The
full chain `cmd/rally → internal/cli → internal/app → internal/relay/runner →
internal/relay` SHALL contain no import cycle.

#### Scenario: Downward-only dependency, no cycle

- **WHEN** the package import graph is inspected after the change
- **THEN** `internal/cli` imports `internal/app`, `internal/app` does not import
  `internal/cli`, and `go build ./...` reports no import cycle

#### Scenario: Slim process entry

- **WHEN** `cmd/rally/main.go` is read after the change
- **THEN** it contains only build-time variables (`Version`,
  `DefaultNewRelicLicenseKey`, `DefaultNewRelicAppName`,
  `DefaultNewRelicHostDisplayName`), `main()`, the background update-check, the
  `cli.NewRootCommand(...).Execute()` call, and process exit handling — and no
  command implementation or relay lifecycle body

#### Scenario: Command construction owned by internal/cli

- **WHEN** the `start`/`relay`, `init`, hidden `init-roles`, `instructions`,
  `version`, `update`, and `tail` commands are located
- **THEN** they are constructed in `internal/cli` (via `cli.NewRootCommand` and
  per-command files) alongside the existing `NewConfigCmd` / `NewRoutesCmd` /
  `NewHooksCmd`, not in `package main`

### Requirement: Presentation-neutral relay-start seam

Relay startup SHALL be reachable through a reusable app-layer function
`app.StartRelay(ctx, app.RelayStartOptions) error` that turns already-resolved
inputs into a configured `runner.Runner` and runs it. `internal/app` SHALL NOT
import `internal/user_prompt` or `internal/laps`: all interactive start-of-run
decisions (resume-vs-new, keep-vs-overwrite mix, and interactive route
validation) SHALL be resolved in `internal/cli` and passed to `app.StartRelay` as
concrete values, and laps hook installation SHALL remain CLI-side before
`app.StartRelay`. The app layer MAY write progress to caller-supplied `Out`/`Err`
writers but SHALL NOT read input to branch on it. A read-only
`app.InspectResume(workspaceDir) (app.ResumeInfo, error)` SHALL expose the
unfinished-relay summary the CLI needs to prompt without itself opening the
store.

#### Scenario: App seam does not depend on interactive prompting

- **WHEN** `go list -deps ./internal/app` is inspected after the change
- **THEN** neither `internal/user_prompt` nor `internal/laps` is in the dependency
  set, and `app.StartRelay` performs no stdin prompting

#### Scenario: Interactive decisions resolved CLI-side

- **WHEN** the `start` command runs with an unfinished relay present and no
  `--resume`/`--new` flag
- **THEN** the resume-vs-new (and any keep-vs-overwrite-mix) prompt is issued from
  `internal/cli`, and `app.StartRelay` receives the resolved decision as a field
  on `RelayStartOptions`, with identical observable behaviour to the pre-change
  `runRelay`

#### Scenario: Laps hooks stay outside the app seam

- **WHEN** a laps-backed `start` command runs and hook files need installing
- **THEN** `internal/cli` performs the existing `laps.InstallHooks` and setup-file
  auto-commit before calling `app.StartRelay`, while `app.StartRelay` only receives
  the already-resolved `LapsEnabled` value for runner configuration

#### Scenario: New-start decisions preserve current reset semantics

- **WHEN** the user passes `--new`
- **THEN** the unfinished relay is completed and agent status is reset as before
- **WHEN** the user chooses "Start new" from the interactive unfinished-relay
  prompt
- **THEN** the unfinished relay is completed but agent status is not reset, matching
  the pre-change `runRelay` behavior

#### Scenario: Executor registry feeds the runner from the composition root

- **WHEN** the built-in and generic executors are constructed
- **THEN** they are built by `app.BuildExecutors(cfg) map[string]agent.Executor`
  and passed to `runner.NewRunner`, with the same harness set and configuration as
  the pre-change inline map

### Requirement: Build-time variables remain process-scoped

The release build-time variables SHALL remain declared in `package main` so the
GoReleaser ldflag targets `main.Version`, `main.DefaultNewRelicLicenseKey`, and
`main.DefaultNewRelicAppName` stay valid. They SHALL be threaded into the lower
layers via `cli.RootOptions` and `app.RelayStartOptions` rather than redeclared in
`internal/cli` or `internal/app`. Telemetry SHALL remain inactive (no-op sink)
until the relay path, and `.goreleaser.yaml` SHALL be unchanged.

#### Scenario: ldflag injection still works

- **WHEN** the binary is built with `-X main.Version=... -X
  main.DefaultNewRelicLicenseKey=... -X main.DefaultNewRelicAppName=...`
- **THEN** the build succeeds, `rally version` reflects the injected version, and
  the injected telemetry defaults reach `app.StartRelay` unchanged

#### Scenario: Telemetry stays no-op off the relay path

- **WHEN** a mechanical command (`version`, `update`, `--help`) runs
- **THEN** no telemetry client is opened and no machine-id file is written, as
  before the change

### Requirement: Config module structure

`internal/config/config_v2.go` SHALL be split into responsibility-named files in
the same `package config` (`types.go`, `load.go`, `decode.go`, `validate.go`,
`resolve.go`, `save.go`; `providers.go` unchanged). The split SHALL be a
file-only move: no exported identifier SHALL be added, removed, renamed, or have
its signature changed, and no config error string or deprecation message SHALL
change unless a test proves a move forces it.

#### Scenario: Exported surface unchanged across the split

- **WHEN** the exported identifiers of `internal/config` are compared before and
  after the change
- **THEN** the sets are identical and differ only in declaring source file

#### Scenario: Config behaviour preserved

- **WHEN** `go test -count=1 ./internal/config ./cmd/rally` runs after the split
- **THEN** it passes with no assertion changes beyond test relocations, and
  deprecation notes and validation errors surface through the CLI exactly as
  before

### Requirement: Behaviour, telemetry, release, and laps preservation

The refactor SHALL be behaviour-preserving. Command names, flags, help text,
config schema and semantics, telemetry event/field names and activation timing,
release behaviour, laps hook installation, persisted-store shape, and
agent-authored git commit messages SHALL be unchanged. The change SHALL NOT bump
`internal/buildinfo/VERSION` and SHALL NOT require a release.

#### Scenario: No behaviour-surface edits

- **WHEN** the diff of the change is reviewed
- **THEN** it contains no command-name/flag/help-text, config-schema/semantic,
  telemetry-field/activation, laps-hook, store-shape, or git-message edit, and
  leaves `internal/buildinfo/VERSION` untouched

#### Scenario: Full suite green after re-homing

- **WHEN** `go test -count=1 ./...` and `go test -race -shuffle=on -count=1
  ./internal/app ./internal/cli` run after the change
- **THEN** both pass, every relocated test (`commitSetupFiles`,
  `chooseRelayAgentSpecs`, `syncRoleFolders`, telemetry-config, command
  registration) appears exactly once in its new home, and no new behavioural test
  is required beyond the `app.StartRelay` / `app.InspectResume` unit coverage
