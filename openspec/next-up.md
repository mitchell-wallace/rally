# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects dependency
and risk-of-drift, not final scope. Last reviewed 2026-06-09.

## Done (archived)

- **harden-relay-run-lifecycle** (`2026-05-29`) — state integrity + freeze/retry/resume
  reliability. Owns the **stall** (liveness) / **frozen** (circuit breaker) /
  **benched** (scheduler route-entry out of rotation) vocabulary split.
- **tidy-rally-runtime-data-storage** (`2026-06-03`) — `.rally/state/`, `summary.jsonl`,
  opt-in Sentry sink, laps bundling.
- **rally-083-polish** (`2026-06-04`) — first CLI-polish pass: stall/slowing thresholds,
  inline `retry N/M`, final-snippet semantics.
- **git-hygiene** (`2026-06-08`) — auto-commit on init/hook-install, agent commit at lap
  boundary, state folding.
- **cli-polish** (`2026-06-08`) — display/config polish, activity-age bounding, collapsed
  retry display, terminal-only colouring, leftover-aware "incomplete".
- **agent-lifecycle** (`2026-06-08`) — graceful subprocess shutdown, pause/resume,
  shortcut renames, route/runner fallback docs, VERIFY-role boundary.

## Order

1. **improve-error-categorisation** _(draft; decisions captured 2026-06-09)_
   Make failure handling cleaner, more consistent, and more understandable.
   Replace the overloaded `rate limit` bucket with a real taxonomy
   (`usage_limit` / `short_rate_limit` / `provider_overloaded` / `invalid_model` /
   `auth_or_proxy` / `harness_launch` / `incomplete_finalization` / `agent_error`),
   reorder classification so provider/config/quota evidence beats `incomplete`,
   make patterns harness-scoped (no more Codex labelled as a Claude rate limit),
   and carry a typed `FailureEvidence` on `TryResult`. Usage limits **bench** the
   affected quota scope until reset instead of looping one-minute waits. See
   `improve-error-categorisation/draft.md`.

2. **enrich-failure-telemetry** _(full artifacts; realigned 2026-06-09 onto #1's baseline)_
   Enriches the existing Sentry sink (not a new integration): run-environment context,
   an anonymous machine-local hash, a globally-unique relay identity
   (`<machine-hash>-<date>-<relay_id>`), username-stripped cwd, and a failure-time
   agent-state snapshot. Consumes #1's typed `FailureCategory` + quota-scope + reset
   evidence and the **benched** state rather than re-deriving its own failure class.

3. **improve-harness-consistency** _(draft)_
   Normalize harness adapters into one `Executor` contract: uniform final-text/summary
   extraction, tool-count, session ID, clean-completion-vs-process-exit detection, and a
   per-adapter conformance suite. Inherits #1's typed evidence: this change moves
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
  rotation. Reuse these words downstream; #1 adds reset-driven benching, #2 tags them.
