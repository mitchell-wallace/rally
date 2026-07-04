# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects dependency
and risk-of-drift, not final scope. Last reviewed 2026-07-04.

## Done (archived)

- **harden-relay-run-lifecycle** (`2026-05-29`) — state integrity + freeze/retry/resume
  reliability. Owns the **stall** (liveness) / **frozen** (circuit breaker) /
  **benched** (scheduler route-entry out of rotation) vocabulary split.
- **tidy-rally-runtime-data-storage** (`2026-06-03`) — `.rally/state/`, `summary.jsonl`,
  opt-in telemetry sink, laps bundling.
- **rally-083-polish** (`2026-06-04`) — first CLI-polish pass: stall/slowing thresholds,
  inline `retry N/M`, final-snippet semantics.
- **git-hygiene** (`2026-06-08`) — auto-commit on init/hook-install, agent commit at lap
  boundary, state folding.
- **cli-polish** (`2026-06-08`) — display/config polish, activity-age bounding, collapsed
  retry display, terminal-only colouring, leftover-aware "incomplete".
- **agent-lifecycle** (`2026-06-08`) — graceful subprocess shutdown, pause/resume,
  shortcut renames, route/runner fallback docs, VERIFY-role boundary.
- **improve-error-categorisation** (`2026-06-11`) — typed failure taxonomy,
  evidence, and reset-driven usage-limit benching.
- **enrich-failure-telemetry** (`2026-06-11`) — obsolete telemetry enrichment
  work. Do not schedule follow-up telemetry enrichment; New Relic migration owns
  provider-forward observability.
- **migrate-telemetry-to-new-relic** (`2026-06-22`) — hard-cut release telemetry
  to New Relic before the 0.10.0 reliability/model-routing work.
- **release-0-10-0-reliability-and-model-routing** (`2026-06-22`) — reliability
  and model-routing release on the New Relic/backend-neutral telemetry vocabulary.
- **harden-ci-correctness-gates** (`2026-06-28`) — race, vet, gofmt,
  govulncheck, and mod-tidy CI gates.
- **improve-harness-consistency** (`2026-06-28`) — normalized harness evidence,
  session-log recovery, runner tags, and Gemini removal follow-up.
- **decompose-relay-runner** (`2026-06-30`) — extracted the
  `internal/relay/runner` package, carved the orchestrator into
  responsibility-named files, decomposed the large run/try loops into named
  steps, and added the `relay-module-structure` spec for the runner/relay
  boundary.
- **slim-cli-composition-root** (`2026-07-01`) — slimmed `cmd/rally/main.go` and
  split `internal/config/config_v2.go`; added the `internal/cli` command/prompt
  layer and the presentation-neutral `internal/app.StartRelay` seam (with
  `InspectResume` / `BuildExecutors`), resolving interactive start-of-run
  decisions CLI-side; broke the `release → app` metadata edge; added the
  `composition-root-structure` spec.
- **add-architecture-guardrails** (`2026-07-01`) — the `tools/archguard` checker
  (file-size budgets with grandfathered caps, import-boundary and
  dependency-confinement rules, test-helper confinement) wired into `just check`
  and the CI `lint` job. Enforces #1's one-way `runner → relay` edge and #2's
  composition-root edges; tooling-and-CI only (no runtime change, no version bump);
  added the `architecture-guardrails` spec.
- **modularize-harness-adapters** (`2026-07-02`) — introduced the
  `internal/harnessapi` contract package (`Executor`/`RunOptions`/`TryResult`/
  `ResolvedAgent` + shared `BuildPrompt`/reasoning helpers), moved each built-in
  harness into its own `internal/harness/<name>` deep module with shared
  `internal/harness/process` support, and exposed `harness.BuildExecutors`
  behind a thin `app.BuildExecutors` mapper; removed `internal/agent` (no shim).
  Added the `harness-module-structure` spec; ratcheted #3's `opencode.go` cap
  away and set up the parked `extract-prompt-builder`.
- **separate-runtime-presentation-boundary** (`2026-07-04`) — introduced a
  presentation-neutral runtime contract in
  `internal/relay/runner/runtimeevent` (typed data-only events, synchronous
  `Sink`, operator-control vocabulary + `ControlSource`) so the CLI and future
  TUI consume runner-emitted events instead of runner internals; carried the
  presentation adapters through the `app.StartRelay` seam. Added the
  `runtime-presentation-boundary` spec; modified `composition-root-structure`.
- **decompose-run-one** (`2026-07-04`) — split the runner orchestration core
  (`run_one.go`, `route_runtime.go`, `relay_steps.go`) into responsibility-named
  per-phase files behind the existing `runOne`/`routeRuntime`/relay-step index
  functions. Behaviour-preserving same-package file split; ratcheted #3's
  flagship `run_one.go` production cap. Modified `relay-module-structure`.
- **decompose-remaining-source-files** (`2026-07-04`) — deep-module split of the
  remaining production warning-band outliers (`monitor.go`,
  `config/providers.go` + `config_v2.go`, `cli/routes_check.go`, `store.go`)
  into responsibility-named files. Findability polish, all under #3's 800-line
  hard budget. Added the `support-module-structure` spec; modified
  `composition-root-structure`.
- **decompose-large-test-files** (`2026-07-04`) — split the oversized `_test.go`
  files along production file lines into responsibility-named test files with
  shared per-package helpers, clearing #3's 1,000-line test cap. Added the
  `test-module-structure` spec.

## Order

The runner is the spine of this sequence: #1 gave it its own package
(`internal/relay/runner`) and the one-way `runner → relay` boundary, #2 layered
the composition root (`cmd/rally → internal/cli → internal/app`) above it, and #3
added the `tools/archguard` guardrail that holds those edges one-way. The
modularization arc (#4–#8) then built on that structure rather than on a
monolithic runner — harness adapters, the presentation boundary, the run/try
loop, remaining source files, and test files — each ratcheting #3's budgets down
as it split its outliers (`opencode.go` 801, `run_one.go` 1,510). That arc is now
complete. What remains queued is the role rename (#9), which the TUI (#10) needs
as stable concepts to render.

9. **rename-rally-roles** _(author input captured; artifacts not drafted)_
   Rename routing roles from skill-hierarchy (JUNIOR/SENIOR/UI/VERIFY) to judgment
   framing (**builder**/**architect**/**designer**/**analyst**), builder as default.
   Needs a migration-vs-breaking decision. See `rename-rally-roles/laps-author-input-1.md`.

10. **build-new-tui** _(stub proposal)_
   Future TUI plus a lighter start-of-run config / inflight steering flow (e.g.
   disabling a runner for one relay, the ergonomic successor to the
   invalid-model-name workaround #1 only classifies). Sequence after the runtime
   presentation boundary and role rename so the TUI has stable concepts to render.

## Parked

- **adopt-lint-and-fuzz-gates** _(draft)_ — deeper lint/fuzz hardening after the
  architectural guardrail baseline is in place. Keep separate from the file-size
  and import-boundary policy so static-analysis backlog does not block the
  modularization sequence.
- **extract-prompt-builder** _(draft)_ — give prompt construction its own module,
  isolated from the harness executor contract (`BuildPrompt` moves to
  `internal/harnessapi` under #4; this later change lifts it out entirely). Not a
  priority while the builder stays simple; worth it once prompt-assembly logic
  grows more distinct concerns. Sequence after `rename-rally-roles` and
  `build-new-tui`.

## Carried-over principles

- **Runner/relay boundary** (from decompose-relay-runner): the relay orchestrator
  lives in `internal/relay/runner` and depends one-way on the `internal/relay`
  primitives (relay-record/resilience/mix). Downstream changes preserve the
  direction — `relay` must never import `runner` — and attach new structure
  (app-start seam #2, import guardrail #3, harness registry #4, presentation
  boundary #5, deep-module decompositions #6–#8) on top of that edge, not by
  re-monolithising the runner. Don't reintroduce a "same-package, defer the
  boundary" framing: extract when the dependency graph already supports it.
- **Deep-module decomposition** (from slim-cli-composition-root's config split):
  when a file grows past its budget, split it into responsibility-named files in
  the *same* package behind a shallow entry point (the type/constructor or the
  step-index function), so the directory listing answers "where does X live" and
  each file exposes a small surface over its own deeper body. Prefer a file split
  first; promote to a child package only when a clean interface has emerged.
  Applies to #6 (`run_one`), #7 (remaining source), and #8 (tests).
- **OpenSpec/laps coupling.** Rally core, the executor, and default role docs stay
  OpenSpec-agnostic; **laps** is the permanent backend. OpenSpec-specific tuning lives in
  `prepare-laps`, applied per-lap only when a lap has a related change. (Bounds #3's
  VERIFY-role item and #4's role docs.)
- **Resilience vocabulary** (from harden-relay-run-lifecycle): **stall** = liveness,
  **frozen** = circuit breaker (per harness+model), **benched** = scheduler entry out of
  rotation. Reuse these words downstream; `improve-error-categorisation` added
  reset-driven benching, and `release-0-10-0-reliability-and-model-routing` keeps
  tagging these states in backend-neutral/New Relic telemetry.
