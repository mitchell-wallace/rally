# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects dependency
and risk-of-drift, not final scope. Last reviewed 2026-06-17.

## Done (archived)

- **harden-relay-run-lifecycle** (`2026-05-29`) — state integrity + freeze/retry/resume
  reliability. Owns the **stall** (liveness) / **frozen** (circuit breaker) /
  **benched** (scheduler route-entry out of rotation) vocabulary split.
- **tidy-rally-runtime-data-storage** (`2026-06-03`) — `.rally/state/`, `summary.jsonl`,
  Sentry-era opt-in telemetry sink, laps bundling.
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
- **enrich-failure-telemetry** (`2026-06-11`) — Sentry-era telemetry enrichment
  work. Do not schedule follow-up Sentry enrichment; New Relic migration owns
  provider-forward observability.

## Order

1. **migrate-telemetry-to-new-relic** _(full artifacts; 0.9.1 release gate)_
   Hard-cut release telemetry from Sentry to New Relic before 0.10.0. Carries
   forward useful observability concepts from the obsolete Sentry enrichment
   draft only where they fit New Relic: native panic/error capture, application
   logs, bounded custom events, and backend-neutral privacy scrubbing.

2. **release-0-10-0-reliability-and-model-routing** _(full artifacts)_
   Build the reliability/model-routing release on the New Relic/backend-neutral
   telemetry vocabulary established by 0.9.1, especially warning diagnostics for
   lap mismatches rather than Sentry issue semantics.

3. **improve-harness-consistency** _(draft)_
   Normalize harness adapters into one `Executor` contract: uniform final-text/summary
   extraction, tool-count, session ID, clean-completion-vs-process-exit detection, and a
   per-adapter conformance suite. Inherits the typed evidence model: this change moves
   `FailureEvidence` *population* from runner-side log parsing into the adapters.
   Motivated by opencode's headless `run --format json` issues. See
   `improve-harness-consistency/draft.md`.

4. **rename-rally-roles** _(author input captured; artifacts not drafted)_
   Rename routing roles from skill-hierarchy (JUNIOR/SENIOR/UI/VERIFY) to judgment
   framing (**builder**/**architect**/**designer**/**analyst**), builder as default.
   Needs a migration-vs-breaking decision. See `rename-rally-roles/laps-author-input-1.md`.

## Parked

- **build-new-tui** _(stub proposal)_ — future TUI plus a lighter start-of-run config /
  inflight steering flow (e.g. disabling a runner for one relay, the ergonomic successor
  to the invalid-model-name workaround #1 only classifies). Not scheduled.

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
