## Why

`internal/relay/runner/run_one.go` (1,510 lines) is the largest production file
left in the tree and the flagship grandfathered outlier in the
`add-architecture-guardrails` (#3) baseline. `decompose-relay-runner` (#1) named
the run/try phases as methods, but every phase body still lives in that one file
— an agent asking "how does Rally decide to retry vs route-fallback?" must page
through the whole loop. A 2026-07-02 scan (commit `f55712c`, after
**#4 modularize-harness-adapters**) confirms the draft's numbers are current:

- `internal/relay/runner/run_one.go` — 1,510 lines (grandfathered at 1,510),
- `internal/relay/runner/route_runtime.go` — 752 lines (warning band),
- `internal/relay/runner/relay_steps.go` — 526 lines (warning band),

with two phase bodies too deep to scan even once relocated:
`classifyAttemptOutcome` (~229 lines) and `recordAttemptOutcome` (~261 lines).

This is change **#6** in the architecture sequence (`openspec/next-up.md`). It is
a **behaviour-preserving refactor**: file splits and unexported helper
extraction inside `package runner`, no runtime, telemetry, store, or CLI
behaviour change, no version bump, no release.

## What Changes

- Split `run_one.go` into responsibility-named files in the **same**
  `package runner`, one per run/try phase (prepare / monitor / reconcile /
  classify / record / retry-decide / handoff-continuation / finalize), keeping
  `run_one.go` as the index: `Runner.runOne`, the `runOneState` constructors,
  and nothing else.
- Shrink the two deep phase bodies (`classifyAttemptOutcome`,
  `recordAttemptOutcome`) by extracting named unexported sub-step helpers so
  each phase file is scannable, not just relocated bulk.
- Give `route_runtime.go` the same split behind the `routeRuntime` type:
  construction variants, selection (`next`) and wait, recovery-signal sync,
  bench/probation, and entry resolution into named files.
- Split `relay_steps.go` by its relay-level concerns (start/resume + spans,
  route-wait, run-progress + resilience application, summary/tally) while it is
  open in the same change.
- Ratchet the `add-architecture-guardrails` grandfather baseline: regenerate the
  map so the `run_one.go` (1,510) production entry is gone and no new
  `internal/relay/runner` production file needs an entry.
- No exported API change: `runner.Config`, `Runner`, `NewRunner`, `Runner.Run`
  and the rest of the `relay-module-structure` surface are untouched.

## Capabilities

### New Capabilities

_None — this extends the existing runner-structure capability rather than
introducing a new one._

### Modified Capabilities

- `relay-module-structure`: adds a requirement that the run/try orchestration
  core is decomposed into responsibility-named per-phase files behind short
  index functions (`runOne`, the `routeRuntime` constructors), with no
  phase body deep enough to hide its sub-steps, and extends the
  behaviour-preservation contract to this second decomposition pass.

## Impact

- **Code**: `internal/relay/runner` only — `run_one.go`, `route_runtime.go`,
  and `relay_steps.go` are split into responsibility-named files in the same
  package; functions move verbatim except the named sub-step extraction inside
  the two deep phase bodies (extracted helpers are unexported and
  package-local). No signature, error-string, telemetry-field, or control-flow
  change.
- **Behaviour**: none. Runtime behaviour, CLI output, telemetry events/fields,
  store shape, laps semantics, and agent-authored commit messages are unchanged;
  `internal/buildinfo/VERSION` untouched; no release.
- **Guardrails**: `tools/archguard` grandfather map regenerated — the flagship
  `run_one.go` production cap is removed; the runner test-file caps stay (owned
  by #8 `decompose-large-test-files`).
- **Tests**: `go test -count=1 ./internal/relay/...` and
  `go test -race -shuffle=on -count=1 ./internal/relay/...` stay green with no
  test edits required by this change; the mirroring split of the runner test
  files is owned by #8 and must track the file boundaries this change creates.
- **Sequencing**: after `separate-runtime-presentation-boundary` (#5), so the
  phase files this change carves are orchestration-plus-events rather than
  orchestration-plus-rendering: #5 removes the runner's `style`/`keyboard`
  rendering and its direct operator-facing stdout/stderr writes, but the
  monitor status-line wiring (`mon.Start(os.Stdout)`, indicator setters)
  deliberately **stays runner-driven** as #5's documented residual. Re-ground
  line counts and function inventories at implementation time; #5 will have
  moved rendering code out of the files this change splits.
- **Out of scope**: any behaviour/telemetry/store/CLI change; harness files
  (#4, landed); non-runner source outliers (#7); test-file decomposition (#8);
  promoting the attempt logic to a child package (file split only, per the
  carried-over deep-module principle).
