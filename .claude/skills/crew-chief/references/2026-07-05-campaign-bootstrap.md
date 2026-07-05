# 2026-07-05 — campaign bootstrap + roles v2 start (rally, feat/tui-prototypes)

Second crew-chief session (first autonomous-campaign one, user present ~2h).
Read `docs/orchestration/crew-chief.md` FIRST on wake — it owns the wake-up
protocol and locked decisions; this file is operational technique only.

## Environment notes (delta from 2026-07-04 file; that file still valid)

- `codex exec --dangerously-bypass-approvals-and-sandbox -C /workspace/rally -`
  still the working invocation. Lap A (new package + tests): ~6 min, 87k
  tokens, near-flawless (one semantic nit it self-flagged honestly).
- No cron/systemd/at. Self-trigger = detached setsid loop
  (`~/.local/bin/crew-chief-scheduler.sh`); pidfile+logs in
  `~/.local/state/crew-chief/`. Tested: survives session detach (ppid 1),
  fires real `claude -p` sessions with tool use. `claude -p` headless works;
  workspace trust flag set in ~/.claude.json to silence allowlist warnings.
- Harness CronCreate is session-bound — useless for cross-session triggers;
  the detached loop is the mechanism.
- `npx skills add mitchell-wallace/skills --skill X --skill Y -y` works
  non-interactively; gh token has access to the private skills repo.
- laps named queues: `laps -f crew-chief <cmd>` → `.laps/crew-chief.json`.
  `add tail --json -` accepts an array on stdin. `claim`/`done <id>` work.

## Session shape that worked

- Bootstrap order: skill-trigger fix → scheduler (test FIRST with env
  overrides: CC_FIRST_DELAY=3, benign CC_PROMPT writing a marker file) →
  queue scoping → memory doc → config → then start queue lap 1.
- AskUserQuestion early while user present: resolved AEST cutoff, claude
  disable vs architect conflict, branch topology, in one round.
- Roles v2: reconciliation lap (chief-owned, ~40 min incl. full read of the
  1935-line baseline) produced design.md D1–D9 + tasks.md laps A–E; then
  A (codex) → B ∥ C (codex, disjoint sets: harnessapi+runner+config.go vs
  agent_prompt+init_roles.go). Specs carry explicit "IN FLIGHT RIGHT NOW"
  lane warnings and out-of-lane-noise instructions for whole-tree gates.

## Review catches

- Lap A: recovery.EscalationTargets held recovery *classifications*
  (continue/discard/...) instead of role names — the §6.3 table conflates
  them. Fixed chief-side. Watch for design-doc-literalism: codex follows
  tables faithfully even when a row changes meaning.

## Gotchas

- `improve-harness-consistency` exists TWICE in history: archived on dev
  (2026-06-28) and active on main. Active copy pulled to this branch as the
  NR evidence corpus for queue lap 5; archive prune deleted the archived one.
- openspec archive + next-up.md Done list now capped at 5 (rule written into
  next-up.md header) — do not let them regrow.
- Foreground `sleep` is blocked by the harness; background tasks notify on
  exit — write next specs while waiting instead of polling.
