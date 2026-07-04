# 2026-07-04 — TUI prototypes (rally, branch feat/tui-prototypes)

First crew-chief session (skill written from it). Built 3 TUI prototypes in
~60% of a 5h Claude session: 6 delegated laps, 8 commits, all landed green.

## Invocations

- `codex exec --dangerously-bypass-approvals-and-sandbox -C /workspace - < spec.md`
  — WORKS in this container. Default sandbox FAILS: bwrap "No permissions to
  create a new namespace". Smoke-tested with a read-only prompt first.
- Spec files piped via stdin (`-`) from scratchpad; inline-argument prompts
  used only for the first small lap. File+stdin is strictly better.
- codex config: gpt-5.5, reasoning medium (defaults). No overrides needed.

## Lap sizes and durations (gpt-5.5)

- Small seam (config field + plumbing + test): ~4 min, ~75k tokens. Fine.
- Policy tables + new package skeleton + tests: ~8 min, ~72k tokens. Fine.
- Full TUI package + CLI command + refactor: ~15-20 min, ~180-260k tokens.
  Upper bound of comfortable; anything bigger should split.

## Parallelism

- Two concurrent codex agents with explicit disjoint writable sets in both
  specs: worked. One agent correctly STOPPED at the boundary (needed to edit a
  pinned command-list test owned by the other lane) and reported instead — I
  made the one-line edit myself. Declare chokepoints (root.go registration,
  pinned test tables, go.mod) as chief-owned in advance.
- Mid-flight, the other lane's half-finished untracked files broke whole-tree
  builds; committed the finished lane's files only (they were disjoint) and
  gates re-ran clean after both landed.

## Review catches (what gpt-5.5 missed; check these every lap)

- Per-message `go send(msg)` forwarding → event ordering loss. Replaced with
  FIFO single-drainer queue.
- `Start(ctx)` watcher goroutine calling unguarded `Stop()` → a stale context
  could close a *later* session's channel. Fixed with session-identity guard.
- Both were in otherwise high-quality code that passed all its gates. Later
  laps copied the corrected patterns faithfully once they existed in-tree.
- Everything else across 6 laps was spec-conformant; deviations were declared
  and sensible (e.g. adding a TranscriptWriter I hadn't specced).

## End-to-end testing

- TUIs: Python `pty` + `TIOCSWINSZ` (bubbletea exits instantly on 0×0 pty —
  cost 20 min to diagnose) + `pyte` (pip install) to render final frames.
  `script -qec` does NOT work (no real winsize). Drive keys by writing bytes
  to the master fd.
- Live rally runs: `op:zai` (glm-5.2) completes trivial file-task relays in
  ~25-30s. `op:dsff` free fallback. No antigravity CLI on this box. Poll
  `.rally/state/relays.jsonl` for `ended_at` to detect completion.

## Misc

- `openspec-plan-review` skill is a broken symlink in this container (points
  outside the repo) — don't try to read it here.
- Working log lived at `openspec/changes/build-new-tui/prototypes.md`,
  updated and committed at every landed lap; stage-2 handoff written as a
  separate static file in the same folder.
