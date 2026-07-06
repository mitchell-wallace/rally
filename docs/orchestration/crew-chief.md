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
- Laps source: `/workspace/laps`, dev branch. Installed binary is a dev build
  (`1.0.0-dev.da489f9`, ldflags-injected version); rally MinLapsVersion is
  1.0.0 and rally CI builds laps from dev until laps v1.0.0 tags (rall-94fc).

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

- **2026-07-06 (session 10, autonomous, 10:43 wake)** — queue lap 12
  (rall-03b6 rally TUI laps tab) DONE (11a6ad0). Session 9's continuation
  died at 06:29 to a USAGE LIMIT (resets 10:40 UTC — new death mode, not the
  turn-end kill), leaving a near-complete uncommitted laps-tab diff. Chief
  reviewed and finished it: fixed activeStint decode (object, not string),
  assignees fixture shape, pinned `laps list --root` (transparent stint
  descent silently dropped the root queue + gate from the tab — found only
  via live pty check, unit fixtures were blind to it), added 10s fetch
  timeout. Full gates + pty+pyte live verification (held/ready/complete)
  green. Next head lap: rall-df75 (laps TUI, in /workspace/laps).
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
