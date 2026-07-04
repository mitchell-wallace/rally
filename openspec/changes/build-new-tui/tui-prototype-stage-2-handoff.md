# TUI prototypes — stage 2 handoff (testing & refinement)

Written 2026-07-04 at the end of stage 1 (~60% of a 5h Claude usage session).
Budget for this stage: roughly the same again. You are the orchestrator: keep
architecture, spec-writing, review, and commits; delegate implementation and
repetitive testing to GPT-5.5 via `codex exec`. Read
`prototypes.md` in this folder first — it is the architecture doc and progress
log, and stays the single running log (append to it; this handoff is static).

## State at handoff

Branch `feat/tui-prototypes` (not pushed), 8 commits `5a0825e..ea0e88c`, clean
tree, all 35 test packages green, archguard clean. Three working prototypes:

- `rally tui-1` (tuisafe): safe alt-screen transpose. Live + `--demo` +
  `--view [N]`. Live-tested with a real op:zai relay (passed, real commit).
- `rally tui-2` (tuipanels): single-tab feed+detail panels. Live + `--demo`.
  Live-tested (relay #2, passed, seeded history rendered).
- `rally tui-3` (tuitabs): tabs Dashboard/Transcript/Messages/Agents. `--demo`
  PTY-verified; live path shares tui-1/2 session mechanics but has NOT run a
  real relay yet.

Test workspace with real store data (2 relays) survives at
`<scratchpad>/live-ws` only if the same session's scratchpad persists — assume
it doesn't and recreate (recipe below).

## How to drive (mechanics that are known to work)

- Delegate with: `codex exec --dangerously-bypass-approvals-and-sandbox -C
  /workspace - < spec.md` (bwrap namespaces unavailable here; the default
  sandbox fails). Write full lap specs to scratchpad files; each spec must
  carry: context pointer to prototypes.md, explicit deliverables, verification
  gates (`gofmt -l`, `go build ./...`, targeted `go test`, `go run
  ./tools/archguard`, `go vet ./...`), "do NOT commit", file boundaries when
  parallel, and a report format. Review and commit yourself, one commit per
  lap.
- Parallel codex agents only across disjoint file sets. Expect and welcome
  boundary-blocked reports (an agent stopping because the fix lies outside its
  writable set); make those edits yourself.
- PTY test harness: Python `pty` + `TIOCSWINSZ` (bubbletea exits instantly on
  a 0×0 pty) + `pyte` (pip-installed) to render final frames. Working examples
  are embedded in the stage-1 session transcript; the pattern is ~30 lines and
  cheap to rewrite. Drive keys by writing bytes to the master fd.
- Live relay recipe: temp git repo → `rally init` → `rally tui-N -i 1 -a
  op:zai "<small concrete task>"`. op:zai (glm-5.2) finishes trivial tasks in
  ~30s. `op:dsff` is a free fallback. No antigravity CLI on this box. Detect
  completion by polling `.rally/state/relays.jsonl` for `ended_at`.

## Stage-2 priorities (in order; drop from the bottom if budget runs short)

1. **Polish the known nits** (small codex lap): tuipanels "1 files"
   pluralization; free-run rows titled "relay run" → derive from task prompt or
   summary first line; seeded-vs-live title consistency; tuisafe stale status
   line already fixed — check tuipanels/tuitabs clear theirs on done too.
2. **Interactive-control testing under PTY, live** (the least-tested surface):
   during a real relay send ^S^S (skip), ^P^P + Enter (pause/resume), ^X^X
   (graceful stop), ^C^C (quit) — verify runner behavior matches plain-CLI
   semantics and the status bar shows arm/act hints. Do this at least for
   tui-1; spot-check tui-2. This exercises the ControlSource bridge for real.
3. **tui-3 live relay** (same recipe): verify tab switching during a live run,
   events fanning out to dashboard+transcript, messages/agents tabs seeded
   from the real store (`rally` has message APIs — seed by adding a message
   record or accept empty-state rendering check).
4. **Laps-backed multi-run relay** (closest to real usage): create a `.laps`
   queue (see laps CLI / prepare-laps skill) with 2-3 trivial laps and run a
   relay through tui-1 and tui-2 — verifies lap titles, laps X/Y header line,
   handoff footers, and multi-run feed behavior. Include one lap designed to
   fail (e.g. impossible instruction + low retry budget) to see retry/interim
   footers live.
5. **Edge rendering sweep** (codex lap with model-level tests + a few PTY
   runs): narrow terminals (40-60 cols), tall/short, resize mid-run (send
   SIGWINCH/TIOCSWINSZ change), very long lap titles, unicode/emoji in
   summaries, 500+ line transcripts (scroll performance).
6. **--view for tui-2/tui-3**: synthesis already exists
   (`internal/cli/tui_view.go`); feed events to a RunFeed instead of a
   Transcript. Cheap, high value for reviewing past relays.
7. **--view fidelity**: persist `Model` and `CommitTitle` on TryRecord (small
   runner/store lap + migration-free additive JSON fields) so replay matches
   live output. Only worth it if --view is convincing.
8. **Selection writeup**: fill the comparison table in prototypes.md with
   observed evidence and recommend the bare-`rally` TUI. Do not wire bare
   `rally` yet; do not replace the monitor status line wholesale (Decision 8)
   — that is acceptance work, not prototype work.

## Cautions

- Don't push; don't touch `internal/buildinfo/VERSION`.
- Keep `rally start` byte-identical: any change under `internal/style`,
  `internal/presentation/terminal`, or the runner needs the full test suite +
  a plain-CLI live smoke.
- Archguard tables are mirrored in tests (`imports_test.go`, `deps_test.go`) —
  policy edits must update both.
- Commit `.laps/laps.json` in any laps-backed test repo you keep.
- The known replay gaps (model, commit title, laps total) are documented in
  prototypes.md — don't re-discover them.
