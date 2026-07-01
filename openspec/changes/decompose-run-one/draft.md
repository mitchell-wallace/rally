## Draft: Decompose the runner run/try loop

Status: drafted 2026-07-01. Behaviour-preserving refactor of the
`internal/relay/runner` orchestration core. No runtime, telemetry, store, or CLI
behaviour change; no version bump, no release.

## Why

`decompose-relay-runner` (#1) gave the runner its own package and named the
run/try steps as methods, and `slim-cli-composition-root` (#2) drained the entry
points. What #1 did *not* finish is the run/try loop itself: the phase methods it
named all still live in one file. A 2026-07-01 snapshot (regenerate at
implementation time — these numbers will drift):

- `internal/relay/runner/run_one.go` — 1,510 lines,
- `internal/relay/runner/route_runtime.go` — 752 lines,
- `internal/relay/runner/relay_steps.go` — 526 lines.

`run_one.go` is the single largest file left in the tree. Its entry point
(`Runner.runOne`) already reads as a clean table of contents — it delegates to
`prepareRunAttempt` → `runMonitoredAttempt` → `reconcileAttemptProgress` →
`classifyAttemptOutcome` → `recordAttemptOutcome` → `decideRetryOrComplete` and
friends — but every one of those bodies is inlined in the same file, and two of
them (`classifyAttemptOutcome` ~230 lines, `recordAttemptOutcome` ~260 lines) are
themselves too deep to scan. An agent asked "how does Rally decide to retry vs
route-fallback?" must page through the whole run/try loop to find it. This is the
`add-architecture-guardrails` (#3) flagship production outlier
(`run_one.go` = 1,510) whose grandfather cap this change ratchets down.

## Philosophy: deep modules, clean entry points, progressive disclosure

This change is guided by deep-module design, not line-count chasing:

- **The entry point is the index.** `runOne` (and the relay-level `Run`) should
  stay a short, linear reader of named phases — the first thing an exploring
  agent lands on, telling it *what happens in what order* without *how*.
- **Each phase is a deep module behind a shallow name.** A phase like
  "classify the try outcome" should be one clearly named unit an agent opens only
  when it cares about classification; its internal taxonomy/telemetry complexity
  stays hidden from the loop.
- **Progressive disclosure at the file level.** Splitting one 1,510-line file
  into per-phase files means the package directory *is* a map: the file names
  answer "where does X live" before any file is opened. This mirrors exactly what
  #2 did to `config_v2.go` (same package, responsibility-named files, no API
  change).

## Intent

- Split `run_one.go` into responsibility-named files in the **same** `package
  runner`, one per run/try phase (e.g. prepare / monitor / reconcile / classify /
  record / retry-decide / handoff-continuation / finalize), with `run_one.go`
  keeping only `runOne` + the small state constructors as the index.
- Shrink the two deep phase bodies (`classifyAttemptOutcome`,
  `recordAttemptOutcome`) by extracting their inner sub-steps into named helpers
  so each phase file is itself scannable, not just relocated bulk.
- Give `route_runtime.go` the same treatment: split construction variants,
  selection/`next`, recovery-signal sync, and bench/probation into named files
  behind the `routeRuntime` type.
- Consider the same for `relay_steps.go` (relay-level start/resume, route-wait,
  progress, summary) if it stays over the 500-line warning after the above.

## Candidate work (grounded 2026-07-01; verify at implementation)

- `run_one.go` → e.g. `run_one.go` (entry + state), `run_attempt_prepare.go`,
  `run_attempt_monitor.go`, `run_attempt_reconcile.go`, `run_attempt_classify.go`,
  `run_attempt_record.go`, `run_retry_decide.go`, `run_handoff.go`.
- `route_runtime.go` → e.g. `route_runtime.go` (type + accessors),
  `route_runtime_construct.go`, `route_runtime_select.go`,
  `route_runtime_recovery.go`, `route_runtime_bench.go`.
- Move each function verbatim; no signature, error-string, telemetry-field, or
  control-flow change. Where a deep body is broken up, the extracted helpers are
  unexported and package-local.

## Sequencing

- Sequence **after** `separate-runtime-presentation-boundary` (#5): #5 pulls the
  presentation/event weight out of the runner first, so this change splits
  orchestration-only code and the phase boundaries are cleaner.
- Runs alongside or ahead of `decompose-large-test-files`: the runner test files
  (`run_one_test.go`, `relay_steps_test.go`, `route_runtime_test.go`,
  `runner_outcome_test.go`, `runner_failure_telemetry_test.go`) should be split to
  mirror the new production file boundaries; that split is owned by the tests
  change but must track this one's structure.

## Testing / behaviour preservation

- File-move only: the exported `runner` API (`Config`, `Runner`, `NewRunner`,
  `Runner.Run`, …) is unchanged; `relay-module-structure` (#1) and
  `composition-root-structure` (#2) import edges are preserved.
- `go test -count=1 ./internal/relay/...` and `go test -race -shuffle=on -count=1
  ./internal/relay/...` stay green with only test relocations; no new behavioural
  assertion required.
- After landing, ratchet the `run_one.go` (and any) grandfather cap in
  `add-architecture-guardrails` down to the new sizes (or remove it).

## Open questions

- Is a same-package file split enough, or does the run/try attempt logic warrant a
  child package (e.g. `internal/relay/runner/attempt`)? Prefer the file split
  first (lower risk, no import edges to design); promote to a subpackage only if a
  clean interface emerges.
- Should `relay_steps.go` (526, just over warning) be split now or left as an
  advisory warning until it grows?

## Out of scope

- Any runtime/behaviour/telemetry/store/CLI change (this is structure-only).
- Harness-adapter files (`modularize-harness-adapters` #4) and the non-runner
  source outliers (`decompose-remaining-source-files` #7).
- Test-file decomposition (`decompose-large-test-files` #8), beyond keeping tests
  green.
