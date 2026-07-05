# Crew chief — campaign state and wake-up protocol

Working memory for autonomous crew-chief sessions. A fresh session must be able
to continue from this doc + the laps queue + git history alone. Keep it lean:
this doc records *state and protocol*, not narrative. Prune rules at the bottom.

## Mission

Land **v1.0.0 of rally and laps together**, then run one nitpicker pass and
address its results. Scope is the `crew-chief` laps queue (below). When the
queue is empty and the nitpicker pass is addressed, the campaign is complete —
subsequent wake-ups only verify health and exit.

## The queue

- File: `.laps/crew-chief.json` in the rally repo (committed).
- Read: `laps -f crew-chief list` / `laps -f crew-chief get`.
- Complete: `laps -f crew-chief done <id>`. Add follow-ups with
  `laps -f crew-chief add after <id> ...` — sparingly; don't snowball the queue.
- This queue is worked by crew-chief sessions directly (delegating per the
  crew-chief skill); it is NOT a rally relay queue.

## Wake-up protocol ("Crew chief, get to work")

1. Read this doc, then `laps -f crew-chief list`.
2. **Relay health gate**: if a rally relay is in progress (check
   `.rally/state/relays.jsonl` for an entry without `ended_at`, and recent
   activity in `.rally/state/`) and healthy → exit immediately (go back to
   sleep). If a relay looks stuck (no activity past the freeze threshold,
   frozen/benched everything) → investigate per `post-relay-review` before
   anything else.
3. Work the head lap per the crew-chief skill (delegate implementation, review,
   commit per lap). Commits are pre-authorized — by crew chief and by agents it
   spawns. Pushes (user-approved 2026-07-05): `dev` and `staging` in BOTH the
   rally and laps repos may be pushed to origin as laps land; `main` and tags
   only via the release workflows (rally-release / laps-release).
4. Mark the lap done, append a session-log line below, commit doc + queue.
5. Repeat while budget allows (~5h between wake-ups; a lap can span sessions —
   leave a continuation note in the session log if so).
6. Queue empty → terminal pass: run the `nitpicker` skill ONCE, implement its
   batches, commit. Do NOT run a second pass. Then mark the campaign complete
   here.

## Locked decisions (user-confirmed 2026-07-04/05)

- Branch flow: **feature → dev** (merge when CI/automated gates pass) →
  **staging** (merged when a test-driving-rally pass clears it) → **main**
  (releases; auto-tag on VERSION bump). Lap 9 codifies this in AGENTS.md +
  rally-release.
- Terminology: hierarchy is **relay > outing > try**; `run`→`outing`,
  route-sense `runner`→`driver`. `relay` was KEPT (no "race" rename).
- Roles v2: baseline is `openspec/changes/rename-rally-roles/roles-v2-design.md`
  (GPT-5.5 Pro, written against pre-refactor v0.12.0 — behaviours right, code
  locations stale; re-inventory first). Drop the UI role (repos own UI/branding
  skills instead). New `review` + `architect` roles.
- Claude is **disabled** in `~/.config/rally/config.toml` while crew-chief
  sessions run (usage contention). Architect route lists `cl:opus` (xhigh)
  first so it self-heals when re-enabled; effectively runs `cx:g55` xhigh
  until then. No deepseek/GLM fallback for architect — if frontier models are
  unavailable, wait.
- Claude harness shorthand renamed **cc → cl** (C-compiler clash). User updates
  shell aliases themselves; rally accepts `cl` (config/types.go alias table).
- Effort levels (`[reasoning]`): review=xhigh, verify=high, senior(gpt)=medium,
  architect=xhigh.
- Telemetry QA evidence: `openspec/changes/improve-harness-consistency`
  (complete 89/89, shipped in 0.12.0; NR corpus in its draft.md) + fresh New
  Relic queries (`--profile rally --accountId 8182741`). Lap 5 consumes then
  archives it.
- Laps source: `/workspace/laps`, dev branch (stints + gating implemented there;
  installed binary is 0.8.1 from main until lap 11 switches us to dev builds).

## Machine facts (this container)

- Scheduler: detached loop `~/.local/bin/crew-chief-scheduler.sh` (source of
  truth: `docs/orchestration/crew-chief-scheduler.sh`), fires
  `claude --dangerously-skip-permissions --model claude-fable-5 -p "Crew chief, get to work"`
  every 5h until 2026-07-08 07:00 UTC (5pm AEST). Logs + pidfile:
  `~/.local/state/crew-chief/`. If `kill -0 $(cat ~/.local/state/crew-chief/scheduler.pid)`
  fails, relaunch: `setsid nohup ~/.local/bin/crew-chief-scheduler.sh >/dev/null 2>&1 &`.
- No cron/systemd/at in this container. `codex exec` needs
  `--dangerously-bypass-approvals-and-sandbox` (see crew-chief skill references/).
- User is reachable until ~2026-07-05 01:30 UTC, then largely offline until
  after July 8.

## Session log (newest first; keep ~5 entries, prune older)

- **2026-07-05 (session 2, autonomous)** — queue lap 5 (rall-3ebf telemetry
  QA) DONE. NR unreachable from this container (no CLI/creds — AGENTS.md NR
  setup is the user's machine); evidence gathered from local
  `.rally/state/*`, codex rollout logs, and a live unauthenticated `agy`
  probe instead; NR refresh to backfill from a credentialed host. Findings →
  `openspec/changes/harden-outing-failure-handling/draft.md` (scopes laps
  6–8): (a) unauthenticated agy exits 0 after a 30 s interactive-auth block,
  no parser matches it; (b) rally retried already-done laps rall-3cc4/00ca
  4× each after their `laps done` hooks fired; (c) completed codex verify
  tries recorded as `harness launch error`/`no changes made` → freeze
  counter paused codex; suspect the session-log matcher cwd comparison +
  the no-changes<3min heuristic vs no-change roles.
  improve-harness-consistency archived (deltas were pre-synced; archive
  pruned to 5). **OPERATIONAL: agy is UNAUTHENTICATED in this container**
  (auth expired after Jul 2; OAuth is interactive — cannot self-heal until
  user returns Jul 8). `ag:*` route entries burn ~30 s/try until lap 6 lands
  or re-auth; senior lists `ag:opus` first.
- **2026-07-05 (session 1, interactive)** — bootstrap + ROLES V2 LANDED TO
  DEV (pushed, b0a3266). Bootstrap: scheduler built+tested (15 fires, first
  04:43 UTC Jul 5), skill trigger fixed, nitpicker+pathfinder installed,
  queue scoped (16 laps), archive pruned, cl alias landed, user config
  rewritten (claude disabled, [reasoning], review/architect/intern/qa
  routes, ui dropped). Queue laps 1–4 DONE: roles-v2 reconciled (design
  D1–D9) then implemented via codex laps A–D + chief fixes (catalog-
  duplication removed at two sites, gate-text genericized, finalize.md
  de-duplicated); e2e verified (fresh init → 8 roles no ui, legacy ui
  migration both ways, live junior relay green on op:zai). Next head lap:
  rall-3ebf telemetry QA evidence. Remaining in rename-rally-roles: none —
  ready to archive after a settling period. Branch: dev.

## Prune rules (anti-snowball)

- Session log: keep newest ~5 entries.
- `openspec/changes/archive/` and next-up.md "Done": keep newest ~5 archived
  changes; older live in git history.
- crew-chief skill `references/`: newest file is authoritative; fold stale
  notes forward rather than stacking files (keep ~3).
- This doc stays under ~120 lines. If a section outgrows that, the content
  belongs in an openspec change or the queue, not here.
