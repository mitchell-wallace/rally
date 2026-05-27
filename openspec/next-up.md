# Next up — proposed change order

Living planning note for the queued OpenSpec changes. Order reflects
dependency and risk-of-drift, not final scope. Names marked _(proposed)_
are not yet renamed on disk.

## Resolved out-of-band

- **R2 `laps done` from subdir** — FIXED in the `laps` repo (commit `3181c44`,
  VERSION → 0.4.6, **not pushed**). Real cause was not dir resolution
  (`DiscoverRepoRoot` already walks up) but hooks running in the CWD instead of
  the repo root; fix sets `cmd.Dir = repoRoot`. Pushing to `laps` main
  auto-cuts the v0.4.6 release. Decide when to push.

## Order

1. **harden-relay-run-lifecycle** _(renamed from
   `prayer-app-auth-email-delivery-hardening-blackbox-review`; artifacts
   drafted — proposal/design/tasks/specs written, `openspec validate` passes)_
   Converts the **solid** QA findings into a real change; keeps the raw reports
   as `qa-report*` evidence subdirs. Sequenced first so we lock the response
   before the codebase drifts. Scope folds state integrity + freeze/retry/resume
   (see "Scope decision").
   - **State integrity:** R1 lap-ID pinning (P0), R3 file-change cross-check,
     R7 role-aware freeze-recovery (VERIFY needs a verdict artifact).
   - **Run-lifecycle reliability (freeze/retry/resume):** the most impactful
     bug — a harness can be **permanently** frozen for a repo with no recovery.
     - A1 freeze decay: `frozen` is terminal in `resilience.go getState`; add a
       time-based expiry back to active/probation.
     - A2 `--new` resets `agent_status.jsonl` (today it does NOT — `--new`
       recovering was timing/fresh-scheduler luck, not a reset).
     - B1 infra-only failure classification: today EVERY non-success
       (agent error, "no changes made", harness launch error, rate limit,
       timeout) counts equally toward freeze. Only rate-limit / harness /
       API-timeout should. `ClassifyError` exists in `patterns.go` but only
       steers in-attempt retry strategy, not the freeze counter. (= QA R4)
     - C1 hourly retries >1: `maxAttempts=1` on the hourly retry burns a freeze
       life on a single transient blip. (= QA "too timid" H3)

2. **tidy-rally-runtime-data-storage**
   `.rally/state/` relocation, `summary.jsonl`, opt-in Sentry, laps bundling.
   Coordinate record shapes with #1 so R10/R11 land here (commit list per try +
   laps-attempted) rather than a second rewrite. Sentry from this change is the
   chosen home for **R6 escalation** (alerts via Sentry; a direct Slack
   integration is a possible later add, deferred). Also the home for **prompt-
   size logging**: emit assembled-prompt size + per-source breakdown so runaway
   prompts are caught empirically.

3. **git-hygiene**
   MUST be updated for #2 first. After #2, `state/` is gitignored and
   `summary.jsonl` is the only churning tracked data file, so most
   `rally: update state` commit noise disappears — auto-squash section largely
   evaporates; `.gitattributes`/logs section likely moot (verbose logs live in
   `dataDir`, not `.rally/logs/`).

4. **cli-polish**
   Candidate home for: prompt-context pruning (R5, see below) if not folded
   elsewhere. NOTE: `rally reconcile` (R8) is **rejected** — fixing internal
   state via a CLI command is a code smell; correctness should be intrinsic
   (R1 pinning prevents the drift; R12 keeps tasks.md current).

5. **agent-lifecycle**
   R9 route/runner fallback (repo's `FallbackConfig` is *instructions*, not
   runner fallback — multi-entry routes parse, scheduler-on-death unverified).
   R12 VERIFY role boundary: the generic VERIFY role (`.rally/agents/verify.md`)
   stays OpenSpec-agnostic. The "mark off tasks.md" behavior is OpenSpec-
   specific and is injected **per-lap by `prepare-laps`** only when a lap has a
   related OpenSpec change — not baked into rally core or the default role doc.
   This subsumes R13 (OpenSpec↔laps bridge): no separate sync mechanism; the
   coupling lives at the prepare-laps layer and is populated into the lap when
   relevant. See "OpenSpec/laps coupling principle" below.

## Prompt bloat (R5) — reframed

Not a transport problem yet. `runner.go:581` already caps to `RecentTries(5)`,
but each try `summary` is concatenated in full with no char budget, so verbose
summaries (or large role/task instructions) swell `current_task.md`.
- **Do now:** keep ~5 (make count configurable), add per-summary + overall
  char budget with sensible truncation; min/max bounds for terse/verbose
  outliers. Argv stays.
- **Then:** Sentry logs prompt size + per-source breakdown (#2) to catch
  runaway growth and identify the real dominant source.
- **Later:** test stdin support across harnesses once we have data. Argv is the
  best-supported transport across harnesses today.

## Scope decision (decided)

**#1 folds in freeze/retry/resume** alongside state integrity. R4
(classification) ≈ B1 and R7 (freeze-recovery) directly touches the freeze
logic, so they ship together under `harden-relay-run-lifecycle`. The
permanent-lockout bug does not wait behind tidy/git-hygiene/cli-polish.

## OpenSpec/laps coupling principle

Rally is not married to OpenSpec — they're dating. Rally core, the executor,
and the default role docs stay OpenSpec-agnostic; nothing should make rally
*require* OpenSpec to feel complete. **Laps** is the permanent backend (rally's
"extra toe"), not one backend among many. OpenSpec-specific tuning lives in the
`prepare-laps` skill, which has strong OpenSpec support and **populates
OpenSpec-aware instructions into laps only when a run has a related change**
(e.g. "mark off the relevant `tasks.md` boxes"). OpenSpec-specific references
are fair game inside prepare-laps to smooth that integration; they should not
leak into rally's generic surfaces.

## Out of scope for rally

Prayer-app remediation (run tests, broken-SMTP smoke test, mark laps done,
archive) is target-repo cleanup, tracked separately.
