# Session Handoff — Rally Model Slugs + Override Round-Robin Fix

**Date:** 2026-05-11
**Session duration:** ~1.5h
**Branch:** `main` (21+ commits ahead of origin/main; release pending)
**Rally version:** `0.7.4`

---

## Continued from

Previous session handoff (2026-05-10) at `tmp/session-handoff.md` — all work
through commit `3c465f2` ("fix footer showing ✓ passed when agent exits 0 but
made no changes; add custom harness real-backend test; bump to 0.7.3").

---

## Changes made this session

Two commits on `main`:

### 1. `9b051c5` — skill: lock in current model slugs, strengthen progress recording

Test-driving-rally skill updated with:

- Canonical model slug table in section 1 (gemini-3.1-pro-preview,
  gemini-3-flash-preview, opencode-go/kimi-k2.6, **opencode/minimax-m2.5-free**
  — NOT opencode-zen, zai-coding-plan/glm-5.1, claude-haiku-4-5, gpt-5.4-mini).
- Default workflow section: update skill first → read prior state → test →
  fix → verify → bump version → wrap up. Calls out the "never replace
  user-provided slugs with older ones" rule.
- Stronger progress-recording guidance: section 1 slug table is canonical,
  delete stale failure notes rather than stacking caveats, single rolling
  handoff doc, commit skill update separately.
- Deleted obsolete "Gemini: No auth configured" failure note.
- Bumped skill metadata to v1.2.

### 2. `5d13a04` — fix multi-harness override round-robin and trim opencode summary; bump to 0.7.4

**Bug A — override route stuck on first harness.** `rally relay --agent "cc ge op"`
with N iterations always ran claude. Root cause in `internal/routing/override.go`:
`BuildOverrideRoute` parsed bare aliases as `ParsedEntry{HasQuota: false, ...}`,
and the scheduler's `shouldStayOnCurrentLocked` treats no-quota entries as
"stay until failed". The legacy mix path (default `[defaults].mix`) avoided
this because `legacyMixRouteEntries` always stamps a `:N` count.

Fix: inject `HasQuota=true, QuotaMin=QuotaMax=1` for bare direct entries in
`BuildOverrideRoute` so multi-entry `--agent` mixes round-robin. Single-entry
overrides wrap to themselves (same effective behaviour).

**Bug B — opencode minimax summary leading-newline noise.** `minimax-m2.5-free`
emits its answer as several streamed text parts starting with `"\n\n\n"` each.
Concatenation left summaries with ~11 leading newlines. Fix: `strings.TrimSpace`
on combined text in `parseOpenCodeOutput`.

**Regression coverage added:**
- `TestBuildOverrideRoute_BareAliasesRoundRobin` (unit, override builder).
- `TestParseOpenCodeOutput_TrimsWhitespace` (unit, opencode parser).
- `TestRealBackend_MultiHarnessRoundRobin` (real-backend e2e — runs cc/ge/op
  one iteration each, asserts `agent_type` order). Takes ~60s.

---

## Live verification (test-drive log)

All real-backend tests pass (`RALLY_TEST_REAL_AGENTS=1 go test ./internal/relay/... -run TestRealBackend -v -timeout 600s` — 208s total).

Per-model verification on 2026-05-11:

| Model | Time | Result |
|---|---|---|
| claude-haiku-4-5 | ~18s | ✓ |
| gemini-3.1-pro-preview | ~115s | ✓ (last-activity counter ticks from t=0 — gemini doesn't write to its log) |
| gemini-3-flash-preview | ~15s | ✓ |
| opencode-go/kimi-k2.6 | ~18s | ✓ (rate limit cleared) |
| opencode/minimax-m2.5-free | ~14s | ✓ (post-fix summary is clean) |
| zai-coding-plan/glm-5.1 | ~10s | ✓ |
| gpt-5.4-mini (codex) | ~43s | ✓ |

Additional flow checks:

- `rally relay --new --iterations 3 --agent "cc ge op"` → header cycles
  claude → gemini → opencode (fixed in 0.7.4).
- `rally routes check` on `default = ["cc:2", "ge:1", "op:1"]` → 4 iterations
  ran claude, claude, gemini, opencode (correct distribution).
- `rally relay --new` → closes prior unfinished relay, starts fresh.
- `rally relay --resume` → resumes silently at next iteration.
- Interactive prompt (no flag) → `"new"` answer discards previous, `"resume"`
  picks up.

---

## Known gaps / outstanding

### Worth investigating next session

- **Interactive resume prompt exposes internal label.** The prompt reads
  `Unfinished relay #1 is at iteration 1/3 (mix: __override__:cc). …`.
  The `__override__:` prefix is an internal marker for route mode; users
  shouldn't see it. `cmd/rally/main.go:212` — strip the prefix before
  formatting, or store a user-friendly label alongside.
- **`[routes]` config with bare aliases** uses the "stay until failed"
  semantic (enshrined by `TestScheduler_Scenario1_NoQuotas`). This is
  arguably intentional for routes (vs. `--agent`), but worth documenting
  explicitly so users know to add `:1` quotas if they want round-robin from
  config too.
- **`rally version` shows `dev`** when built locally (no ldflags). Release
  builds inject the real version. Consider making the dev binary fall back
  to the `VERSION` file contents instead of literal `"dev"`, so test-drives
  can confirm they're running the right build.

### Not yet covered (carryover candidates)

- Codex multi-harness in `--agent "cc cx"` end-to-end (codex was verified
  solo, but the cross-harness header rotation specifically with cc+cx
  wasn't re-checked this session).
- Gemini complex task (todo app) with `freeze_threshold_secs = 600` — from
  previous handoff, still untested.
- `rally tail --try N` mapping across multiple repos sharing a data dir.
- Custom harness with `kimi`/`zai` model variants (only the bare `mycode`
  with default model was verified).

---

## Files changed (this session)

```
 .claude/skills/test-driving-rally/SKILL.md     | ~80 lines  (skill update)
 VERSION                                        |  0.7.3 → 0.7.4
 internal/agent/agent_test.go                   | +18  (trim test)
 internal/agent/opencode.go                     | +1/-1 (TrimSpace)
 internal/routing/override.go                   | +10  (quota=1 default)
 internal/routing/override_test.go              | +28  (round-robin test)
 internal/relay/runner_real_backend_test.go     | +75  (real-backend rr test)
 tmp/session-handoff.md                         | overwrites prior handoff
```

---

## Notes for next session

- Build with `go build -o /tmp/rally ./cmd/rally/ && export PATH="/tmp:$PATH"`.
  `rally version` will print `dev` — that's expected for local builds.
- All 7 pre-built real-backend tests should pass in ~3.5min. Run them as a
  baseline before manual smoke tests.
- `opencode-go/kimi-k2.6` may be rate-limited any given session; if so,
  `TestRealBackend_OpenCodeRelay` takes ~3min (2m freeze + 1m ctx) and the
  free tier resets ~every 12h.
- The skill's "default workflow" section now codifies the test-drive →
  patch → bump → wrap-up loop. Follow it unless the user overrides.
