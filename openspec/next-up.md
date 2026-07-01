# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects dependency
and risk-of-drift, not final scope. Last reviewed 2026-07-01.

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

## Order

The runner is the spine of this sequence: #1 gave it its own package
(`internal/relay/runner`) and the one-way `runner → relay` boundary, and #2
layered the composition root (`cmd/rally → internal/cli → internal/app`) above
it. The queued changes #3–#8 build on those edges rather than on a monolithic
runner: a guardrail (#3) that holds the boundaries, then the deep-module
decompositions (#4 harness adapters, #5 presentation boundary, #6 run/try loop,
#7 remaining source files, #8 test files).

3. **add-architecture-guardrails** _(implemented; pending archive)_
   Add the `tools/archguard` checker (file-size budgets with grandfathered caps,
   import-boundary rules, dependency confinement, test-helper confinement) plus a
   `just arch-check` recipe folded into `just check` and an `archguard` step in the
   CI `lint` job. Tooling-and-CI only — no runtime change, no version bump, no
   release; stdlib-only so no new dependency. Flagship import rules are #1's
   one-way `runner → relay` edge (relay must not import runner) and #2's
   composition-root edges (`release ↛ app`; `app ↛ {cli, user_prompt, laps}`;
   nothing imports `cli` but `cmd/rally`). Baselined against the current tree
   (2026-07-01) so the gate enforces from day one with a green baseline; warnings
   (500/700) stay advisory while hard budgets (800/1,000) and grandfather caps
   ratchet down as #4+ split the remaining outliers (e.g. `opencode.go` 801,
   `run_one.go` 1,510). Note: #4/#5 only own the harness + presentation seams; the
   runner orchestration core (`run_one.go` + its tests), `config`, `store`, and
   `monitor` outliers have no owning change yet and need follow-up splits. Adds the
   `architecture-guardrails` spec.

4. **modularize-harness-adapters** _(proposed)_
   Give future first-class harnesses a clean place to grow (draft **Option B**,
   chosen for cleaner terminology over churn): a dedicated `internal/harnessapi`
   contract package (`Executor`/`RunOptions`/`TryResult`/`ResolvedAgent` + shared
   `BuildPrompt`/reasoning helpers), one deep module per built-in harness under
   `internal/harness/<name>` (each owning its CLI parsing and log recovery), a
   shared `internal/harness/process` support package, and a top-level
   `internal/harness.BuildExecutors(harness.Config)` registry (narrow,
   config-decoupled input) whose `map[string]harnessapi.Executor` output feeds
   `runner.NewRunner` via a thin `app.BuildExecutors` mapper. `internal/agent` is
   removed (no shim). Behaviour-preserving except six same-package helpers that
   become exported to cross the new boundaries. Consumes #3's guardrail (adds the
   harness allow-lists, ratchets away the `opencode.go` 801 cap) and keeps the
   `reliability` parsers in place. Adds the `harness-module-structure` spec; hands
   #5 a harness layer that already cannot import presentation packages; sets up the
   parked `extract-prompt-builder`.

5. **separate-runtime-presentation-boundary** _(draft)_
   Prepare for multiple presentation surfaces by introducing runtime events and
   operator-control boundaries. Attaches to the `terminal.go`/`action_loop.go`/
   `liveness.go` seams #1 already isolated inside `internal/relay/runner`, so the
   CLI and future TUI consume a presentation-neutral runtime instead of runner
   internals.

6. **decompose-run-one** _(draft)_
   Split the runner orchestration core (`run_one.go` 1,510 + `route_runtime.go`
   752 + `relay_steps.go` 526) into responsibility-named files behind the existing
   `runOne` index, and break up its two deepest phase bodies (classify/record).
   Same-package file split like #2 did to config; behaviour-preserving, no API
   change. Ratchets #3's flagship production cap (`run_one.go`) down. Sequence
   after #5 so it splits orchestration-only code.

7. **decompose-remaining-source-files** _(draft)_
   Deep-module split of the production warning-band outliers no other change owns:
   `monitor.go` (663), `config/providers.go` (621), `cli/routes_check.go` (619),
   `store.go` (541). All under #3's 800-line hard budget, so this is findability
   polish, not gate-clearing. Same-package file splits, behaviour-preserving.

8. **decompose-large-test-files** _(draft)_
   Split the nine `_test.go` files over #3's 1,000-line cap into responsibility-
   named test files that mirror the post-#4/#6/#7 source layout, with shared setup
   in per-package helper files. Pure test reorganization. Coordinates with #4/#6
   (which may split their own tests) and directly owns the stable-package tests
   (`config_v2_test`, `store_test`, `resilience_test`). Land last of the
   decompositions; ratchets #3's test caps away.

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
