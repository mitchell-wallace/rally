## Why

`internal/relay/runner.go` is **3,782 lines** and its primary test file
`internal/relay/runner_test.go` is **~6,915 lines**. One file mixes relay
startup/resume/completion, route selection and fallback, run/try execution,
operator keyboard actions and countdown rendering, liveness/monitor wiring,
telemetry assembly, git commit and state-folding, laps claim/finalization
validation, bounded handoff-only continuation, final-snippet normalization, and a
long tail of pure formatting and aggregation helpers. No maintainer or agent can
hold that in one pass.

Two functions dominate the mass: `runOne` is **~1,221 lines** and `Run` is
**~465 lines**. A 1,200-line function is a standing hazard, not just an
aesthetic one: the next changes in the architecture sequence
(`modularize-harness-adapters` #4, `separate-runtime-presentation-boundary` #5)
have to read the whole run lifecycle to touch any part of it. We pay that cost
down now, while the surrounding behaviour is fully pinned by the existing test
suite, rather than after later changes wrap new behaviour around it.

This is change **#1** in the sequence (`openspec/next-up.md`). It is deliberately
the *seam-exposure* step: it stays inside `package relay`, changes no exported
API and no behaviour, and pays no subpackage/interface tax. Its job is to make
the responsibility seams **visible and single-purpose** so the later changes can
lift them into genuinely deep modules — and so `add-architecture-guardrails` (#3)
inherits a small `runner.go` and can set its grandfathered file-size caps low.

## What Changes

- **Preserve the package and public surface.** Everything stays in `package
  relay`. No new subpackages. The exported surface is unchanged: `Config`,
  `Runner`, `NewRunner`, `Runner.Run`, `Runner.SetTelemetry`,
  `Runner.RequestStop`, `Resilience`/`NewResilience`/`ResilienceKey`,
  `AgentMix`/`ParseAgentMix`, `Resolver`, `CancellationSource`, `FormatMixLabel`,
  `CreateRelay`/`ResumeRelay`/`CompleteRelay`, the `AgentState`/`State*`
  constants, and exported test helpers — all keep their names and signatures.

- **Carve `runner.go` into responsibility-named files**, using bare names
  consistent with the package's existing siblings (`resilience.go`,
  `route_runtime.go`, `mix.go`) and a `runner_` qualifier *only* where a bare
  name would collide with an imported package. New files: `terminal.go`,
  `failure_display.go`, `runner_telemetry.go`, `task.go`, `git.go`,
  `final_snippet.go`, `runner_progress.go`, `action_loop.go`, `liveness.go`,
  `handoff_only.go`, plus the lifecycle-spine files `run_one.go` and
  `relay_steps.go`. Two stragglers move to existing homes: `logf` → `log.go`,
  `prepareExecutorForSelection` → `route_runtime.go`. Every current symbol gets
  exactly one home (see design.md's file manifest).

- **Decompose the two big lifecycle functions** into named private step-methods,
  preserving logic block-for-block: `Run` delegates to relay-iteration steps in
  `relay_steps.go`; `runOne` delegates to run-level steps in `run_one.go`. After
  this, `runner.go` is a thin top-level orchestrator (~250–400 lines) that
  *explains* the relay flow rather than implementing all of it.

- **Re-shard `runner_test.go`** along the same seams (`terminal_test.go`,
  `task_test.go`, `git_test.go`, `runner_progress_test.go`, `handoff_only_test.go`,
  etc.), keeping one small shared fixtures file. No new behaviour assertions; only
  relocation.

- **Codify what must stay the same.** Add a `relay-module-structure` capability
  spec recording the preserved public surface, the behaviour/telemetry/persistence
  preservation contract for this refactor, and the one-responsibility-per-file
  invariant whose CI enforcement is handed to `add-architecture-guardrails` (#3).

This change is **structure-only**: no runtime behaviour, CLI output, telemetry
field, persisted-store, laps, or git-message change. Therefore **no version bump
and no release**, and it can land independently of feature work.

## Capabilities

### New Capabilities

- `relay-module-structure`: the preserved exported surface of `internal/relay`,
  the behaviour/telemetry/persistence-preservation contract this refactor holds
  itself to, and the responsibility-per-file decomposition invariant for the
  package (enforcement mechanism owned by `add-architecture-guardrails`).

### Modified Capabilities

<!-- None. This change relocates code within one package and changes no runtime
     behaviour requirement, so no existing capability spec is modified. -->

## Impact

- **Code** (`internal/relay/` only): `runner.go` split into ~12 responsibility
  files plus two relocations; `runOne` and `Run` decomposed into named private
  methods (pure relocation of logic). No other package is touched and there is
  **no import churn** — everything remains `package relay`.
- **Tests** (`internal/relay/`): `runner_test.go` re-sharded to mirror the new
  files; no assertion changes. The suite must stay green, including
  `go test -race -shuffle=on`.
- **Public surface**: unchanged, and verified (`go build ./...` of all callers;
  exported-identifier set diffed before/after).
- **Sequencing**: lands first; leaves `runner.go` small so #3's grandfathered
  caps start low; exposes the operator-control (`action_loop.go`), liveness, run,
  and telemetry seams that #5 (and #4) will lift into boundaries; keeps the laps
  coupling confined to `task.go`/`runner_progress.go` per the OpenSpec/laps
  carried-over principle.
- **Out of scope**: subpackage extraction, any new interface/abstraction layer,
  CI file-size/import-boundary budgets (owned by `add-architecture-guardrails`),
  TUI integration, new harnesses or roles, and any behaviour change to retry,
  routing, laps, git, telemetry, or terminal output.
