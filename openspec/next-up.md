# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects dependency
and risk-of-drift, not final scope. Last reviewed 2026-06-29.

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

## Order

The runner is the spine of this sequence: #1 gives it its own package
(`internal/relay/runner`) and the one-way `runner → relay` boundary, and #2–#5
build on that edge rather than on a monolithic `runner.go`.

1. **decompose-relay-runner** _(proposed)_
   **Extract the `internal/relay/runner` package.** The orchestrator (`runner.go`
   + `route_runtime.go` + `log.go`, 3,782+ lines) moves into its own package
   depending one-way on the relay primitives (`relay.go`/`resilience.go`/`mix.go`/
   `constants.go`); only `cmd/rally` (two refs) changes. Then carve the
   orchestrator into responsibility-named files (terminal, telemetry, action-loop,
   liveness, task, git, progress, final-snippet, handoff-only) and decompose the
   1,221-line `runOne` and 465-line `Run` into named steps (Phase C, last). Adds
   the `relay-module-structure` spec — including the `runner → relay` edge — that
   #3 enforces.

2. **slim-cli-composition-root** _(draft)_
   Reduce `cmd/rally/main.go` and `internal/config/config_v2.go` from broad
   catch-all files into slim entry points and responsibility-named config modules.
   Introduces the `internal/app` relay-start seam that composes `runner.Runner`
   above config — the reusable start path CLI and the future TUI share. Moves
   command construction into `internal/cli` directly (no package-`main`-first
   deferral).

3. **add-architecture-guardrails** _(draft)_
   Add file-size budgets, import-boundary checks, and dependency-confinement CI
   so future files cannot grow to runner.go scale. Its flagship import rule is the
   `runner → relay` one-way edge #1 created (relay must not import runner). Roll
   out with grandfathered caps regenerated against the post-#1/#2 tree, and ratchet
   them down as refactors land.

4. **modularize-harness-adapters** _(draft)_
   Give future first-class harnesses a clean place to grow: a small executor API,
   one module per built-in harness (the deep-module move), shared process/log
   support, and a registry whose `map[string]agent.Executor` output feeds
   `runner.NewRunner` at the composition root.

5. **separate-runtime-presentation-boundary** _(draft)_
   Prepare for multiple presentation surfaces by introducing runtime events and
   operator-control boundaries. Attaches to the `terminal.go`/`action_loop.go`/
   `liveness.go` seams #1 already isolated inside `internal/relay/runner`, so the
   CLI and future TUI consume a presentation-neutral runtime instead of runner
   internals.

6. **rename-rally-roles** _(author input captured; artifacts not drafted)_
   Rename routing roles from skill-hierarchy (JUNIOR/SENIOR/UI/VERIFY) to judgment
   framing (**builder**/**architect**/**designer**/**analyst**), builder as default.
   Needs a migration-vs-breaking decision. See `rename-rally-roles/laps-author-input-1.md`.

7. **build-new-tui** _(stub proposal)_
   Future TUI plus a lighter start-of-run config / inflight steering flow (e.g.
   disabling a runner for one relay, the ergonomic successor to the
   invalid-model-name workaround #1 only classifies). Sequence after the runtime
   presentation boundary and role rename so the TUI has stable concepts to render.

## Parked

- **adopt-lint-and-fuzz-gates** _(draft)_ — deeper lint/fuzz hardening after the
  architectural guardrail baseline is in place. Keep separate from the file-size
  and import-boundary policy so static-analysis backlog does not block the
  modularization sequence.

## Carried-over principles

- **Runner/relay boundary** (from decompose-relay-runner): the relay orchestrator
  lives in `internal/relay/runner` and depends one-way on the `internal/relay`
  primitives (relay-record/resilience/mix). Downstream changes preserve the
  direction — `relay` must never import `runner` — and attach new structure
  (app-start seam #2, import guardrail #3, harness registry #4, presentation
  boundary #5) on top of that edge, not by re-monolithising the runner. Don't
  reintroduce a "same-package, defer the boundary" framing: extract when the
  dependency graph already supports it.
- **OpenSpec/laps coupling.** Rally core, the executor, and default role docs stay
  OpenSpec-agnostic; **laps** is the permanent backend. OpenSpec-specific tuning lives in
  `prepare-laps`, applied per-lap only when a lap has a related change. (Bounds #3's
  VERIFY-role item and #4's role docs.)
- **Resilience vocabulary** (from harden-relay-run-lifecycle): **stall** = liveness,
  **frozen** = circuit breaker (per harness+model), **benched** = scheduler entry out of
  rotation. Reuse these words downstream; `improve-error-categorisation` added
  reset-driven benching, and `release-0-10-0-reliability-and-model-routing` keeps
  tagging these states in backend-neutral/New Relic telemetry.
