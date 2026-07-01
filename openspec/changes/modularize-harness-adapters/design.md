# Design — modularize-harness-adapters

Behaviour-preserving refactor of runtime harness code. No observable behaviour,
CLI, config, telemetry, or store change; no version bump; no release on its own.
Baselined against the tree at commit `1505f02` (2026-07-01), after
`decompose-relay-runner` (#1), `slim-cli-composition-root` (#2), and
`add-architecture-guardrails` (#3).

## Goals / non-goals

- **Goal**: make adding a harness feel like adding a module — a small contract
  package the rest of Rally depends on, and one deep module per harness that owns
  its CLI parsing and log recovery behind that contract.
- **Goal**: keep the `Executor` contract and every harness's observable behaviour
  identical; this is a relocation, not a behaviour change.
- **Goal**: let #3's guardrail engine hold the new harness boundaries and ratchet
  the `opencode.go` cap away.
- **Non-goal**: adding a harness, changing harness CLI flags, changing
  failure-classification semantics, moving the `reliability` parsers, renaming
  `ResolvedAgent`, or extracting the prompt builder (parked
  `extract-prompt-builder`).

## Decision 1 — dedicated harness API package (draft Option B)

Resolves the draft's only genuinely-open decision (API package name). Chosen
**Option B**: a dedicated `internal/harnessapi` contract package plus
`internal/harness/*` adapter modules, rather than keeping the overloaded `agent`
name (Option A). Rationale: Rally's terminology already distinguishes **runner**,
**harness**, **role**, and **agent prompt**; `agent` was the most overloaded of
those. The wide re-import is mechanical (`agent.` → `harnessapi.` for the contract
types), and the operator accepted the churn to improve the vocabulary. The
architecture guardrails (#3) prevent new bad imports regardless of the name.

`internal/agent` is emptied and removed by this change — no shim package is left
behind, so the rename is complete rather than aliased.

## Decision 2 — package layout

```text
internal/harnessapi/          # the contract, most-imported surface in the tree
  executor.go                 # Executor, RunOptions, TryResult, ResolvedAgent
  prompt.go                   # BuildPrompt (shared; consumed by adapters and runner)
  reasoning.go                # applyReasoningEffort, IsKnownReasoningEffort, warnings
internal/harness/
  process/                    # SetProcessGroup, runLoggedCommand, log helpers
  claude/                     # claude.New / claude.Executor + session-log parsing
  codex/                      # codex.New  / codex.Executor  + session-log parsing
  opencode/                   # opencode.New + server-log evidence recovery
  antigravity/                # antigravity.New + glog parsing + DefaultModel
  generic/                    # generic.New for user-defined command harnesses
  fixture/                    # fixture.New — the replay fake used by tests
  registry.go                 # package harness: BuildExecutors(Config)
```

`internal/harnessapi` MUST NOT import any `internal/harness/*` package (that would
cycle). Adapters depend on the contract; the contract never depends on the
adapters. The registry (`package harness`) is the only harness-layer package that
imports the adapter subpackages.

## Decision 3 — contract package contents

`internal/harnessapi` holds exactly the surface the rest of the tree consumes:

- `Executor` (the five-method interface, unchanged),
- `RunOptions`, `TryResult`, `ResolvedAgent` (unchanged field sets),
- `BuildPrompt(RunOptions) string` — shared by adapters **and** by
  `internal/relay/runner/run_one.go`, which calls it directly today
  (`agent.BuildPrompt` → `harnessapi.BuildPrompt`),
- the generic reasoning-effort helpers (`applyReasoningEffort`,
  `IsKnownReasoningEffort`, `unknownEffortWarning`, `emitReasoningWarning`) — they
  are harness-agnostic (a `switch harness` over flag spellings), so they belong
  with the contract, not duplicated per adapter.

No registry, telemetry, routing, or config concern enters the `Executor`
interface. Harnesses execute and return structured `TryResult`/`FailureEvidence`;
relay and runtime keep owning retry, routing, and telemetry emission.
`harnessapi` keeps `internal/agent`'s current leaf imports —
`internal/agent_prompt`, `internal/reliability`, `internal/textutil` — and adds no
others.

Three helpers the adapters call are currently **unexported same-package**
functions and must be **exported** once adapters live in sibling packages (see
Decision 10): `boundedExecutorFinalText` → `harnessapi.BoundedFinalText` (claude),
`applyReasoningEffort` → `harnessapi.ApplyReasoningEffort` (all four built-ins),
and `emitReasoningWarning` → `harnessapi.EmitReasoningWarning` (all four).
`unknownEffortWarning`, `sortedEffortValues`, `knownReasoningEfforts`, and
`isVerifyRole` are used only within `harnessapi` and stay unexported.

`BuildPrompt` staying here is deliberate and temporary: the parked
`extract-prompt-builder` change lifts it into its own module once prompt assembly
grows more distinct concerns. Until then, co-locating it with the contract avoids
a premature package.

## Decision 4 — adapter constructor convention

Each adapter package exposes an idiomatic constructor returning the interface:

```go
// internal/harness/claude
func New(model string) harnessapi.Executor   // concrete type: claude.Executor
```

- Concrete types are `<harness>.Executor` (or unexported where nothing outside the
  package needs the concrete type), constructed via `<harness>.New(...)`. This
  drops the `agent.ClaudeExecutor` stutter and gives each module a stable, tiny
  public surface.
- Each adapter owns its default-model constant where it has one:
  `antigravity.DefaultModel` (relocated from `agent.DefaultAntigravityModel`).
  `internal/cli/init_roles.go` and the real-backend test import
  `internal/harness/antigravity` for it; `cli` is a composition/presentation layer
  already permitted broad imports.
- The behavioural `executor` spec continues to define what each `New`-built
  executor does (flags, parsing, evidence, bounded summaries); only the type/
  package names move.

## Decision 5 — top-level `internal/harness` registry with a config-decoupled input

Resolves the draft's registry open questions. Chosen: the registry lives at the
**top level of `internal/harness`** and takes a **narrow adapter-shaped input**,
not `config.V2Config`.

```go
// package harness
func BuildExecutors(cfg Config) map[string]harnessapi.Executor

type Config struct {
    ClaudeModel      string
    CodexModel       string
    OpenCodeModel    string
    AntigravityModel string
    Custom           map[string]GenericConfig // one generic adapter per entry
}
```

- `harness.BuildExecutors` constructs the four built-in adapters keyed by
  canonical name plus a `generic.New(...)` per configured custom harness. Because
  `Config` is a plain adapter-shaped struct, the harness layer never imports
  `internal/config`.
- `internal/app.BuildExecutors(cfg config.V2Config) map[string]harnessapi.Executor`
  becomes a thin mapper: it translates `config.V2Config` (built-in model fields
  and `cfg.Harnesses` command specs) into `harness.Config` and delegates. The map
  it returns still feeds `runner.NewRunner`, so concrete adapter types stay out of
  both `cmd/rally` and the runner.
- `GenericConfig` carries the current `GenericExecutor` construction fields
  (`Command`, `ModelFlag`, `OutputStrategy`, `OutputLines`, `TailStream`); the
  `config → harness.Config` translation in `app` is the only place that knows both
  shapes.

Alternative considered — keep construction in `internal/app` (draft Option C):
rejected because "add a harness" would then edit a composition-root file and
`app` would depend on every adapter's internals. Alternative considered — registry
takes full `config.V2Config`: rejected to keep the harness layer independent of
the config schema.

## Decision 6 — process/log support package

Move the shared subprocess plumbing into `internal/harness/process`:
`SetProcessGroup` (graceful group-wide cancellation via
`reliability.KillProcessGroup`), `runLoggedCommand`, `WriteTryLog`/`openTryLog`,
and `tailString`. It imports only `internal/reliability` and the standard
library. It is deliberately small and MUST NOT accumulate parser helpers — those
stay in their owning adapter. Naming: `internal/harness/process` (not a generic
`internal/execx`) so ownership reads from the path.

`SetProcessGroup` and `WriteTryLog` are already exported; the three helpers
adapters call across the new boundary must be **exported** (Decision 10):
`runLoggedCommand` → `process.RunLoggedCommand` (all four built-ins),
`openTryLog` → `process.OpenTryLog` (codex, generic), and `tailString` →
`process.TailString` (antigravity).

## Decision 7 — per-harness parsing stays local; reliability keeps the taxonomy

- Each adapter owns its CLI-schema parsing and native-log recovery: Claude
  session-log parsing in `harness/claude`, Codex session-log parsing in
  `harness/codex`, OpenCode server-log evidence in `harness/opencode`, Antigravity
  glog parsing in `harness/antigravity`. The large `opencode.go` splits into
  responsibility-named files within `harness/opencode` (execution vs server-log
  evidence), which is what removes its 801-line grandfather cap.
- The `internal/reliability` per-harness parsers (`ParseClaudeError`,
  `ParseCodexError`, `ParseOpencodeError`, `ParseAntigravityError`) **stay in
  `internal/reliability`**. They encode Rally's normalized failure taxonomy, not
  harness process execution; adapters keep calling them and returning
  `reliability.FailureEvidence`. Moving them would be a behaviour-risk refactor
  outside this change's scope. This resolves the draft's open question in the
  conservative, behaviour-preserving direction.
- The 2,812-line `internal/agent/agent_test.go` is a monolith mixing every
  harness's tests plus `BuildPrompt`/reasoning tests. It is distributed with the
  code it exercises: contract/`BuildPrompt`/reasoning tests → `harnessapi`, each
  executor's tests → its `harness/<name>` package (joining the already-separate
  `*_sessionlog_test.go` / `*_glog_test.go` / `generic_test.go`). Relocated test
  files SHALL respect #3's 1,000-line `_test.go` cap, split by concern (e.g.
  `opencode_evidence_test.go` vs `opencode_exec_test.go`) where a single adapter's
  suite would exceed it. This coordinates with #8 (`decompose-large-test-files`),
  which owns the stable-package test caps; a grandfather entry is a last resort
  only if a suite cannot be cleanly split within this change.

## Decision 8 — import-boundary rules and guardrail updates

The new packages are lower-level modules held to tight internal allow-lists, and
the changed consumers swap `agent` for `harnessapi`. Update the `tools/archguard`
policy tables (#3) to match the new production graph.

New-package allow-lists (each may import only these internal packages):

| Package | May import (internal) |
|---|---|
| `internal/harnessapi` | `agent_prompt`, `reliability`, `textutil` |
| `internal/harness/process` | `reliability` |
| `internal/harness/claude` | `harnessapi`, `harness/process`, `reliability` |
| `internal/harness/codex` | `harnessapi`, `harness/process`, `reliability` |
| `internal/harness/opencode` | `harnessapi`, `harness/process`, `reliability` |
| `internal/harness/antigravity` | `harnessapi`, `harness/process`, `reliability` |
| `internal/harness/generic` | `harnessapi`, `harness/process`, `reliability` |
| `internal/harness/fixture` | `harnessapi` |
| `internal/harness` (registry) | `harnessapi`, `harness/{claude,codex,opencode,antigravity,generic}` |

Changed consumer allow-lists (swap `agent` → `harnessapi`):

| Package | May import (internal) |
|---|---|
| `internal/config` | `harnessapi`, `routing`, `store` |
| `internal/routing` | `harnessapi` |
| `internal/relay` | `harnessapi`, `store` |
| `internal/relay/runner` | `harnessapi` (in place of `agent`), plus its current set (`agent_prompt`, `gitx`, `keyboard`, `laps`, `monitor`, `progress`, `relay`, `reliability`, `routing`, `store`, `style`, `telemetry`, `textutil`, `user_prompt/roleloader`) |
| `internal/app` | `harnessapi`, `harness`, `config`, `relay`, `relay/runner`, `routing`, `store`, `telemetry` |

The adapter-confinement intent (encoded by the tight allow-lists above): no
`internal/harness/*` adapter may import `internal/relay`, `internal/relay/runner`,
`internal/config`, `internal/store`, `internal/progress`, `internal/telemetry`,
`internal/cli`, or any future presentation package. The diagnostic explains the
intent ("harness adapters must not depend on relay/runtime/presentation; they
execute and return typed evidence").

Grandfather ratchet: remove the `internal/agent/opencode.go` entry (801) from the
`--report` baseline now that `opencode` is split into `internal/harness/opencode`.
Regenerate the grandfather map against HEAD at implementation time rather than
trusting these figures. This is a policy-table update; the
`architecture-guardrails` spec already requires tight per-package allow-lists, so
it is not modified.

## Decision 9 — behaviour preservation

Command names, flags, help text, config schema and semantics, prompt content,
telemetry event/field names and activation timing, store shape, and
agent-authored commit messages are unchanged. Observable *runtime behaviour* is
identical; what moves is package paths, concrete type names, and the visibility of
the six helpers in Decision 10 (unexported → exported). No consumer relied on
those helpers being unexported (they were same-package only), so exporting them is
safe. The change does not touch `internal/buildinfo/VERSION` and does not require
a release.

## Decision 10 — exported helper surface (relocation is not a pure move)

Six helpers cross a new package boundary and therefore change from unexported to
exported. This is the one way the refactor is *not* a pure relocation, so it is
called out explicitly to keep the implementation honest:

| Current (unexported, `internal/agent`) | New (exported) | Adapter callers |
|---|---|---|
| `boundedExecutorFinalText` (executor.go) | `harnessapi.BoundedFinalText` | claude |
| `applyReasoningEffort` (reasoning.go) | `harnessapi.ApplyReasoningEffort` | claude, codex, opencode, antigravity |
| `emitReasoningWarning` (reasoning.go) | `harnessapi.EmitReasoningWarning` | claude, codex, opencode, antigravity |
| `runLoggedCommand` (log.go) | `process.RunLoggedCommand` | claude, codex, opencode, antigravity |
| `openTryLog` (log.go) | `process.OpenTryLog` | codex, generic |
| `tailString` (log.go) | `process.TailString` | antigravity |

`SetProcessGroup`, `WriteTryLog`, `BuildPrompt`, and `IsKnownReasoningEffort` are
already exported and keep their names (re-homed). Everything else stays
unexported within its new package.

## Sequencing

1. Introduce `internal/harnessapi` with the contract, `BuildPrompt`, and reasoning
   helpers; re-point all consumers. Tree compiles.
2. Extract `internal/harness/process`; re-point adapters (still in `agent`) to it.
3. Move adapters one at a time into `internal/harness/<name>` with their tests,
   starting with `generic` (self-contained, 216 lines) and `fixture`, then
   `claude`/`codex`/`antigravity`, then `opencode` last (largest, splits into
   multiple files).
4. Add `internal/harness.BuildExecutors` + `harness.Config`; make
   `app.BuildExecutors` the thin mapper; remove the now-empty `internal/agent`.
5. Update `tools/archguard` policy tables and regenerate the grandfather map
   (drop `opencode.go`).
6. Update README architecture docs and the `add-new-harness` skill.

## Testing strategy

Before moving adapters:

- `go test -count=1 ./internal/agent ./internal/reliability ./internal/config ./internal/relay`.

Per adapter move:

- Move its unit tests with it; run the new adapter package tests.
- `go test -count=1 ./internal/harness/... ./internal/harnessapi`.
- `go test -count=1 ./internal/relay/... ./internal/app ./cmd/rally` — relay
  consumes the interface and `app`/command wiring construct executors.

For the registry:

- Unit-test built-in registration (four canonical names present) and
  generic/custom registration from `harness.Config`.
- Unit-test that configured model defaults reach the right adapter, and that
  `app.BuildExecutors` maps `config.V2Config` to the same executor set as before.

Guardrails and full suite:

- `go run ./tools/archguard --report` to regenerate the baseline, then
  `go run ./tools/archguard --ci` exits 0; `just check` green; `go vet ./...`;
  `gofmt -l .` empty; `go mod tidy` no diff.
- `go test -count=1 ./...`. If real-backend harness tests are affected, run
  `RALLY_TEST_REAL_AGENTS=1 go test -count=1 ./internal/harness/... ./cmd/rally`
  where local credentials allow.

## Open questions — resolved

- **API package name** → `internal/harnessapi` + `internal/harness/*` (Option B);
  `internal/agent` removed, no shim (Decision 1).
- **`BuildPrompt` location** → stays with the contract in `internal/harnessapi`
  for now; the parked `extract-prompt-builder` change lifts it out later
  (Decision 3).
- **`reliability` per-harness parsers** → stay in `internal/reliability` as the
  normalized taxonomy; adapters keep calling them (Decision 7).
- **Registry input** → narrow `harness.Config`, not full `config.V2Config`;
  registry lives at the top level of `internal/harness`; `app.BuildExecutors`
  maps config → registry (Decision 5).
