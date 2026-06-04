# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects dependency and
risk-of-drift, not final scope. Last reviewed 2026-06-04.

## Done (cleared from the active queue)

- **harden-relay-run-lifecycle** — archived `2026-05-29`. State integrity (lap-ID
  pinning, file-change cross-check, role-aware stall-recovery) + freeze/retry/resume
  reliability (freeze decay, `--new` reset, infra-only failure classification, hourly
  retries). Owns the freeze→**stall**→**benched** vocabulary split.
- **tidy-rally-runtime-data-storage** — archived `2026-06-03`. `.rally/state/`
  relocation, `summary.jsonl`, opt-in Sentry sink, prompt-size logging, laps bundling.
- **rally-083-polish** — archived `2026-06-04`. The first CLI-polish pass: softened
  the stall/slowing thresholds, consolidated failures/retries into the inline
  `retry N/M` field + run-once tally, and normalized final-snippet semantics.
- **git-hygiene** — implemented (branch `git-hygiene`, `✓ Complete`), awaiting
  archive. Auto-commit on init/hook-install, agent commit at lap boundary, and
  folding `summary.jsonl` into the work commit (no standalone `rally: update state`).

## Order

1. **cli-polish** _(active, full artifacts; `openspec validate --strict` passes)_
   Display + config polish, now expanded with four more rough edges surfaced in
   v0.8.3:
   - Display: width-aware single-line shortcut hint, left-aligned hints, full-width
     headers, and the `FallbackConfig`→`FreeRunPrompt` rename (config/naming clarity).
   - **Activity age bounded by try runtime** — `last activity` no longer reports a
     stale log mtime (`20h 50m ago`) at a retry's start, and `⚠ slowing` can't fire
     in a try's first seconds.
   - **Collapsed retry display** — one updating `↻ retrying N/M` line + a single
     coloured outcome footer, instead of N red `✗ failed` footers.
   - **Terminal-outcome-only colouring** — `✗ failed` is red only on the final
     (terminal) failure; interim retries render neutral.
   - **Leftover-aware "incomplete"** _(relay-runner MODIFIED)_ — "file changes
     without finalization" is computed from changes produced by *this* try, so
     uncommitted leftovers from a prior failed try no longer trigger it.
   Coordination: `style.ShortcutHint()` is also edited by #2 (label renames) —
   co-implement/sequence.

2. **agent-lifecycle** _(full artifacts; `openspec validate --strict` passes)_
   Graceful subprocess shutdown (SIGINT + `WaitDelay`), pause-now + session resume
   (`--resume <session>`), shortcut-label renames ("graceful stop" / "quit now"), and
   the routed QA items R9 (route/runner fallback — docs/defaults + a no-fallback-lane
   warning, leaning on #harden's failure classification) and R12/R13 (VERIFY role
   boundary stays OpenSpec-agnostic; the "mark off tasks.md" behavior is injected
   per-lap by `prepare-laps`). Builds on the already-archived freeze-decay / `--new`
   reset work.

3. **enrich-failure-telemetry** _(new — drafted; `openspec validate --strict` passes)_
   Enriches the existing Sentry sink (not a new integration) so a captured failure is
   triageable without the originating machine: run-environment context (rally version,
   OS/arch, terminal), an **anonymous machine-local hash** (random, persisted, not
   derived from any machine attribute), a **globally-unique relay identity**
   (`<machine-hash>-<date>-<relay_id>` + start timestamp), username-stripped cwd, and
   an agent-state snapshot (attempt/budget, failure class, resilience state) on each
   captured failure. No new PII; extends the `before_send` scrubber. Reuses the
   resilience vocabulary from `harden-relay-run-lifecycle`.

4. **rename-rally-roles** _(author input captured; artifacts not yet drafted)_
   Rename the routing roles from a skill-hierarchy framing (JUNIOR/SENIOR/UI/VERIFY)
   to a judgment framing (**builder**/**architect**/**designer**/**analyst**), with
   **builder as the default**. Lean: Option A (no fifth `principal` role yet; `grunt`
   stays optional/per-queue). Touches `.rally/agents/<role>.md`, the `prepare-laps`
   role-assignment guidance, config routing labels, and needs a migration-vs-breaking
   decision (support old+new names for one release vs hard rename). See
   `rename-rally-roles/laps-author-input-1.md`.

5. **improve-harness-consistency** _(draft only)_
   Normalize harness adapters into one `Executor` contract: uniform final-text/summary
   extraction, tool-count, session ID, infra-vs-agent classification, rate-limit
   evidence, and clean-completion-vs-process-exit detection, with a per-adapter
   conformance test suite. Motivated by opencode's headless `run --format json`
   parser/lifecycle issues — fix the integration shape rather than treating a harness
   as unstable. See `improve-harness-consistency/draft.md`.

## Parked

- **build-new-tui** _(stub proposal)_ — a future TUI, including the tabbed/sectioned
  config browser deferred out of cli-polish. Not scheduled.

## Carried-over principles

### OpenSpec/laps coupling

Rally is not married to OpenSpec — they're dating. Rally core, the executor, and the
default role docs stay OpenSpec-agnostic; nothing should make rally *require* OpenSpec
to feel complete. **Laps** is the permanent backend, not one backend among many.
OpenSpec-specific tuning lives in the `prepare-laps` skill, which populates
OpenSpec-aware instructions into laps **only when a run has a related change** (e.g.
"mark off the relevant `tasks.md` boxes"). OpenSpec references are fair game inside
prepare-laps; they should not leak into rally's generic surfaces. (Bounds #4's role
docs and #5's VERIFY-role-boundary item.)

### Naming cleanups

- **`FallbackConfig` → `FreeRunPrompt`** — sets the task prompt for a laps-less,
  promptless ("free") run, not runner failover. **Home: #1 cli-polish.**
- **freeze vocabulary split** — liveness detector → **stall**; resilience circuit
  breaker → keep **frozen**; scheduler route-entry → **benched**. **Done** in the
  archived `harden-relay-run-lifecycle`; reuse these words downstream (incl. #3's
  agent-state tags).
