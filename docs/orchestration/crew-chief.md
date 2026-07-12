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
- **2026-07-11 (Sat evening UTC)** — USER SESSION (window): pacenotes+data
  repos pushed to GitHub (memory now wipe-proof; data remote repointed);
  PAT verdict: contents OK, issues/creation blocked — new PAT tonight (user);
  Linear = primary intake (MCP at group-1 scope; trigger = Mitchell comment
  mentioning crewchief); GitHub machine user agreed (username via gh issue).
  BREAKOUT to bare Rover VM planned Sun AM window; dune->dunex parity
  manifest stored in pacenotes (01KX8835TG); dunex facts + post-breakout
  tasks (AEST tz, thenn background-service design) in pacenotes. agy AUTHED
  → exposed real rally bug: transient startup auth noise failed every
  completed agy run (auth/proxy false positive); fixed 465f1ed (source-split
  evidence, red-green proven, live relay + real-backend PASS → suite 8/8),
  ff'd to staging. mbtw session 18: local-Postgres correction closed ALL
  gated tasks (3.5/5.5/8.3), 40/47; session 19 running (legacy checkbox
  reconciliation, no courtesy ticks + capstone 8.4 seeded browser lane).
  pacenotes v0.1.1 release triggered (verify once new PAT can read Actions).
- **2026-07-11 (Sat ~06:50 UTC)** — MEMORY-LEVELLING ARC COMPLETE (sessions
  8-15, 42/42 tasks) and PROMOTED to mbtw staging (325f667) after chief
  full-arc review: deterministic replay core w/ divergence guards verified,
  legacy normalization side-effect-free, typecheck + shared 257/257 + both
  strict validations re-run by chief. CAVEAT held open: Postgres functional
  lane (bootstrap 3.5/8.3) needs CI/user run — NO DOCKER in container
  (recorded in pacenotes). today-queue-bootstrap-sync at 20/47, session 16
  continues it. mbtw backlog remaining: bootstrap-sync completion,
  ui-component-library arc, optimistic-stats design proposal, archive
  levelling change after settling.
- **2026-07-11 (session 13 cont., Sat ~07:30 UTC)** — memory-levelling arc
  sessions 8-10 DONE on dev (not staging — arc promotes only when
  feature-complete): foundations, backend persistence/sync gating, per-card
  streaks, canonical memory_queue_snapshot THROUGH today-queue-bootstrap-
  sync's contract. Progress: leveling 17/42, bootstrap-sync 9/47, strict
  validation both changes each session. Session 11 running (shared scorer
  extraction first per handoff). One usage-limit bounce (session 8 dispatch,
  ~90min pause, one-shot cron retried — pattern works). User window Sat
  6-9pm AEST: status prepared; open asks = pacenotes repos/PAT, agy OAuth.
- **2026-07-10/11 (session 13 cont., overnight)** — mbtw sessions 5-7 DONE,
  reviewed, each promoted to staging (9e0e8a9 chips / 987eab5 double-tap +
  memory modals / b5cfc86 hints framework). Session 5 died once to a
  transient sol CAPACITY error mid-lap; continuation chief preserved and
  completed the partial diff (recovery pattern holds for codex chiefs).
  Session 7's audit: lazy-loading is implemented+green (44 tests) but NO
  OpenSpec record exists — chief ruling: verified-done, no retroactive spec
  reconstruction. P0s+P1s+ALL P2 tweaks complete in 7 sessions. Session 8
  dispatched: memory-leveling-redesign arc (openspec-driven, ~2-3 bounded
  sessions, foundation phases first, today-queue-bootstrap-sync dependency
  flagged). Remaining after: ui-component-library arc, design-first
  proposals (optimistic stats), Monday rally+laps release.
- **2026-07-10 (session 13 cont., ~17:30 UTC)** — mbtw sessions 3+4 DONE and
  chief-reviewed: ALL P1s (streak high scores use row-locked monotonic
  max-merge — reviewed; segmented controls; list-view delete; five tweaks
  found already-landed, coverage added) and the notification-bell P2
  (design-note-first: local-only versioned envelope, archive history
  decoupled from clearable rows, taxonomy-compliant placement; 1039 unit/
  525 component/161 integration/5 e2e green). Staging promoted per batch:
  4b933a8 (P1s), 652ea6f (bell; June-19 staging-only test tweaks now fully
  superseded by dev-side resolutions — future merges won't conflict).
  Session 5 (tappable chips) dispatched. rally/laps: idle by design until
  Monday release.
- **2026-07-10 (session 13 cont., ~13:30 UTC)** — Staging CI fix-forward
  BOTH repos green (rally ae5d23e: chief's gofmt miss on tui.go + fresh
  stdlib CVE GO-2026-5856 → Go 1.26.5 bump; laps 45ffe48: same bump
  preemptively). mbtw session 2 DONE (5565470, 700k tok): remaining 3 P0s
  (daily rollover via Page Lifecycle resume — 2-line surgical fix w/ tests;
  sync-paused copy; expired-prayer answering) + 2 P1s; chief spot-reviewed +
  typecheck; PROMOTED mbtw dev→staging (7bb69f9, merge commit per repo
  convention; 2 test conflicts vs June-19 staging tweaks resolved to dev's
  verified side, conflicted unit file re-run green 31/31). ALL SIX mbtw P0s
  now on staging. Session 3 (P1 tweaks) dispatched. Monday: rally+laps
  staging→main (both staging CIs green at ae5d23e/45ffe48) + nitpicker.
- **2026-07-10 (session 13 cont., ~09:45 UTC)** — TEST-DRIVE PASS, staging
  promoted: rally 7301b00, laps 62d4ae9 (both pushed; CI watched). Suite 7/8
  (agy env-fail only). Live drives: laps queue drained incl. resume-after-
  stop; round-robin op/cx; REAL usage-limit rotation observed (codex rolling
  limit hit ~09:10, resets 12:14 — did NOT burn a weekly /reset; Sol
  dispatches paused until then, one-shot cron re-dispatches mbtw chief).
  FIXED from mbtw chief's field report: stopped relays claimed "relay
  complete" (TUI banner + CLI line) — 7301b00 adds stopped-with-work-
  remaining end line, DoneHintFunc seam, tests; openspec wording aligned to
  the implemented Ctrl+X contract. mbtw session 1 reviewed: 3/6 P0s landed
  (queue corruption root-caused; answered/prayed decoupled; sign-out = Web
  Lock cross-context refresh coordination), typecheck 4/4, no new lint
  (backend prettier errors pre-exist), dev pushed. thenn v1.1.0 RELEASED
  (job hardening; chief fixed removal idempotence + killed a 21-try
  handoff loop on an env-blocked lap — rally gap noted in pacenotes).
  Monday remains: VERSION bumps + staging→main (rall-94fc), then nitpicker.
- **2026-07-10 (session 13 cont., ~09:00 UTC)** — pacenotes v1 BUILT and
  LIVE: Sol implemented full spec (4k lines, 25 tests) in one lap; chief
  acceptance found+fixed 2 real bugs (init silently repointing machine config
  → --force guard; git() TrimSpace mangling first porcelain line → manual
  note removals refused; gitRaw + regression tests). Binary installed,
  claude+codex adapters installed (hooks merged, settings keys preserved),
  store live at ~/.local/share/pacenotes with 7 notes, data remote LOCAL
  bare (~/.local/share/pacenotes-remote/) pending GitHub repos (PAT
  blocker). Two-writer sync verified end-to-end. Sol-chief dispatch contract
  now: prepend `pacenotes brief` output to launch briefs.
- **2026-07-10 (session 13 cont., ~08:10 UTC)** — LANDED: rover v0.6.0
  released (azure-cred isolation via AZURE_CONFIG_DIR + rover login/logout;
  tmux-on-ssh default with --no-tmux; shellcheck SC2016 fix-forward — NOTE
  rover's `just lint` does not run shellcheck, CI does). rally TUI config
  surface landed on dev (Sol chief, 3 commits 0b8092f/9f90dec/e188db3):
  targeted comment-preserving TOML writes, Config tab (routes/reasoning/
  providers/custom roles), archguard `roles`->app only; chief re-ran gates +
  real-config tmux acceptance (only intended byte changed). thenn laps 1-2
  done (graceful no-systemd errors verified black-box; verify flagged
  interval timers lack Persistent=true → relay working follow-up then-e79d).
  pacenotes: design consult (Sol xhigh) adjudicated — edit-in-place, no
  SQLite, flat ULID notes/, brief-on-demand; repo scaffolded locally,
  implementation lap running. BLOCKER for user: gh PAT cannot create repos
  (403) — need pacenotes + pacenotes-data private repos or a token bump.
- **2026-07-10 (session 13, user brief, 07:10 UTC)** — Expanded mission (see
  above). Wake timer rebuilt: in-session cron (5h) + scheduler v2 (a9f06f7,
  interactive-in-tmux, watchdog-only, running pid in
  ~/.local/state/crew-chief/). GPT-5.6 family added to rally config
  (sol/terra/luna verified live via codex). Dispatched in parallel: mbtw Sol
  chief (P0 bugs), rally-TUI Sol chief (config-from-TUI), rover relay (3
  laps: azure-creds/tmux-ssh/verify, dev), thenn relay (2 laps: job
  no-systemd hardening/verify, new dev branch), memory research (codex; the
  Claude research subagent died twice to API errors). Fresh dev binaries
  installed: rally v1.0.0-dev @2086a94, laps 1.0.0-dev.62d4ae9. Chief
  requirements for pacenotes written (scratchpad). Rally friction log: `rally
  start` on uninitialized repo requires manual `rally init` + committing the
  queue; backgrounded `rally start | tail` buffers all output (use
  .rally/state + summary.jsonl to monitor instead). — queue lap 14 (rall-dbfb
  dogfood + staging test-drive) DONE. Session 11 (15:43) died at 15:53 to an
  API connection error mid-test-drive, leaving good uncommitted artifacts:
  fresh dev binaries installed (rally v0.13.0-dev, laps 1.0.0-dev.8ad0098),
  manual drives for laps/multi-harness/resume/config, SKILL.md slug refresh.
  Chief verified all of it from relay records + logs rather than redoing:
  round-robin op→cx alternated, laps queue drained with recorded_laps,
  resume relay resumed. Filled gaps: real-backend suite 7/8 (agy fail is the
  documented env auth issue), config-validation outputs, weighted mix op:2,
  tail/progress/instructions, full build+vet+test green. PASS recorded in
  tmp/session-handoff.md (gitignored, intentional). Promoted BOTH repos:
  rally staging 8d30bce→2c2df59, laps staging da519c2→8ad0098 (ff, pushed).
  FINDING (recorded, not fixed): single-runner retry exhaustion ends relay
  with end_reason "config_error" (relay_route_wait.go:67) — misleading label,
  consumer-facing contract, deferred to nitpicker lap rall-c3a8. Next head
  lap: rall-94fc (coordinated v1.0.0 release).
- **2026-07-06 (session 10, autonomous, 10:43 wake)** — queue lap 12
  (rall-03b6 rally TUI laps tab) DONE (11a6ad0). Session 9's continuation
  died at 06:29 to a USAGE LIMIT (resets 10:40 UTC — new death mode, not the
  turn-end kill), leaving a near-complete uncommitted laps-tab diff. Chief
  reviewed and finished it: fixed activeStint decode (object, not string),
  assignees fixture shape, pinned `laps list --root` (transparent stint
  descent silently dropped the root queue + gate from the tab — found only
  via live pty check, unit fixtures were blind to it), added 10s fetch
  timeout. Full gates + pty+pyte live verification (held/ready/complete)
  green. THEN queue lap 13 (rall-df75 laps TUI) DONE (laps dev 9840ddd,
  pushed): codex built `laps tui` (view + done/delete/reorder/hold actions
  via self-exec, consumer-contract JSON reads) in one clean lap; chief fixes:
  "tui" missing from isKnownCommand (hook-only intercept swallowed the
  command — pty e2e caught it, package tests could not), delete-confirm key
  leak (cursor move between x and y retargeted the delete). Live pty e2e:
  release + done actions mutated a real queue correctly. Laps CI then failed
  on its strict gocritic gate (lint not installed locally) — fixed forward
  (8ad0098, pointer-receiver model; golangci-lint now installed, see skill
  references), CI green both repos. Next head lap: rall-dbfb (dogfood in-dev
  rally+laps; staging test-drive).
- **2026-07-06 (session 9, autonomous, 05:43 wake)** — queue lap 11 (rall-f734
  rally adopts laps v3) DONE (0e89c50). Session 8 (00:43) DIED mid-codex-lap
  AGAIN — it ended its assistant turn while codex ran in background; harness
  killed both (headless `claude -p` does NOT survive turn end; block in-turn
  until codex exits). Recovered its in-flight source diff (it was correct and
  complete), re-delegated the 12-file test migration to codex (8 new tests,
  187k tokens, clean), chief-fixed the hardcoded version-warning test + added
  queue-state-aware `rally start` end lines. MinLapsVersion 0.8.1→1.0.0; CI
  builds laps from dev with injected version until v1.0.0 tags (re-pin at
  rall-94fc). Verified live: exit codes 10/11/12, JSON+stint-scoped claims,
  stint descent (`-f stints/<name>.laps` is the file spelling), real codex
  relay completed a stint-served lap end-to-end. Next head lap: rall-03b6
  (rally TUI laps tab).
- **2026-07-05 (session 7, autonomous, 19:43 wake)** — recovered session 6's
  orphaned queue tick (2545aa6), then queue lap 10 (rall-953d laps stints
  readiness review) DONE. Laps dev is READY: build+full suite+vet green, CI
  green, Explore sweep found no spec-vs-impl gap in stints; chief smoke-tested
  e2e (stint descent, hold/release, exit codes 0/10/11/12, claim JSON, nested
  enqueue not supported — root-only by design). Gaps fixed on laps dev
  (da489f9, pushed): README --oneline example was stale, added Consumer
  contract section + version-gating rule (v3 tasks 2.1-2.3), task ticks.
  DECISION: no 0.9.0 tag — v3 line ships as the coordinated v1.0.0
  (rall-94fc); MinLapsVersion targets 1.0.0; dev companion must be built with
  ldflags-injected version (git-describe reports 0.8.1-N-g<sha> → parses
  0.8.1, fails the floor). Rally adoption surface confirmed for rall-f734:
  ReadClaim bare-id parse + QueueSize line-count are the two breaks;
  parseLapOutput already strips the v3 undo footer.
- **2026-07-05 (session 6, autonomous, ~15:20 continuation)** — queue lap 9
  (rall-3f79 staging pipeline) DONE (1413cb2, pushed): staging branch created
  at main tip, AGENTS.md branch-pipeline section, rally-release v0.4 with
  test-drive gate, test.yml runs on staging. Session died AFTER `laps done`
  but BEFORE committing the queue tick + log (THIRD session-death occurrence;
  this one cost only housekeeping — session 7 recovered it). Next head lap:
  rall-953d (laps stints readiness review, chief-owned).
- **2026-07-05 (sessions 2–5, autonomous, condensed)** — queue laps 5–8 DONE
  (harden-outing-failure-handling phase A+B, all on origin/dev, CI green
  through 2984c3e): rall-3ebf telemetry QA (evidence from local state — NR
  has no creds in this container; draft.md scoped laps 6–8), rall-178b
  unauthenticated-harness handling (chief added AuthScope: antigravity auth
  is account-wide, not per-quota; glog Message overwrite restricted to auth),
  rall-2730 recovery fallback on repeated retries (chief split tests to
  respect the 1069-line archguard budget), rall-7fe9 codex sidelining +
  relay end reasons (chief reverted EndedAt-on-cancellation — breaks relay
  resumability; added EndReasonDiscarded; root-caused the corpus records to
  stall-recovery stale fail_reason). Sessions 2 and 4 both DIED mid-codex-lap
  (keep the session alive until codex exits — review in-turn while it runs,
  which worked in session 5). **OPERATIONAL: agy is UNAUTHENTICATED here**
  (OAuth interactive-only; can't self-heal until user returns Jul 8); `ag:*`
  routes burn ~30 s/try; senior lists `ag:opus` first.

## Prune rules (anti-snowball)

- Session log: keep newest ~5 entries.
- `openspec/changes/archive/` and next-up.md "Done": keep newest ~5 archived
  changes; older live in git history.
- crew-chief skill `references/`: newest file is authoritative; fold stale
  notes forward rather than stacking files (keep ~3).
- This doc stays under ~120 lines. If a section outgrows that, the content
  belongs in an openspec change or the queue, not here.
