## Why

Rally is expected to support more first-class harnesses over time, but every
harness adapter, the executor contract, the shared prompt builder, the
reasoning-effort mapping, the process/log helpers, and the per-harness parsers
all live in one flat `internal/agent` package. A 2026-07-01 scan (commit
`1505f02`, after **#1 decompose-relay-runner** and **#2 slim-cli-composition-root**)
shows the growth pressure:

- `internal/agent/opencode.go` — 801 lines (the #3 grandfather cap this change
  ratchets away),
- `internal/agent/claude.go` — 560 lines,
- `internal/agent/antigravity.go` — 517 lines,
- `internal/agent/agent_test.go` — 2,812 lines.

Adding a harness today means editing a growing central package and invites
cross-harness helper leakage. Harness modules should instead be **deep**: each
adapter can carry whatever CLI-schema parsing and disk-log recovery complexity it
needs, while the rest of Rally sees a small, stable interface. This is the same
lesson as #1 and #2 — draw the boundary the dependency graph already supports
rather than leaving everything in one file.

The contract types are also the most-imported surface in the tree:
`agent.TryResult`, `agent.ResolvedAgent`, `agent.RunOptions`, and `agent.Executor`
are referenced from `internal/config`, `internal/routing`, `internal/relay`,
`internal/relay/runner`, `internal/app`, and `internal/cli`. That breadth is
exactly why the contract deserves its own small package, decoupled from the
adapter bulk that changes far more often.

This is change **#4** in the architecture sequence (`openspec/next-up.md`). It is
a **behaviour-preserving refactor** of runtime code: it relocates and repackages
existing harness code without changing any observable behaviour, CLI flag, config
schema, telemetry field, or store shape. It does not bump
`internal/buildinfo/VERSION` and does not require a release on its own.

## What Changes

The change adopts a dedicated harness API package plus one module per harness
(the draft's **Option B**, chosen deliberately to improve the terminology even at
the cost of a wide but mechanical re-import). It rolls out contract-first so the
tree compiles at each phase.

**Phase 1 — freeze and relocate the executor contract:**

- Introduce `internal/harnessapi` holding the minimal contract: `Executor`,
  `RunOptions`, `TryResult`, `ResolvedAgent`, the shared `BuildPrompt`, and the
  generic reasoning-effort helpers (`applyReasoningEffort`,
  `IsKnownReasoningEffort`). No registry, telemetry, or routing concern enters the
  interface — harnesses execute and return structured results/evidence; relay and
  runtime keep owning retry, routing, and telemetry.
- Re-point every consumer (`config`, `routing`, `relay`, `relay/runner`, `app`,
  `cli`, and their tests) from `internal/agent` to `internal/harnessapi` for the
  contract types. `run_one.go` keeps calling the shared `BuildPrompt`, now
  `harnessapi.BuildPrompt`.

**Phase 2 — split the process/log support:**

- Move the shared process-group and logged-command helpers (`SetProcessGroup`,
  `runLoggedCommand`, `WriteTryLog`, and the log tail helpers) into
  `internal/harness/process`, importing only `internal/reliability` and the
  standard library. Keep it small — it is not a home for parser helpers.

**Phase 3 — move adapters into per-harness modules (the deep-module move):**

- Give each built-in harness its own package under `internal/harness/`:
  `claude`, `codex`, `opencode`, `antigravity`, `generic`, plus the `fixture`
  fake. Each exposes an idiomatic `New(...) harnessapi.Executor` constructor over
  its own concrete `Executor` type, importing only `internal/harnessapi`,
  `internal/reliability`, and `internal/harness/process`. Per-harness CLI parsing
  and disk-log recovery (Claude/Codex session logs, OpenCode server log,
  Antigravity glog) stay in the owning module. Each adapter owns its default-model
  constant (e.g. `antigravity.DefaultModel`).
- Move each adapter's unit tests with it.

**Phase 4 — introduce the harness registry:**

- Add `internal/harness.BuildExecutors(harness.Config) map[string]harnessapi.Executor`
  as the top-level factory that constructs the built-in adapters and a generic
  adapter per configured custom harness. `harness.Config` is a narrow
  adapter-shaped struct (built-in model strings + a map of generic-harness command
  specs), so the harness layer never imports `internal/config`.
- `internal/app.BuildExecutors(cfg config.V2Config)` becomes a thin mapper:
  `config.V2Config → harness.Config → harness.BuildExecutors`. Its output still
  feeds `runner.NewRunner` at the composition root, keeping concrete adapter types
  out of both `cmd/rally` and the runner.

**Phase 5 — tighten the architecture guardrails:**

- Update the `tools/archguard` policy tables (#3): add tight internal allow-lists
  for `harnessapi`, `harness/process`, each adapter package, and the `harness`
  registry; confine adapter packages from importing `internal/relay`,
  `internal/relay/runner`, `internal/config`, `internal/store`,
  `internal/progress`, `internal/telemetry`, `internal/cli`, and future
  presentation packages; and remove the `internal/agent/opencode.go` grandfather
  entry now that `opencode` is split. This is a policy-table update consistent
  with the existing `architecture-guardrails` spec (which already requires tight
  per-package allow-lists) — no archguard spec change is needed.

**Phase 6 — durable guidance:**

- Update the README architecture section and the `add-new-harness` skill so
  "add a harness" is documented as "add a `internal/harness/<name>` module and
  register it," reflecting the new layout.

## Capabilities

### New Capabilities

- `harness-module-structure`: the modular harness architecture — the
  `internal/harnessapi` contract package, one `internal/harness/<harness>` module
  per built-in adapter (deep modules owning their own parsing/log recovery), the
  `internal/harness/process` support package, the top-level
  `internal/harness.BuildExecutors` registry with a config-decoupled input, and
  the one-way import boundaries that keep adapters from depending on relay,
  config, store, telemetry, progress, or presentation. Includes the
  behaviour-preserving / no-version-bump contract.

### Modified Capabilities

- `executor`: keeps the behavioural executor contract intact while aligning the
  spec with the current six-method lifecycle interface and moving the OpenCode
  disk-log machinery reference from `internal/agent/opencode.go` to the relocated
  `internal/harness/opencode` adapter files. It also updates the concrete adapter
  type/package names (`agent.ClaudeExecutor` → `claude.Executor` via
  `claude.New`, etc.) without changing CLI flag, parsing, evidence, retry,
  telemetry, or prompt behaviour.
- `composition-root-structure`: updates the executor-registry seam from
  `app.BuildExecutors(cfg) map[string]agent.Executor` to
  `app.BuildExecutors(cfg) map[string]harnessapi.Executor`, reflecting the new
  contract package while preserving the same runner wiring.

## Impact

- **Code**: new `internal/harnessapi/` and `internal/harness/**` packages
  (contract + `process` + six adapter modules + registry). `internal/agent` is
  emptied and removed. `internal/app/executors.go` becomes a thin config→registry
  mapper. Wide but mechanical import churn across `config`, `routing`, `relay`,
  `relay/runner`, `app`, `cli`, and their tests (`agent.` → `harnessapi.`). Six
  previously-unexported same-package helpers become exported because adapters now
  call them across a package boundary (see `design.md` Decision 10) — the one way
  this is not a pure move.
- **Behaviour**: none. No CLI flag, config schema, telemetry field/activation,
  store shape, prompt content, or agent-authored commit message changes. The
  `Executor` contract and every harness's observable behaviour are preserved.
- **Guardrails**: `tools/archguard` policy tables gain the new package allow-lists
  and adapter-confinement rules; the `opencode.go` grandfather cap is removed.
  No `architecture-guardrails` spec change.
- **Dependencies**: none added or removed; `go.mod`/`go.sum` untouched.
- **Sequencing**: consumes #3's guardrail engine (tightens harness-package
  boundaries and ratchets the `opencode.go` cap). Hands #5
  `separate-runtime-presentation-boundary` a clean harness layer that already
  cannot import presentation packages. Sets up the later parked
  `extract-prompt-builder` change, which lifts `BuildPrompt` out of `harnessapi`
  once prompt assembly grows more complex.
- **Out of scope**: adding a new harness; changing CLI flags passed to existing
  harnesses; changing failure-classification semantics or moving the per-harness
  `reliability` parsers; renaming `ResolvedAgent`; extracting the prompt builder
  into its own module (parked `extract-prompt-builder`); bumping the version or
  cutting a release.
