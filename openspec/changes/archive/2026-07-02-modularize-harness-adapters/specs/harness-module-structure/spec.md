## ADDED Requirements

### Requirement: Harness contract package

The harness executor contract SHALL live in a dedicated `internal/harnessapi`
package containing the `Executor` interface, `RunOptions`, `TryResult`,
`ResolvedAgent`, the shared `BuildPrompt` function, and the harness-agnostic
reasoning-effort helpers. `internal/harnessapi` SHALL depend only on leaf support
packages (`internal/agent_prompt`, `internal/reliability`, `internal/textutil`)
and SHALL NOT import any `internal/harness/*` adapter package, nor
`internal/relay`, `internal/relay/runner`, `internal/config`, `internal/store`,
`internal/progress`, `internal/telemetry`, `internal/cli`, or any presentation
package. The `Executor` interface SHALL carry no registry, telemetry, routing, or
config concern: harnesses execute and return structured `TryResult`/evidence, and
relay/runtime retain retry, routing, and telemetry ownership. The former
`internal/agent` package SHALL be removed with no compatibility shim.

#### Scenario: Contract types resolve from harnessapi

- **WHEN** `internal/config`, `internal/routing`, `internal/relay`,
  `internal/relay/runner`, `internal/app`, and `internal/cli` are built after the
  change
- **THEN** they import `internal/harnessapi` for the contract types and shared
  helpers they use (`ResolvedAgent` in config/routing/relay/runner/app; `Executor`
  in app/runner; `RunOptions`/`TryResult`/`BuildPrompt` in runner;
  `IsKnownReasoningEffort` in cli), no package imports `internal/agent`, and
  `go build ./...` reports no import cycle

#### Scenario: Contract package does not depend on adapters or runtime

- **WHEN** `go list -deps ./internal/harnessapi` is inspected
- **THEN** it includes no `internal/harness/*` adapter package and none of
  `internal/relay`, `internal/relay/runner`, `internal/config`, `internal/store`,
  `internal/progress`, `internal/telemetry`, or `internal/cli`

### Requirement: Per-harness adapter modules

Each built-in harness SHALL be a deep module under `internal/harness/<name>`
(`claude`, `codex`, `opencode`, `antigravity`, `generic`) exposing an idiomatic
constructor `New(...) harnessapi.Executor` over its own concrete `Executor` type.
Production files in each adapter module SHALL limit their direct Rally-internal
imports to `internal/harnessapi`, `internal/harness/process`, and
`internal/reliability`, and SHALL own their own
harness CLI-schema parsing and native session-/server-log recovery. Production
adapter files SHALL NOT import one another and SHALL NOT import `internal/relay`,
`internal/relay/runner`, `internal/config`, `internal/store`, `internal/progress`,
`internal/telemetry`, `internal/cli`, or any presentation package. Adapter test
files remain outside the production import-boundary allow-list, while dependency
confinement still applies to tests. Each adapter SHALL own its harness
default-model constant where it has one (e.g.
`antigravity.DefaultModel`). The replay `fixture` adapter SHALL move to
`internal/harness/fixture` under the same convention.

#### Scenario: Adapter constructed via its package constructor

- **WHEN** the registry builds the built-in executors
- **THEN** each is constructed via its package constructor (e.g. `claude.New`,
  `opencode.New`) returning a `harnessapi.Executor`, with no `agent.XExecutor`
  type name remaining anywhere in the tree

#### Scenario: Adapter package stays confined to the contract and support layers

- **WHEN** the **direct** internal imports of a production
  `internal/harness/<name>` adapter file are inspected (as `archguard` parses
  them via `parser.ImportsOnly`, or
  `go list -f '{{.Imports}}'`)
- **THEN** they are a subset of `internal/harnessapi`, `internal/harness/process`,
  and `internal/reliability`, with no other adapter package and none of relay,
  config, store, progress, telemetry, cli, or presentation

#### Scenario: Per-harness parsing lives in the owning module

- **WHEN** a harness's CLI output or native log is parsed for a `TryResult` or
  `FailureEvidence`
- **THEN** that parsing lives in the owning `internal/harness/<name>` module
  (Claude/Codex session logs, OpenCode server log, Antigravity glog), not in a
  shared cross-harness helper

### Requirement: Harness registry with config-decoupled input

Harness executor construction SHALL be owned by a top-level registry
`internal/harness.BuildExecutors(cfg harness.Config) map[string]harnessapi.Executor`
that constructs the built-in adapters keyed by canonical name plus one generic
adapter per configured custom harness. `harness.Config` SHALL be a narrow
adapter-shaped input (built-in model strings and a map of generic-harness command
specs), and the `internal/harness` layer SHALL NOT import `internal/config`.
`internal/app.BuildExecutors(cfg config.V2Config) map[string]harnessapi.Executor`
SHALL become a thin mapper that translates `config.V2Config` into `harness.Config`
and delegates, and its result SHALL continue to feed `runner.NewRunner`, keeping
concrete adapter types out of both `cmd/rally` and `internal/relay/runner`.

#### Scenario: Registry builds the built-in and generic executors

- **WHEN** `harness.BuildExecutors` is called with model defaults and one generic
  harness command spec
- **THEN** the returned map contains the four built-in canonical names
  (`claude`, `codex`, `opencode`, `antigravity`) plus the generic harness, each
  constructed with its configured model/command, matching the pre-change executor
  set

#### Scenario: Harness layer does not import config

- **WHEN** `go list -deps ./internal/harness` and `./internal/harness/...` are
  inspected
- **THEN** none of them import `internal/config`; the `config.V2Config →
  harness.Config` translation lives only in `internal/app`

#### Scenario: Executor map still feeds the runner from the composition root

- **WHEN** a relay starts
- **THEN** `app.BuildExecutors(cfg)` produces the executor map passed to
  `runner.NewRunner`, with the same harness set and configuration as before the
  change, and neither `cmd/rally` nor `internal/relay/runner` references a concrete
  adapter type

### Requirement: Process and log support package

The shared subprocess plumbing SHALL live in `internal/harness/process` —
process-group setup with graceful group-wide cancellation, the logged-command
runner, and the try-log helpers — importing only `internal/reliability` and the
standard library. It SHALL NOT contain harness CLI parser helpers, which belong
to the owning adapter modules.

#### Scenario: Support package is minimal and dependency-light

- **WHEN** the **direct** internal imports of `internal/harness/process` are
  inspected
- **THEN** its only direct internal import is `internal/reliability`
  (transitively `internal/monitor` via reliability), and it holds the shared
  process/log helpers rather than any harness-specific parser

### Requirement: Harness boundaries enforced by the guardrail

The architecture checker (`tools/archguard`, from `architecture-guardrails`) SHALL
enforce the new harness package boundaries using tight per-package internal
allow-lists that match the post-change production graph: `internal/harnessapi`,
`internal/harness/process`, each adapter module, and the `internal/harness`
registry each limited to the imports in `design.md` Decision 8, and the
`internal/config`, `internal/routing`, `internal/relay`, `internal/relay/runner`,
and `internal/app` allow-lists updated to import `internal/harnessapi` in place of
`internal/agent`. A production adapter import of a forbidden package SHALL hard-
fail with an architectural reason. The `internal/agent/opencode.go` grandfather
entry SHALL be removed once `opencode` is split, and no `internal/harness/**` file
SHALL require a new grandfather entry. These are `archguard` policy-table updates
consistent with the existing `architecture-guardrails` spec; that spec is not
redefined.

#### Scenario: Adapter importing relay hard-fails

- **WHEN** a production file in an `internal/harness/<name>` adapter imports
  `internal/relay`, `internal/relay/runner`, `internal/config`, `internal/store`,
  `internal/progress`, `internal/telemetry`, or `internal/cli`
- **THEN** `archguard --ci` exits non-zero with a message explaining harness
  adapters must not depend on relay/runtime/presentation and instead return typed
  evidence

#### Scenario: Split opencode drops its grandfather cap

- **WHEN** `archguard --report` is regenerated against the post-change tree
- **THEN** no `internal/agent/opencode.go` entry remains, no `internal/harness/**`
  production file needs a grandfather entry, and `archguard --ci` exits 0 —
  relocated `_test.go` files stay under the 1,000-line cap by being split per
  concern rather than grandfathered

### Requirement: Behaviour-preserving relocation

This change SHALL be a behaviour-preserving relocation. The `Executor` contract
and every harness's observable behaviour (CLI flags, output parsing, failure
evidence, bounded summaries, reasoning-effort injection) defined by the `executor`
capability SHALL be unchanged. What moves is package paths, concrete type names,
and the visibility of six previously-unexported same-package helpers that adapters
now call across a package boundary (`BoundedFinalText`, `ApplyReasoningEffort`,
`EmitReasoningWarning` in `internal/harnessapi`; `RunLoggedCommand`, `OpenTryLog`,
`TailString` in `internal/harness/process`) — no consumer relied on those being
unexported. Command names, flags, help text, config schema and semantics, prompt
content, telemetry event/field names and activation timing, and store shape SHALL
be unchanged. The `internal/reliability` per-harness parsers SHALL remain in
`internal/reliability`. The change SHALL NOT bump `internal/buildinfo/VERSION` and
SHALL NOT require a release.

#### Scenario: Full suite green after relocation

- **WHEN** `go test -count=1 ./...` and `go run ./tools/archguard --ci` run after
  the change
- **THEN** both pass, every relocated adapter test appears exactly once in its new
  `internal/harness/<name>` home, and no new behavioural test is required beyond
  the registry unit coverage

#### Scenario: No behaviour-surface or release edits

- **WHEN** the diff of the change is reviewed
- **THEN** it contains no command-name/flag/help-text, config-schema/semantic,
  telemetry-field/activation, prompt-content, or store-shape edit, leaves the
  `internal/reliability` parsers in place, leaves `internal/buildinfo/VERSION`
  untouched, and implies no release
