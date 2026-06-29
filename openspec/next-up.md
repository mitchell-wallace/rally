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

1. **decompose-relay-runner** _(proposed)_
   Architecture split for `internal/relay/runner.go` (3,782 lines) and the giant
   runner tests. Keep `package relay` and public behaviour stable while moving
   terminal, telemetry, action-loop, liveness, task, git, progress, final-snippet,
   and handoff-only helpers into responsibility-named files — and decompose the
   1,221-line `runOne` and 465-line `Run` into named steps (sequenced last). Adds
   the `relay-module-structure` spec that #3 will enforce.

2. **slim-cli-composition-root** _(draft)_
   Reduce `cmd/rally/main.go` and `internal/config/config_v2.go` from broad
   catch-all files into slim entry points and responsibility-named config/runtime
   modules. This creates a reusable relay-start composition seam for CLI and TUI.

3. **add-architecture-guardrails** _(draft)_
   Add file-size budgets, import-boundary checks, and dependency-confinement CI
   so future files cannot grow to runner.go scale. Roll out with grandfathered
   caps and ratchet them down as the refactors land.

4. **modularize-harness-adapters** _(draft)_
   Give future first-class harnesses a clean place to grow: a small executor API,
   one module per built-in harness, shared process/log support, and a registry
   that hides concrete adapter construction from command wiring.

5. **separate-runtime-presentation-boundary** _(draft)_
   Prepare for multiple presentation surfaces by introducing runtime events and
   operator-control boundaries. Keep relay orchestration presentation-neutral so
   CLI rendering and the future TUI do not couple to relay internals.

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

- **OpenSpec/laps coupling.** Rally core, the executor, and default role docs stay
  OpenSpec-agnostic; **laps** is the permanent backend. OpenSpec-specific tuning lives in
  `prepare-laps`, applied per-lap only when a lap has a related change. (Bounds #3's
  VERIFY-role item and #4's role docs.)
- **Resilience vocabulary** (from harden-relay-run-lifecycle): **stall** = liveness,
  **frozen** = circuit breaker (per harness+model), **benched** = scheduler entry out of
  rotation. Reuse these words downstream; `improve-error-categorisation` added
  reset-driven benching, and `release-0-10-0-reliability-and-model-routing` keeps
  tagging these states in backend-neutral/New Relic telemetry.
