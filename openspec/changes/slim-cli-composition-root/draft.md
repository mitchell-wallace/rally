## Draft: Slim CLI Composition Root

Status: drafted 2026-06-29 - initial architecture concept only.

This change is an architectural refactor. It should not change command names,
flags, config semantics, telemetry activation, release behaviour, laps hook
installation, or runtime relay behaviour.

## Why

`cmd/rally/main.go` is 863 lines and imports most of the codebase. It currently
acts as:

- Cobra root and command registration,
- release/version wiring,
- config template storage,
- workspace resolution,
- relay flag interpretation,
- user/repo config loading,
- route startup validation,
- executor construction,
- laps hook installation,
- telemetry initialization,
- runner config assembly,
- resume/new-batch prompting,
- signal handling,
- command implementations for non-relay flows.

Large composition roots are common in CLIs, but this one is now carrying too
much implementation detail. It also makes future TUI work harder because the
runtime wiring for a relay is embedded inside the Cobra command body rather than
available as a small reusable application service.

`internal/config/config_v2.go` is 993 lines and similarly combines TOML decode,
layering, defaults, validation, deprecation warnings, alias resolution,
providers, route validation, reasoning, and save logic. Some of that belongs in
one package, but not necessarily one file.

## Intent

Make entry points slim and module interfaces explicit:

- `cmd/rally/main.go` should mostly set process-level build variables and call a
  command builder.
- Relay startup should be represented by a small app-layer function or object
  that both CLI and future TUI code can reuse.
- Config decode, validation, model resolution, and provider resolution should be
  easier to inspect independently.
- The code should remain boring Go, with no framework or dependency injection
  container.

## Candidate Work

### A. Move command construction behind a small CLI package entry point

Introduce a function such as:

```go
func NewRootCommand(opts RootOptions) *cobra.Command
```

Candidate package locations:

- `internal/cli`: natural because it already owns interactive config and route
  checks.
- `cmd/rally`: lower churn, by splitting `main.go` into multiple files while
  keeping package `main`.

Recommended first step: split within `cmd/rally` if that keeps the diff smaller,
then promote stable command builders into `internal/cli` later.

`main.go` should ideally retain only:

- build-time variables,
- `main()`,
- root command construction call,
- process exit handling.

### B. Extract relay startup orchestration

Introduce an app-layer type or function that turns CLI inputs into a configured
`relay.Runner` and runs it.

Possible shape:

```go
type RelayStartOptions struct {
    WorkspaceDir string
    Args []string
    Iterations int
    AgentSpecs []string
    MixSpecs []string
    Resume bool
    NewBatch bool
    In io.Reader
    Out io.Writer
    Err io.Writer
}

func StartRelay(ctx context.Context, opts RelayStartOptions) error
```

This can live in a new package such as `internal/runtime`, `internal/app/relay`,
or `internal/cli/relay`. The best name should be decided during proposal work.

The key is that Cobra should parse flags and then delegate. It should not own
the full relay lifecycle.

### C. Extract executor registry construction

Move the map construction for built-in and generic executors out of
`cmd/rally/main.go`.

Initial candidate:

```go
func BuildExecutors(cfg config.V2Config) map[string]agent.Executor
```

Potential homes:

- `internal/agent`: simple but keeps adapters and registry in one package.
- `internal/harness`: better long-term if harness adapters are modularized.
- `internal/runtime`: acceptable if registry construction remains application
  wiring rather than harness domain logic.

This should be coordinated with `modularize-harness-adapters` if that change is
implemented first.

### D. Move config templates out of `main.go`

The repo and user config seed strings currently live in `cmd/rally/main.go`.
Move them to a config/init-focused file or package, such as:

- `cmd/rally/init_config_templates.go` as a quick split,
- `internal/config/templates.go` if config package ownership is preferred,
- `internal/cli/init_templates.go` if only CLI init commands use them.

This is a low-risk readability win.

### E. Split `internal/config/config_v2.go` by responsibility

Keep package `config`, but split files around responsibilities:

- `types.go`: public config structs and constants,
- `load.go`: `LoadV2`, `LoadV2File`, layering and TOML read helpers,
- `decode.go`: `decodeV2`, defaults, deprecation note assembly,
- `validate.go`: harness and route validation,
- `resolve.go`: agent/model/reasoning resolution,
- `providers.go`: already split and should stay focused,
- `save.go`: save/write helpers if present in the tail of `config_v2.go`.

This should be a same-package move first. Do not change config names or errors
unless a test proves a message must be updated because of a move.

### F. Document the entry-point model

Update the README architecture section after the refactor. The docs should make
the progressive disclosure clear:

- `cmd/rally`: process entry and build-time values,
- `internal/cli`: Cobra command construction and interactive prompts,
- app/runtime package: orchestration from config to relay runner,
- `internal/relay`: relay/run/try execution,
- `internal/agent` or harness packages: executor contract and adapters.

## Testing Strategy

Before editing:

- Run `go test -count=1 ./cmd/rally ./internal/cli ./internal/config`.

After command wiring moves:

- Run `go test -count=1 ./cmd/rally ./internal/cli`.
- Add or update tests that instantiate the root command without running the
  process-level `main()` function.
- Exercise `--version`, `start/relay` flag parsing, config command creation,
  route commands, init commands, and update command registration.

After relay startup extraction:

- Add unit tests around the new relay-start app-layer function using fake IO and
  temp workspaces.
- Keep existing `cmd/rally/main_test.go` assertions green.
- Run `go test -count=1 ./internal/relay ./cmd/rally` to catch wiring drift.

After config file splits:

- Run `go test -count=1 ./internal/config`.
- Run `go test -count=1 ./cmd/rally` because config errors and deprecation notes
  surface through the CLI.

Before completion:

- Run `go test -count=1 ./...`.
- Run `just check` if available.

## Sequencing

1. Split `cmd/rally/main.go` into same-package files for obvious sections.
2. Split config into same-package files.
3. Extract executor registry construction.
4. Extract relay startup orchestration behind a reusable app-layer API.
5. Update README architecture docs.

## Open Questions

- Should the reusable relay-start API live in `internal/app`, `internal/runtime`,
  or `internal/cli`?
- Should command construction move immediately to `internal/cli`, or should the
  first change only split package-main files?
- Should executor registry extraction wait for harness modularization, or is a
  small `BuildExecutors` helper useful before that larger change?

## Out of Scope

- Changing command names, flags, defaults, or help text except where tests need
  imports updated.
- TUI implementation.
- Changing config schema or migration behaviour.
- Adding import-boundary CI. That belongs in `add-architecture-guardrails`.
