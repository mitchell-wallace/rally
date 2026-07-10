# 2026-07-10 — Sol-chief fleet, GPT-5.6, pacenotes (session 13)

Read docs/orchestration/crew-chief.md FIRST. 07-06 file still valid for
laps-v3/TUI mechanics; this folds forward the fleet-operating model.

## Session mode changed: interactive, not headless

This session ran INTERACTIVE claude (no -p) — background tasks survive turn
end and notifications re-invoke the chief. The old "never end turn while
codex runs" rule applies ONLY to headless -p sessions. Scheduler v2 revives
a dead chief in tmux (`crew-chief` session); in-session cron owns cadence.

## Sol-chief pattern (worked extremely well)

- `codex exec --dangerously-bypass-approvals-and-sandbox -m gpt-5.6-sol
  -c model_reasoning_effort=high -C <repo> - < brief.md`, backgrounded.
  Do NOT pipe through `tail` (buffers all interim output).
- Brief shape that worked: mission + read-first list + bounded session scope
  (~2.5h) + method (rally+laps dogfood, review-every-lap) + hard boundaries
  (branches, no-touch paths, binary reinstall ban) + mandatory wrap-up
  (report file in repo tmp/, clean tree, push dev, stdout summary).
- Sol chiefs are trustworthy on scope and produce excellent friction reports;
  design-consult-at-xhigh (pacenotes) was outstanding — have Sol argue
  against the research doc, then chief adjudicates.
- Sizing: mbtw chief landed 3 root-caused P0s + ~20 commits in one session;
  TUI chief landed a full config surface with tmux evidence. One coherent
  campaign chunk per session is right.
- Keep dispatch briefs in repo tmp/ — the Claude scratchpad under
  /tmp/claude-1000 was WIPED mid-session once.

## Usage discipline

- codex ROLLING limit hit ~09:10 UTC after 5 Sol sessions + suite + relays
  ("resets in 3h5m"). Account-wide across sol/terra/luna. Rally classified
  and rotated correctly. Do NOT burn the finite weekly `/reset` on rolling
  windows; pause Sol dispatches and schedule a one-shot cron at reset time.
- GLM-5.2 (op:zai) resets fully every 5h — junior/qa lanes keep moving
  during codex windows; op:dsff is the free smoke-test lane.

## Relay babysitting

- KILL a relay that ping-pongs an environment-blocked lap (thenn: 21 tries
  of "provide a real systemd host" handoffs before chief intervened).
  Prune unactionable laps yourself; rally has no handoff-loop breaker yet
  (post-v1.0.0 candidate, noted in pacenotes).
- Graceful stop (Ctrl+X) CANCELS the in-flight try by design and leaves the
  relay record OPEN (resumable). Fixed the "relay complete" lie (7301b00).
  Pty probes: both C-x presses within the ~3s arm window.

## pacenotes is live

`pacenotes brief --repo <dir>` at session start; add notes for durable
decisions/ops/friction (author claude-chief). Claude hook auto-injects on
first prompt. Data remote still LOCAL pending user-created GitHub repos.
Chief-review lesson from its build: acceptance testing found 2 real bugs
(init config repoint; porcelain first-line mangling) that 25 unit tests
missed — always drive the real binary against a real remote.
