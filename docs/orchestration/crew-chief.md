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

- **2026-07-12 (Sun, SAME session continued, ~01:20-02:00 UTC)** — Mitchell
  approved all 4 Linear tickets and directed Sol to orchestrate main
  implementation while chief manages/monitors (session budget: ~2.5h,
  ~50% used at handoff). Bootstrapped rally+laps binaries on this host
  (built from group-1/rally + group-1/laps dev, installed ~/.local/bin —
  neither existed post-breakout). Found + fixed a real bug live: `codex exec`
  was fully broken host-wide (`/etc/codex` mode 0700 blocked stat-ing an
  unrelated file, not just Linear-key reads) — fixed forward on rover dev
  (29ed90b) and chmod'd live. Dispatched Sol (gpt-5.6-sol, codex exec) for
  MIT-111/112/113 (laps cluster, one session) and MIT-114 (thenn, feature
  branch `feat/supervisor-job-backend`) — thenn's first dispatch got cut off
  by an operator (chief) Bash-timeout mistake mid-step-1; chief verified the
  salvaged diff (interface extraction, zero test changes, green gates),
  committed it (a82c255), and re-dispatched properly-backgrounded for step 2
  (scheduler core). New workstream from Mitchell: an inter-agent coordination
  layer ("radio") so frontier models can iterate together on hard plans, with
  a human-facing bridge (Discord/Telegram/etc) as Mitchell's own call.
  Researched `Dicklesworthstone/mcp_agent_mail` (Python+Rust versions) —
  decided to build bespoke Go instead (fleet-consistent, pacenotes-style,
  neither upstream has a human bridge anyway) — chief's call per explicit
  agency grant. Scaffolded `~/group-1/radio` (gh PAT can't create repos,
  known limitation; Mitchell created it manually mid-session), dispatched Sol
  for design-collab, got back a full design doc + working v0 CLI in one
  session (ULID messages, hardlink-atomic writes, advisory file reservations,
  derived inbox/thread views, no daemon/SQLite) — chief-reviewed
  (atomicity/collision/path-validation/thread-consistency all checked) and
  pushed clean. Filed MIT-115 (radio, chief's call, landed) and MIT-116
  (human bridge options for Mitchell, Backlog). All 4 original tickets now
  In Progress. Queue (rall-94fc/rall-c3a8) still release-gated, untouched.

- **2026-07-12 (Sun, FIRST host session, ~01:00-01:20 UTC)** — Bootstrapped on
  the bare host per the breakout runbook; pacenotes already live, brief
  returns real cross-repo notes. Release gate confirmed still correct and
  NOT yet due (465f1ed/45ffe48, both CI green, VERSION 1.0.0 both; gate is
  Sun 23:00 UTC, ~22h out at session start) — did not release early.
  Reviewed + landed the FIRST review item: Sonnet's rover codex-key patch —
  found it sitting unpushed in the STALE ~/rover clone (main stuck at
  14baf4a, missing v0.6.0/v0.7.0), cherry-picked onto current group-1/rover
  dev, pushed (5a662c5). User also flagged high memory/swap live (btm at
  18%/no tty, orphaned since before breakout) — killed it, stopped 4 running
  dune sandbox containers (group-1-01 + 2 stray laps-c5/rally-37 pipelock
  sidecars); swap dropped 1.9Gi→91Mi. Verified sbx-7-host-credential-injection
  DRAFT.md's open questions against a real `sbx` on this host: keychain
  storage/encryption now VERIFIED (age+scrypt file fallback when no OS
  keyring), create-vs-run injection path de-risked from docs (not fully
  live-traced), proxy transparency still open — committed to dune
  dual-backend-exploration (c1fe320, pushed, docs-only). Linear intake sweep
  (agent:chief label created, workspace had none): filed MIT-111/112/113
  (laps trapdoor/file-targeting/contract-surfacing, Todo — ready to go,
  independent of the release) and MIT-114 (thenn supervisor-job-backend,
  Backlog — flagged for Mitchell's evening review, real new-daemon scope
  call). Found thenn's "four proposals" scout commit only wrote one; noted
  in pacenotes + the MIT-114 ticket for a possible re-scout. Queue unchanged
  (rall-94fc/rall-c3a8 still the only two laps, both release-gated).

- **2026-07-12 (Sun ~breakout, FINAL in-container entry)** — Container chief
  DECOMMISSIONED for breakout to bare Rover VM: fleet quiet, watchdog
  killed, in-session timers die with this session. Next chief bootstraps on
  the HOST per the pacenotes breakout runbook (01KX8J7FR8): install
  pacenotes v0.1.1 → init → brief. Crew-chief skill v2 installed at
  ~/group-1/.claude/skills/ + AGENTS.md pointer (both harnesses). Linear
  conventions in pacenotes (01KX9RGD0R): agent:chief label, crewchief::
  comment prefix. FIRST review item on host: Sonnet's rover codex-key patch
  in ~/rover (unpushed). Release gate Sun 23:00 UTC: rally staging 465f1ed
  + laps staging 45ffe48, both CI green, suite 8/8, VERSION 1.0.0 both —
  rally-release + laps-release skills, then rall-c3a8 nitpicker post-release.

- **2026-07-11 (Sat night UTC)** — Scout sweep landed (user-directed, Claude
  allowance burn): pacenotes 4 patch batches (pushed main), laps 3 agent-UX
  proposals, thenn 4 proposals incl. the supervisor job backend (user's
  direction), rover 2 batches (incomplete — scout hit Claude session limit;
  all scouts died at commit step, chief salvaged + committed). Skills repo:
  pathfinder→feature-scout, nitpicker→patch-scout renames + portable
  crew-chief v2 pushed (c6ec61e); consuming repos' skills-lock.json entries
  need renaming on next sync. Rover v0.7.0 RELEASED (provisioning:
  docker-sbx, claude code, thenn, mise, half-RAM swapfile w/ targeted
  resize). pacenotes v0.1.1 published manually + CI/release wiring fixed
  (green). Sol's dune dual-backend research on branch dual-backend-
  exploration (DinD-sibling recommendation). PAT v2 verified (no repo-
  create by design; annotations 403 = Checks:read absent, harmless).
  Sonnet's rover codex-key patch STILL UNPUSHED — review pending. NEXT:
  breakout window (Sun 9-11am AEST), then release gate Sun 23:00 UTC.

- **2026-07-11 (Sat ~15:00 UTC)** — BOTH mbtw OpenSpec arcs COMPLETE and on
  staging (d052bc8): today-queue-bootstrap-sync 47/47 (session 19: honest
  legacy reconciliation — 1.2 needed a real shared-calendar clock-injection
  fix; capstone 2000+2000-record browser lane, 625ms first assembly, 20+20
  records returned). Chief combined review: gates re-run (typecheck, shared
  264/264, both strict validations), calendar fix reviewed. Both changes
  ARCHIVE-READY — archive deferred to post-breakout settling. mbtw backlog:
  everything implementable is DONE except the two design-first items
  (meta algorithm — waits on design; optimistic stats — proposal-only if
  time). 19 Sol sessions total on mbtw.

## Prune rules (anti-snowball)

- Session log: keep newest ~5 entries.
- `openspec/changes/archive/` and next-up.md "Done": keep newest ~5 archived
  changes; older live in git history.
- crew-chief skill `references/`: newest file is authoritative; fold stale
  notes forward rather than stacking files (keep ~3).
- This doc stays under ~120 lines. If a section outgrows that, the content
  belongs in an openspec change or the queue, not here.
