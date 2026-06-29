## Why

`internal/relay/runner.go` is **3,782 lines** and its primary test file
`internal/relay/runner_test.go` is **~6,915 lines**. One file mixes relay
startup/resume/completion, route selection and fallback, run/try execution,
operator keyboard actions and countdown rendering, liveness/monitor wiring,
telemetry assembly, git commit and state-folding, laps claim/finalization
validation, bounded handoff-only continuation, final-snippet normalization, and a
long tail of pure formatting helpers. Two functions dominate: `runOne` is
**~1,221 lines** and `Run` is **~465 lines** — a standing hazard the later
changes would otherwise have to read in full to touch at all.

The runner is the single largest body of work in the codebase and is going to
keep attracting work (operator-control boundary, executor API, runtime events).
It deserves its **own package**, not a corner of `internal/relay`. The original
draft's "keep `package relay`, defer extraction until boundaries are obvious"
line was a preemptive constraint, not a real one: the dependency graph shows the
boundaries are *already* obvious. The five primitive files (`relay.go`,
`resilience.go`, `mix.go`, `constants.go`, `log.go`'s consumers) reach into
nothing orchestrator-side; `route_runtime.go` couples to the orchestrator through
exactly one thread (`runTask`); and only `cmd/rally/main.go` imports the package
externally — with just two references (`relay.NewRunner`, `relay.Config`) that
move. Deferring the split would only move every file twice.

This is change **#1** in the architecture sequence (`openspec/next-up.md`). It
establishes the `internal/relay/runner` package, gives the runner an internal
shape, and creates the one-way `runner → relay` boundary that the rest of the
sequence orbits: `add-architecture-guardrails` (#3) gets its first real internal
edge to enforce, `slim-cli-composition-root` (#2) composes a `runner.Runner`
above a clean app seam, and `separate-runtime-presentation-boundary` (#5) attaches
its event/control boundary to the already-isolated `terminal.go` / `action_loop.go`
/ `liveness.go` files.

## What Changes

The change runs in three internal phases (skeleton + moves first, decomposition
last) so risk only rises after the mechanical work is green.

**Phase A — establish the package (mechanical):**

- Create `internal/relay/runner` (`package runner`). Move `runner.go`,
  `route_runtime.go`, and `log.go` into it wholesale. Relocate the only exported
  symbol in `route_runtime.go`, `FormatMixLabel`, *down* into `mix.go` so it stays
  `relay.FormatMixLabel` (a mix-formatting primitive).
- The primitives stay in `internal/relay`: `relay.go` (relay-record lifecycle),
  `resilience.go` (the freeze/bench/pause state machine), `mix.go`
  (`AgentMix`/`ParseAgentMix`/`Resolver`/`FormatMixLabel`), `constants.go`. The
  resulting dependency is one-way: `runner → relay`.
- Update callers: `relay.NewRunner` → `runner.NewRunner` and `relay.Config` →
  `runner.Config` in `cmd/rally/main.go` (+ two test files). Everything else
  (`relay.CompleteRelay`, `relay.FormatMixLabel`, `relay.NewResilience`,
  `relay.AgentMix`, …) is unchanged. The compiler enforces correctness.

**Phase B — carve the runner into responsibility files** (all inside
`internal/relay/runner`, born in their final home — no double-move): `terminal.go`,
`failure_display.go`, `telemetry.go`, `task.go`, `git.go`, `final_snippet.go`,
`progress.go`, `action_loop.go`, `liveness.go`, `handoff_only.go`, plus the
lifecycle-spine `run_one.go` and `relay_steps.go`. `logf` joins `log.go`;
`prepareExecutorForSelection` joins `route_runtime.go`. Every current symbol gets
exactly one home (see design.md's file manifest). Because the package is now
`runner`, **all** files use bare responsibility names (`telemetry.go`,
`progress.go`, …): a filename never collides with an imported package, so the
`runner_` qualifier the same-package draft needed is dropped entirely.

**Phase C — decompose the two big lifecycle functions** (highest risk, last):
`Run` delegates to relay-iteration steps in `relay_steps.go`; `runOne` delegates
to run-level steps in `run_one.go`. Block-for-block extraction, `-race` after each.
`runner.go` ends as a thin top-level orchestrator (~250–400 lines).

**Tests & spec:** re-shard `runner_test.go` along the new files (one small shared
fixtures file). Add a `relay-module-structure` capability spec codifying the
`runner → relay` boundary, the behaviour/telemetry/persistence-preservation
contract, and the one-responsibility-per-file invariant (CI enforcement handed to
`add-architecture-guardrails` #3).

This change is **structure-only**: no runtime behaviour, CLI output, telemetry
field, persisted-store, laps, or git-message change. **No version bump, no
release.**

## Capabilities

### New Capabilities

- `relay-module-structure`: the `internal/relay/runner` package and its one-way
  dependency on `internal/relay`; the exported-API relocation contract
  (`runner.Runner`/`runner.Config`/`runner.NewRunner` vs the primitives that stay
  `relay.*`); the behaviour/telemetry/persistence-preservation contract; and the
  responsibility-per-file decomposition invariant (enforcement mechanism owned by
  `add-architecture-guardrails`).

### Modified Capabilities

<!-- None. This change relocates code and changes no runtime behaviour
     requirement, so no existing capability spec is modified. -->

## Impact

- **Code**: new `internal/relay/runner` package; `runner.go` (+`route_runtime.go`,
  `log.go`) move into it and are carved into ~12 responsibility files; `runOne`
  and `Run` decomposed into named private methods; `FormatMixLabel` relocated to
  `mix.go`. `internal/relay` shrinks to the primitive set.
- **Callers**: `cmd/rally/main.go` (+ `main_test.go`, `telemetry_test.go`) switch
  two references to `runner.*` and add the import. No other package changes.
- **Public surface**: relocated, not redesigned — `Runner`/`Config`/`NewRunner`/
  `CancellationSource` move to `package runner` with unchanged signatures; the
  `Resilience`/`AgentMix`/`CreateRelay`/`FormatMixLabel`/… surface stays in
  `package relay`. Verified by `go build ./...`.
- **Sequencing**: lands first; creates the `runner → relay` edge #3 enforces;
  isolates the operator-control (`action_loop.go`), liveness, and terminal seams
  #5 builds on; keeps the laps coupling confined to `task.go`/`progress.go`.
- **Out of scope**: any further package extraction beyond `runner` (e.g. pushing
  presentation or harness adapters out — owned by #4/#5), CI budgets (owned by
  #3), TUI, new harnesses/roles, and any behaviour change.
