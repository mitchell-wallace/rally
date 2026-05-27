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

3. **git-hygiene** _(full artifacts — proposal/design/tasks/specs written, `openspec validate --strict` passes; reworked for the post-#2 world)_
   Depends on #2. Slimmed to two surviving items + one rewrite: auto-commit on
   init/hook-install (commits whatever #2 declares tracked), agent commit at lap
   boundary, and **folding `summary.jsonl` into the work commit** (no standalone
   `rally: update state` commit; amend-fallback only). Dropped: `.gitattributes`
   for `.rally/logs/` (no such dir; logs live in `dataDir`) and the elaborate
   auto-squash (window git-commits are removed by #2, so there are no streaks).
   Coordination flag: #2's relocation makes `CommitRallyState`'s `.rally/*.jsonl`
   glob a near-no-op — retire/repurpose it when folding.

4. **cli-polish** _(full artifacts — proposal/design/tasks/specs written, `openspec validate --strict` passes)_
   Display fixes (shortcut-hint width-aware truncation + left-align, full-width
   headers) and config UX (model shorthands, `rally init` subcommands). Adds the
   **`FallbackConfig`→`FreeRunPrompt`** rename (config/naming clarity). NOTE:
   prompt-context pruning is NOT here — it lives in #1 (Bounded prompt context).
   `rally reconcile` (R8) is **rejected**; `rally resume` (R14) is **subsumed**
   by #1 + #5's resume work. Coordination: `style.ShortcutHint()` is also edited
   by #5 (label rename) — co-implement/sequence.

5. **agent-lifecycle** _(full artifacts — proposal/design/tasks/specs written, `openspec validate --strict` passes)_
   Core scope (storage-independent): graceful subprocess shutdown (SIGINT +
   `WaitDelay` instead of bare SIGKILL), pause-now + session resume
   (`--resume <session>` where the harness supports it), and shortcut-label
   renames ("graceful stop" / "quit now"). Coordination with #1: the graceful
   shutdown changes the stall-kill path #1 renames/owns; pause/resume + run-state
   overlaps #1's freeze-decay + `--new` reset; #1 ships first. Plus the routed QA
   items:
   R9 route/runner fallback: the chain (e.g. `senior = ['claude','kimi','gpt']`)
   **already exists** — the routing Scheduler rotates a lane to the next entry
   when the current one is `Frozen`/`Exhausted` (`internal/routing/scheduler.go`).
   The Prayer-app stall was a single-entry lane (`senior=['claude']`) with nothing
   to rotate to. So R9 is NOT a feature build; it reduces to: (a) a dependency on
   #1's failure classification marking infra failures frozen/exhausted so rotation
   actually triggers, and (b) docs/defaults encouraging multi-runner lanes plus a
   relay-start warning when a lane has no fallback runner. NOTE: `FallbackConfig`
   is unrelated — it is the default prompt for a laps-less/promptless run, not
   runner failover (rename pending; see "Naming cleanups").
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

## Naming cleanups (clarity refactors)

Surfaced while tracing the freeze/fallback logic; names confused even us.

- **`FallbackConfig` → `FreeRunPrompt`.** `FallbackConfig.InstructionsFile` /
  `loadFallbackInstructions()` / `builtInDefaultFallback` only set the task
  prompt for a laps-less, promptless run (`runner.go:1054`). "Fallback" reads
  like runner failover, which it is not. Rename to `loadFreeRunPrompt()` /
  `FreeRunPromptFile` / `builtInDefaultFreeRunPrompt`, config key
  `[free_run] prompt_file` with a back-compat alias for the old
  `[fallback] instructions_file`. **Home: #4 cli-polish** (config/naming polish).
- **Three "freeze" concepts → three words.** (1) liveness detector
  (`reliability/freeze.go`) = a *stalled process* → rename freeze→**stall**
  (`StallDetector`, `Assessment.Stalled`, "stalled try"); (2) resilience circuit
  breaker (`resilience.go`, persisted `agent_status.jsonl`) = an *agent type
  benched after repeated infra failures* → **keep "frozen"** (user-facing; its
  `event_type` is persisted); (3) scheduler `EntryState.Frozen`
  (`routing/scheduler.go`) = a *route entry currently unavailable* → rename
  **`Frozen`→`Benched`**. **Home: #1** — it already reworks these subsystems.
  Consequently #1's spec language uses "stall-recovery" (not "freeze-recovery")
  for recovery from a liveness kill, while the agent-type cascade keeps "frozen".

## Out of scope for rally

Prayer-app remediation (run tests, broken-SMTP smoke test, mark laps done,
archive) is target-repo cleanup, tracked separately.
