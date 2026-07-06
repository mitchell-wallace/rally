# 2026-07-06 — laps v3 adoption (session 9; sessions 8 died mid-codex)

Read `docs/orchestration/crew-chief.md` FIRST on wake. 2026-07-05 file still
valid for scheduler/queue mechanics; this file folds forward what changed.

## THE session-death rule (4th occurrence — now root-caused)

Headless `claude -p` sessions END when the assistant ends its turn, even with
background tasks running; the harness then kills backgrounded codex. "You will
be notified on completion" does NOT apply across turn end in headless mode.
- Do NOT end the turn while codex runs. Foreground `sleep` is blocked and a
  foreground until-loop gets auto-backgrounded, so: interleave real work
  (pre-review streamed diff via `git diff`, prep next spec/e2e scripts) with
  instant liveness checks (`pgrep -x codex`).
- Commit queue tick + session log IMMEDIATELY after `laps done`, before
  starting anything new — orphaned ticks cost two recovery sessions this
  campaign.
- Recovery pattern that worked: a killed codex leaves a coherent partial diff;
  read it before assuming loss. Session 8's source diff was complete and
  correct — only tests were missing.

## Environment deltas

- `codex exec --dangerously-bypass-approvals-and-sandbox -C /workspace/rally -`
  still the invocation. Test-migration lap (12 files, 8 new tests): 187k
  tokens, ~15 min, near-flawless; honest boundary-blocked report on the one
  out-of-lane test.
- Installed laps is now `1.0.0-dev.da489f9` (dev build, ldflags
  `-X main.version=...`). Plain `just build` in /workspace/laps uses
  git-describe → parses 0.8.1 → fails rally's 1.0.0 floor; always inject.
- Rally CI (test.yml) clones laps dev + builds with injected version.
  Re-pin `go install ...@v1.0.0` at the coordinated release (rall-94fc).

## laps v3 CLI gotchas (cost real debugging time)

- `laps add` needs a position AND `--title`: `laps add tail --title "..."`.
- Stint file spelling for direct ops: `-f stints/<name>.laps` (the file is
  `.laps/stints/<name>.laps.json`). `-f <name>` or `-f stints/<name>` silently
  create UNRELATED files — laps=0 in `stints ls` is the tell.
- Correct gating flow: `stints new X` → `laps -f stints/X.laps add tail
  --title ...` → `stints enqueue X` → `stints hold X`. Held head: exit 10;
  `stints release X` resumes descent.
- Consumer contract (laps README §Consumer contract) is the adoption
  checklist: exit codes 10/11/12, JSON claim, `list --oneline`, transparent
  stint descent. All verified against the real binary + a live codex relay.

## Review catches (chief-side fixes on codex laps)

- Hardcoded version string in a test name/assertion
  (TestVersionWarningRequiresLaps081) — derive from release.MinLapsVersion.
- Operator surface: relay ending on a held gate printed "Relay complete." —
  misleading; added queue-state-aware end lines in app layer (relayEndLine).
  Watch for "log has the truth, stdout lies" gaps on new terminal states.
