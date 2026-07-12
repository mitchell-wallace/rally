# Crew chief — campaign state and wake-up protocol

Working memory for autonomous crew-chief sessions. A fresh session must be able
to continue from this doc + the laps queue + git history alone. Keep it lean:
this doc records *state and protocol*, not narrative. Prune rules at the bottom.

## Mission

**Expanded 2026-07-10 (user brief)** — campaign runs until Monday 2026-07-13
09:00 AEST (cutoff epoch 1783897200):

1. rally+laps v1.0.0: rall-c3a8 nitpicker pass now; rall-94fc coordinated
   main release **gated to Monday morning, only if confident it's stable**.
2. rally TUI: role/routing config + provider enable/disable from the TUI
   (Sol chief sessions; brief in scratchpad, re-dispatch bounded sessions).
3. Prayer-app backlog: Sol chief campaign, doc =
   Prayer-app/docs/backlog-2026-07-10.md. Staging when confident; NEVER main.
4. rover: azure-cred isolation via config + tmux-on-ssh (rally relay, dev
   branch; may merge main after chief review).
5. thenn: `thenn job` hardening + verification (rally relay, dev; may merge
   main). NOTE: no systemd in this container → thenn job CANNOT replace the
   wake timer here; shell watchdog stays.
6. Memory system "pacenotes": research (codex) + chief requirements
   (scratchpad pacenotes-requirements.md) → Sol design collab → private
   repos `pacenotes` + `pacenotes-data` via gh → Go CLI build.
7. Delegation: Sol chiefs (gpt-5.6-sol high) for campaigns; rally+laps for
   1-3-lap tasks; GPT usage aggressive; GLM-5.2 zai pool first for junior.

Model routing + merge gates recorded in ~/.config/rally/config.toml and the
harness memory dir (decision-merge-gates-2026-07-10).

## Tonight's push (2026-07-12 night AEST → cutoff Mon 09:00 AEST)

Circuit checkpoint is about the agent-tooling fleet, NOT Moved by the Word —
see pacenotes 01KXB589 (supersedes 01KXB1E26). Contract is phrased around IP
the new employer would want; agent tooling qualifies, a prayer app doesn't.
Circuit is a frozen-but-extensible snapshot for future usability, not a
maintenance-mode signal yet. Mitchell explicitly wants heavy usage burn
tonight — Sol chiefs should not sit idle — until the cutoff, then real
maintenance mode begins.

Wake infrastructure (both confirmed running 2026-07-12 ~12:40 UTC):
- External watchdog `~/.local/bin/crew-chief-scheduler.sh` (source:
  docs/orchestration/, REPO path fixed this session — was pointing at a
  dead container path, so it had never actually been installed
  post-breakout). 5h dead-man's-switch, revives in tmux with
  `--model claude-fable-5 --dangerously-skip-permissions` if no claude
  process is alive.
- In-session cron: recurring check-in every 2h (:43), one-shot wrap-up at
  cutoff (08:57 AEST). Session-scoped — dies if this session exits; the
  watchdog is the fallback for that case.

Overnight backlog for Sol (dispatch one at a time, keep it fed):
1. Telemetry pass (rally's pattern) beyond marshal: laps, pacenotes, radio,
   pitstop, spotter, mechanic, chassis, formula, lanes, starter, tarmac,
   rover.
2. `.circuit/<tool>/` dotfiles pattern beyond rally+laps: same tool list.
3. Interactive configuration beyond rover/spotter/mechanic: same tool list.
4. General robustness/test-coverage hardening pass per tool, framed for
   "future usability/extensibility" — this is a checkpoint meant to be
   picked back up later, not abandoned.
5. Queue empty before cutoff → nitpicker-style scout pass across the fleet
   for anything else worth doing in the time remaining.

Circuit's actual site content (landing + docs) is chief-reserved, not
delegated — tackled directly this session.

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
- Laps source: `/workspace/laps`, dev branch. Installed binary is a dev build
  (`1.0.0-dev.da489f9`, ldflags-injected version); rally MinLapsVersion is
  1.0.0 and rally CI builds laps from dev until laps v1.0.0 tags (rall-94fc).

## Machine facts (this container)

- Scheduler v2 (2026-07-10): watchdog loop `~/.local/bin/crew-chief-scheduler.sh`
  (source of truth: `docs/orchestration/crew-chief-scheduler.sh`) checks every
  5h until 2026-07-12 23:00 UTC (Mon 9am AEST); if NO claude process is alive
  it launches an INTERACTIVE session (no -p) in tmux session `crew-chief`
  (attachable / remote-controllable). While a session is alive its in-session
  cron owns the cadence and the watchdog logs "skip". Logs + pidfile:
  `~/.local/state/crew-chief/`. If `kill -0 $(cat ~/.local/state/crew-chief/scheduler.pid)`
  fails, relaunch: `setsid nohup ~/.local/bin/crew-chief-scheduler.sh >/dev/null 2>&1 &`.
- No cron/systemd/at in this container (systemctl absent — `thenn job` cannot
  run here). `codex exec` needs `--dangerously-bypass-approvals-and-sandbox`
  (see crew-chief skill references/).
- The Claude scratchpad dir under /tmp/claude-1000/ can be WIPED mid-session;
  keep dispatch briefs and reports in repo tmp/ dirs instead.
- User availability (2026-07-10 brief): Sat ~6-9pm, Sun ~9-11am and ~7-9pm
  AEST; can answer via remote control. Batch questions for those windows.

## Session log (newest first; keep ~5 entries, prune older)

- **2026-07-12 (Sun night AEST, fifth 2h cron check-in, ~21:14 UTC)** —
  Mechanic's telemetry dispatch had genuinely finished (ps + file-stability
  verified); reviewed (diff, README addition, path-scrubbing present,
  parity test present), gates rerun clean, pushed (dd09d0e). Memory
  recovered to ~2.8Gi available (false alarm from the prior check-in, no
  action needed). Dispatched next backlog item #1 (telemetry pass) to Sol
  for rover — skipped chassis (pure library, no CLI surface, doesn't fit
  this pattern) and tarmac (naming mid-rename, dune/dunex→tarmac still in
  flux) as poor fits for now. Confirmed running (pid 1836942). Telemetry
  landed: marshal, laps, pacenotes, radio, pitstop, spotter, mechanic;
  rover in flight; remaining: formula, lanes, starter (chassis/tarmac
  deferred — revisit fit later).

- **2026-07-12 (Sun night AEST, fourth 2h cron check-in, ~19:14 UTC)** —
  Spotter's telemetry dispatch had genuinely finished (ps + file-stability
  verified); reviewed (diff, README addition, no token/content leakage into
  events, parity test present), gates rerun clean, pushed (d95b9e7).
  Dispatched next backlog item #1 (telemetry pass) to Sol for mechanic,
  confirmed running (pid 1833839). Memory watch: available dropped to
  ~1.5Gi (from ~1.8-2.2Gi at earlier check-ins) — an "sbx daemon start"
  process (pid 1202877, started Jul11, ~830MB RSS) is the largest single
  consumer and looks possibly orphaned, but not clearly so; did not touch
  it mid-check-in. Worth investigating at the next check-in if the trend
  continues. Telemetry landed: marshal, laps, pacenotes, radio, pitstop,
  spotter; mechanic in flight; remaining: chassis, formula, lanes, starter,
  tarmac, rover.

- **2026-07-12 (Sun night AEST, third 2h cron check-in, ~17:13 UTC)** —
  Pitstop's telemetry dispatch had genuinely finished (ps + file-stability
  verified); reviewed (diff, README addition, no puncture-content leakage
  into events, parity test present), gates rerun clean, pushed (b007994).
  Dispatched next backlog item #1 (telemetry pass) to Sol for spotter,
  confirmed running (pid 1829812). Telemetry pass so far: marshal (earlier
  tonight), laps, pacenotes, radio, pitstop landed; spotter in flight;
  remaining: mechanic, chassis, formula, lanes, starter, tarmac, rover.

- **2026-07-12 (Sun night AEST, second 2h cron check-in, ~15:14 UTC)** —
  Radio's telemetry dispatch (from the prior check-in) had genuinely
  finished (ps + file-stability verified); reviewed (diff + README doc
  addition + parity test present), gates rerun clean, pushed (d29aaa0).
  Dispatched next backlog item #1 (telemetry pass) to Sol for pitstop,
  confirmed running (pid 1825740).

- **2026-07-12 (Sun night AEST, first 2h cron check-in, ~13:13 UTC)** — No
  in-flight dispatches at wake (both laps + pacenotes telemetry from the
  prior entry had already landed and pushed before this check-in fired).
  Circuit's real site content is live and verified
  (circuit-navy.vercel.app). Dispatched next backlog item #1 (telemetry
  pass) to Sol for radio, confirmed running (pid 1821750). Nothing else
  actionable this check-in.

## Prune rules (anti-snowball)

- Session log: keep newest ~5 entries.
- `openspec/changes/archive/` and next-up.md "Done": keep newest ~5 archived
  changes; older live in git history.
- crew-chief skill `references/`: newest file is authoritative; fold stale
  notes forward rather than stacking files (keep ~3).
- This doc stays under ~120 lines. If a section outgrows that, the content
  belongs in an openspec change or the queue, not here.
