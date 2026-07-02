# Handoff — formalize OpenSpec changes #5–#8 (2026-07-02)

Session-resume note for the plan-formalization campaign. Delete after
prepare-laps completes and the queues are committed.

## Mission (user's brief)

1. Formalize drafts #5–#8 from `openspec/next-up.md` into full spec-driven
   artifacts (proposal/design/specs/tasks) using the openspec skills. ✅ done
2. Review passes (~3) on the plans using a workflow based on
   `openspec-plan-review`, with **GPT-5.5 via `codex exec -m gpt-5.5`**
   (read-only, no permission-skip flags — host machine) as the reviewer;
   Claude triages findings, tightens artifacts, commits per pass. Stop when
   reviews find no substantive issues. ⬅ IN PROGRESS
3. Write/refresh this handoff report.
4. Run `prepare-laps` against each change via **Opus subagents, one at a
   time** (less lost work if usage-limited), writing separate queues to
   `.laps/<number>-<change-name>.laps.json`.

## State at last update

- Branch `dev`. Commits so far (all `--no-verify`, artifacts only):
  - `d440693` #6 decompose-run-one formalized
  - `c8c9fca` #7 decompose-remaining-source-files formalized
  - `cefc1e0` #8 decompose-large-test-files formalized
  - `0e8e964` #5 separate-runtime-presentation-boundary formalized
- All four validate with `openspec validate <change> --strict`.
- Review passes completed: 0 of ~3.

## Key grounding facts (verified 2026-07-02, commit f55712c)

- #4 modularize-harness-adapters is implemented but **not archived** — its
  specs deltas (incl. `composition-root-structure` "Presentation-neutral
  relay-start seam") are still in its change folder. #5's delta to the same
  requirement **builds on #4's delta text**; archive #4 before #5 (noted in
  #5 tasks 1.3).
- `internal/agent` is gone; draft #8's nine-file inventory is now **eight**
  (agent_test.go was carved up by #4). Archguard test grandfather map = those
  eight exactly.
- #4 introduced three warning-band harness files (claude.go 571,
  opencode_evidence.go 570, antigravity.go 531) — #7 routes them **out of
  scope** with a revisit trigger (design Decision 3).
- No event/callback mechanism exists in the runner; presentation coupling
  inventory and print-site table live in #5's design (from the gpt-5.5 wiring
  dossier, scratchpad `presentation-wiring-dossier.md`; final report also
  summarized in #5 design Context/Decision 3).

## Architecture decisions made (do not re-litigate without user input)

- **#5**: stdlib-only `internal/relay/runner/runtimeevent` child package holds
  events + operator-control vocabulary; synchronous emit at print sites (byte
  parity); `internal/presentation/terminal` adapter (sink + keyboard-backed
  ControlSource incl. `WaitResume`); injection `cli → app.RelayStartOptions →
  runner.Config`; monitor status line stays runner-driven (documented
  residual); relay-log lines are not events.
- **#6**: same-package per-phase file split (no `attempt` child package);
  relay_steps.go splits now; only classify/record get sub-step extraction;
  spec delta = `relay-module-structure` ADDED requirements.
- **#7**: one change, one commit per package; harness warning-band files
  routed out; new `support-module-structure` capability (monitor+store) +
  `composition-root-structure` delta (providers split supersedes
  "providers.go unchanged"; routes-check ADDED requirement).
- **#8**: scope = archguard test grandfather map (eight files); mirrors
  #6/#7 final layouts (their tasks record file lists for this); helper
  extraction extends existing `helpers_test.go`; new `test-module-structure`
  capability; golden extraction deferred.
- Grandfather-map regeneration is a policy-baseline update, never an
  `architecture-guardrails` spec change (#4 precedent).

## Review workflow (step 2)

Per `openspec-plan-review` skill adapted to Codex: reviewer = read-only
`codex exec -m gpt-5.5` fed a prompt with the findings format
(Findings/What's Solid/Product Calls, severities Critical/Major/Minor) and the
calibration file
`.claude/skills/openspec-plan-review/references/accepted-tightening-patterns.md`.
Pass structure: pass 1 = deep review of #5 + combined review of #6/#7/#8
(incl. cross-change coordination); triage → tighten → validate → commit. Pass
2 = fresh reviews against updated artifacts, diffing against pass-1 findings.
Pass 3 = final; stop when no substantive findings. Escalate product calls to
the user rather than deciding silently (there may be none; the changes are
behaviour-preserving refactors).

## Prepare-laps stage (step 4, after reviews)

- Use the `prepare-laps` skill via **Opus subagents** (`Agent` tool,
  `model: "opus"`), **sequentially**, one per change, in order #5, #6, #7, #8.
- Each writes its own queue file: `.laps/5-separate-runtime-presentation-boundary.laps.json`,
  `.laps/6-decompose-run-one.laps.json`,
  `.laps/7-decompose-remaining-source-files.laps.json`,
  `.laps/8-decompose-large-test-files.laps.json`.
- Commit after each subagent completes (protects against usage limits).
- Memory note: `parallel-batch-worktree-staging` — independent Rally batches
  run as separate branch+worktree with their own `.laps` queue; these
  per-change queue files fit that staging pattern.

## Scratchpad artifacts (this session, /tmp — may not survive reboot)

- `ground-survey-final.md` — full re-grounding report (#1–#8 sections).
- `presentation-wiring-dossier.md` — #5 wiring dossier (final message at end
  of file, after last `codex` marker).
