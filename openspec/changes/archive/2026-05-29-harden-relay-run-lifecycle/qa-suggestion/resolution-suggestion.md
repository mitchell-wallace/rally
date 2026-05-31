# Resolution Suggestions — Prayer-app blackbox post-mortem

This synthesizes the two QA reports (`qa-report/` and `qa-report-2/`) against
rally's current code, and proposes where each fix belongs in the rally model.
It also pulls in the `prepare-laps` skill where appropriate.

## Framing: three layers of responsibility

Rally has three intervention points. Conflating them produces brittle fixes.

1. **Meta-harness (deterministic Go code in `rally/`)** — owns lap pinning,
   retry policy, freeze recovery, prompt transport, error classification,
   escalation, reconciliation. Decisions made here apply uniformly and don't
   rely on agents reading instructions.
2. **Prepare-laps planning skill (`.claude/skills/prepare-laps/SKILL.md`)** —
   shapes the queue *before* a relay runs. Owns role assignment cadence,
   verification placement, declaring expected file paths and acceptance, and
   sanity checks against routing config.
3. **Runner role instructions (`.rally/agents/*.md`)** — guide the agent
   already mid-run. Owns role-boundary discipline (e.g., what VERIFY may fix
   inline) and runtime exit hygiene (where to call `laps done`, what to put
   in wrapups).

A useful test for each fix: *if an agent ignores or misreads its prompt, does
this still hold?* If yes, it belongs in the harness; if no, it belongs in
the runner role instructions, and the harness should have a backstop.

## Root-cause map → layer

| # | Failure | Pri | Layer | Notes |
|---|---|---|---|---|
| RC1 | `laps done` CWD-dependent → wrong lap consumed | P0 | Harness + Runner | Hook script path resolution is upstream `laps` (not rally), but rally must defend. |
| RC2 | No "lap claimed == lap completed" check | P0 | Harness | Foundational — unlocks most other validations. |
| RC3 | Argv prompt blows past ARG_MAX on Claude lane | P0 | Harness | `claude -p $PROMPT` accumulates `RecentTryContext` until E2BIG. |
| RC4 | Rate-limit retries burn budget; monthly-cap not special-cased | P1 | Harness | `patterns.go` matches generic `rate-limit`; one strategy. |
| RC5 | No human escalation after N stalls | P1 | Harness | `retry_failed` is counted but never surfaced. |
| RC6 | Freeze-recovery treats VERIFY same as implementation | P1 | Harness | `runner.go:874` — role-agnostic. |
| RC7 | VERIFY role boundary fuzzy (small fix vs. 11-file commit) | P2 | Runner | `verify.md` allows "small fixes." Codex took it as license. |
| RC8 | OpenSpec `tasks.md` never updated | P2 | Prepare-laps + Runner | Either drop the file or wire it explicitly into final VERIFY acceptance. |
| RC9 | VERIFY-found blocker → single-lane stall | P2 | Prepare-laps + Harness | `senior = ["claude"]` plus dead Claude = total stall. Multi-entry routes already supported by parser. |
| RC10 | No drift-reconciliation command | P3 | Harness | New `rally reconcile`. |

## What rally already does (so we don't propose duplicates)

Verified via code spot-check:

- `laps get head` does not return a stable lap ID; `task.LapID` is empty
  for laps-backed runs (`internal/laps/adapter.go:11-13`, `internal/relay/runner.go:1068`).
- `ClassifyError` already handles `claude rate-limit interrupt` with a
  wait-and-resume strategy (`internal/reliability/patterns.go:47-60`), but
  treats all 429s identically.
- `agent_status.jsonl` already tracks `paused / retry_failed / frozen`
  transitions, with a 5-hourly-failure → frozen escalation (`resilience.go:137-163`).
  Nothing reads "frozen" out to a human.
- The routing parser already accepts arrays of harnesses
  (`internal/routing/parse.go`). Prayer-app's config just lists one each.
- The `markerAsText` detector already catches agents that *type* `laps done`
  instead of running it (`runner.go:859-868`). Good prior art for the
  defensive-against-agent-misbehavior pattern.
- No `reconcile` command exists.
- No file-change cross-check on `laps done`.
- Freeze recovery (`runner.go:874-877`) is role-agnostic.

## Recommendations by layer

### A. Rally meta-harness (deterministic)

These are the load-bearing fixes. RC1, RC2, RC3 should land first because
they prevent silent corruption; the rest are recoverability and operator UX.

**A1. Pin the active lap ID at run start (RC2).**
The current shape of `laps get head` doesn't expose an ID, so the upstream
`laps` CLI needs to grow that. Once it does, rally writes the pinned ID into
`RunState` at run start and, at completion, verifies that
`runStateAfter.RecordedLaps == [pinned_id]`. If not, mark the try `failed`
with a distinct reason (`wrong_lap_consumed` or `multi_lap_consumed`) and
do **not** advance the queue further. This is the safeguard that would have
caught the `pray-43a5` ghost completion immediately.

**A2. Cap and re-route prompt transport (RC3).**
`internal/agent/claude.go:39` passes the prompt as `-p $PROMPT` argv.
Prompts accumulate `RecentTryContext` from the last 5 tries; with a
stalled relay this grows monotonically until E2BIG (~2 MB).
- Switch claude/codex/gemini/opencode adapters to write the prompt to
  `.rally/tries/try-N.prompt.md` and invoke `claude -p "$(cat …)"` only as
  a fallback — preferably feed via stdin where the CLI supports it.
- Add a hard size cap (e.g., 256 KB) on the assembled prompt; when over,
  trim `RecentTryContext` oldest-first, then `PreviousSummary`. Log the
  trim so retries are not silently lobotomized.

**A3. Classify failures into recovery families (RC4).**

Extend `ErrorPatterns` with:

- `rate_limit_monthly` — match `overageStatus.*rejected`, `monthly` near
  `usage limit`. Strategy: freeze lane immediately + emit escalation;
  do not burn retry budget.
- `rate_limit_short_term` — current behavior, but should be a *free*
  wait-and-resume that does not count against `retry_budget`.
- `harness_launch_error` — match `argument list too long`, `fork/exec`,
  `bufio.Scanner: token too long`. Strategy: compact prompt and rotate
  to next route entry; if still failing, freeze with a distinct reason
  so the operator sees that this is plumbing, not agent quality.
- `zero_tool_calls_sub_second` — synthetic classification when
  `runtime < 5s && tool_calls == 0`. Same handling as launch error.

This is where rally's "transparent communication about risk" lives: the
operator who reads `tries.jsonl` should be able to tell apart a flaky
test, a quota wall, and a launch failure at a glance.

**A4. Role-aware freeze recovery (RC6).**
`runner.go:874-877` treats any try with `freezeMarked && commitHash != ""`
as success. For VERIFY laps this is wrong — committed files are not evidence
that verification *concluded*. Gate recovery on role:

- non-VERIFY roles: keep current behavior.
- VERIFY: require a sentinel artifact (e.g., the wrapup summary was written
  *and* contains a verdict marker like `verify-result: pass|fail`, OR a
  `.rally/verify-report.json` file exists for this run). Without that,
  mark the try failed and let the next try (or escalation) take over.

**A5. Human escalation on stall (RC5).**
Once `resilience.go:RecordHourlyFailure` rolls over from `retry_failed` to
`frozen`, emit a structured escalation:

- write `.rally/escalations/<timestamp>-<reason>.json` (file in a watched
  dir is the simplest universal trigger),
- include: relay ID, agent type, last failure reasons, last 3 commit
  hashes, list of laps still open, suggested operator action,
- optionally call a configured webhook (`[notifications].webhook_url` in
  `config.toml`),
- exit the relay with a non-zero distinct code so any orchestration layer
  (cron, GHA, container supervisor) can route on it.

The bar is: an operator skimming a watched directory should know within
one glance whether this is "agent working hard" or "infrastructure is
broken; nothing will happen until I intervene."

**A6. Defensive `laps done` consumption check (RC1 backstop).**
Even after RC1's CWD bug is fixed in `laps`, rally should:

- log a structured `hook-audit.jsonl` entry that captures CWD,
- compare `RecordedLaps` length to "expected 1" before advancing the queue,
- and, if `RecordedLaps` shows the wrong lap (per A1 pinning), refuse to
  advance and emit an escalation.

This is the same instinct as the existing `markerAsText` detector — assume
agents and external tools will misbehave, and verify state transitions.

**A7. `rally reconcile` (RC10).**
A new command — call it once at end-of-relay or on-demand — that compares:

- HEAD commits since the relay started → file paths touched,
- open laps (those still `isDone: false`) → file paths declared by the lap
  (if `prepare-laps` recommendation P3 below lands),
- if available, OpenSpec `tasks.md` checkboxes vs. the diff,

and prints a punch list: *"these laps look done by code state but are not
marked done; these tasks.md boxes match committed work."*

Output is advisory; an `--apply` flag could optionally tick checkboxes and
mark laps done. Without this, every messy run requires manual triangulation
between three truth sources.

**A8. Optional file-change cross-check (RC2 + RC8 enabler).**
When a lap description contains a `Files & scope` block (the `prepare-laps`
section spec already encourages this), parse it. On `laps done`, if none
of those files appear in the run's diff, fail the done call with a
distinct reason. Only activate when the field is present — keep it opt-in.

### B. Prepare-laps planning skill

These steer how laps are written so the harness checks above have something
to check against. None require code changes outside the skill file.

**B1. Encourage declared file paths in `Files & scope` (enables A8).**
The skill already lists "Files & scope" as one of the dynamic sections.
Add a sentence: *"Where the work is mechanical or file-targeted, list the
specific files the lap should touch. Rally can cross-check these against
the diff on `laps done`."*

**B2. Don't put critical blockers on a single-runner role (RC9).**
Add to the "Orient" step: *"Before queuing high-stakes laps (auth,
migrations, data correctness), check that the relevant role in
`.rally/config.toml` has at least one fallback runner. If `senior` or
`verify` is single-entry, suggest the user add a fallback before the
campaign starts."*

**B3. Final VERIFY must own OpenSpec bookkeeping (RC8).**
For OpenSpec work, the final VERIFY lap's acceptance section should
include: *"Tick the relevant `tasks.md` checkboxes; verify
`openspec status --change <name>` shows no remaining open work; the
change is ready to archive."* Without that, `tasks.md` will keep drifting
toward false negatives, exactly as it did on prayer-app (0/39 boxes).

**B4. Tighten VERIFY split rules.**
Add: *"Verification laps may fix only tiny, safe one-liners (no more than
~10 lines and ~2 files). Anything larger must become a new head lap. If
you find yourself committing a multi-file fix from a VERIFY lap, you've
escaped the verify scope — stop and create a head lap."* This complements
the runner-side instruction in C1 but lives in planning because the
queue's *shape* determines whether VERIFY is the right slot for a given
gap in the first place.

**B5. Manual-smoke laps state the environment they need.**
For tasks like the broken-SMTP smoke, the lap should explicitly say
"requires operator action / local dev env" so that a) it routes
appropriately (probably last in the queue), and b) an operator-escalation
artifact has clear "next human step" content. This addresses the
QA-report-1 point about no clear escalation path for
environment-blocked work.

### C. Runner role instructions

These tighten in-run behavior. Keep them short — role files are heavily
re-injected into prompts.

**C1. `verify.md` — sharper boundary on "small fix" (RC7).**
Current line *"Apply small fixes directly when they are clearly correct
and only a few lines"* gave Codex license to ship an 11-file commit.
Replace with: *"Apply tiny one-line fixes (typos, obvious off-by-one,
single import) only when the fix is mechanical and the rest of the
change is fully verified. For anything else — including multi-file fixes,
controller wiring, refactors, or anything that touches behavior — add a
head lap describing the gap and stop."*

**C2. All roles — exit hygiene (RC1 defense).**
Add to all four role files: *"Run `laps done` from the repo root, never
from a subdirectory. If `laps done` errors or its hook does not print
follow-up wrapup instructions, do **not** retry from another directory —
that may consume the next queued lap. Instead, run `laps handoff` and
explain what happened in the wrapup."*

**C3. All roles — confirm lap match (RC2 supplement).**
Add: *"Before calling `laps done`, sanity-check that the work you did
matches the lap title. If you started on a different problem mid-run
(e.g., because a blocker appeared), call `laps handoff` instead so the
next runner picks the right lap. Never `laps done` a lap whose work you
did not actually do."* This is the agent-side mirror of A1's
harness-side check.

**C4. All roles — record lap ID in wrapup summaries.**
Add: *"Include the lap ID in the wrapup `--summary` so audits can match
your commit work against the lap queue."* Cheap traceability for when
the harness check in A1 isn't yet wired.

## Prioritization

If only a few changes land first:

1. **A1 + A2 + A3** — these eliminate the corruption modes
   (`pray-43a5` ghost completion, Claude E2BIG death, rate-limit budget burn).
2. **A5 + C1** — these stop the silent-stall pattern and the
   VERIFY-overreach pattern.
3. **A7 + B3** — these recover the operator's ability to reason about
   "is this campaign actually done?"

Everything else is incremental.

## Naming

The current change folder name (`…-blackbox-review`) describes the
*method* of the review, not the *subject*. Rename suggestion when the
proposal lands:

- `rally-stall-recovery-and-lap-integrity` — covers A1/A2/A6/A8 + C2/C3.
- `rally-failure-classification-and-escalation` — covers A3/A5.
- `rally-role-boundary-tightening` — covers A4/B4/C1.

These can be three separate OpenSpec changes or one bundled change with
those as sections. The blackbox review itself stays under its current
name; the proposals it produces get fresh, scoped names.

## Open questions for architecture / solution design

These need owner judgment before the work can be turned into laps.

1. **Lap-ID pinning (A1) requires upstream `laps` to expose IDs.** Is
   that a coordinated change with the laps tool maintainer, or should
   rally fork-or-vendor enough to read the IDs out of `.laps/laps.json`
   directly? The latter is faster but couples rally to laps' on-disk
   shape.

2. **Where does "verify result" live (A4)?** Options: (a) a sentinel
   string in the wrapup summary that the harness parses, (b) a dedicated
   `.rally/verify-report.json` written by the agent before `laps done`,
   (c) a new `laps verify-pass` / `laps verify-fail` command. (c) feels
   cleanest but is the biggest scope; (b) is the simplest contract.

3. **Should A5's escalation block the relay or just notify?** I lean
   "block + exit non-zero on monthly-cap and consecutive harness
   launch errors; notify-only on first hourly retry." But the answer
   depends on whether rally is expected to be supervisor-restarted vs.
   long-lived.

4. **Reconcile (A7) — automatic on relay end, or operator-invoked?**
   Automatic post-relay reconcile would catch drift earlier but might
   noise up output for clean campaigns. Operator-invoked keeps the
   blast radius small but relies on remembering to run it.

5. **Should prepare-laps proactively detect single-runner critical
   routes (B2) and block proposing the queue, or just warn?** Hard
   block on critical-domain heuristic feels too opinionated for a
   planning skill; soft warn + operator confirmation seems right but
   needs a definition of "critical."

6. **VERIFY-found blocker, two paths (RC9):**
   - Path A: keep the current "VERIFY creates head lap, next runner
     picks it up" flow but ensure route fallback (B2) prevents single-
     lane stalls.
   - Path B: allow VERIFY to optionally fix-and-re-verify within the
     same lap when the fix is bounded (per C1's tightened rule).
   Mixed approach is plausible (B for tiny, A for everything else)
   but the threshold needs to be either deterministic-in-harness or
   trusted to the role prompt. Which?

7. **OpenSpec sync (B3 vs. drop `tasks.md`):** If `tasks.md` is dead
   weight in the live workflow, is the right move to teach the final
   VERIFY lap to update it, or to deprecate the checkboxes for
   rally-driven changes? The QA reports show both options are
   defensible; the answer probably depends on how often non-rally
   contributors use `tasks.md` as a status surface.
