# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects dependency
and risk-of-drift, not final scope. Last reviewed 2026-07-05.

Prune rule: the archive folder and this Done list keep only the ~5 newest
archived changes; older ones live in git history (`git log -- openspec/changes/archive`).

## Done (archived)

- **improve-harness-consistency** (`2026-07-05`, shipped in 0.12.0) — cut the
  deprecated `gemini` CLI harness (antigravity inherits the model family),
  populated `failure_evidence` on every classified `RallyFailure` (dirty-tree,
  text-pattern, unmatched paths), added codex session-log and opencode
  disk-log evidence fallbacks, resolved-model `runner` tags, and moved routing
  events out of `RallyTry`. Spec deltas were synced before archive; its
  evidence-corpus role passes to `harden-outing-failure-handling`.
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
complete. Roles v2 (#9) landed to dev 2026-07-05, giving the TUI (#10) stable
concepts to render.

9. **rename-rally-roles** _(implemented; landed to dev 2026-07-05, archive
   after a settling period)_ — roles v2: the eight-role set
   (intern/junior/senior ladder + architect/review/verify/qa/recovery), `ui`
   retired as a built-in, legacy migration both ways. Baseline:
   `rename-rally-roles/roles-v2-design.md`.

9.5. **harden-outing-failure-handling** _(draft; evidence captured 2026-07-05)_
   Phase-B reliability fixes scoped by the crew-chief campaign: unauthenticated
   harness detection + provider sidelining (antigravity first), retry/recovery
   fallback when the same lap burns tries without progress (including the
   retry-of-already-done-lap bug), and codex misclassification that pauses it
   for work it completed. Evidence and code leads in its `draft.md`.

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
