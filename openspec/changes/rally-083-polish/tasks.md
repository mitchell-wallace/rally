## 1. Spike — opencode JSON schema (precursor)

- [x] 1.1 Capture live `opencode run "<prompt>" --format json` output for a configured opencode model (use model names from `.rally/config.toml`, e.g. `op:zai`/`op:zgo`; `test-driving-rally` for usage guidance) — see `spike-opencode-json.md` + `spike-evidence/`
- [x] 1.2 Diff the captured event stream against `opencodeJSONEvent` expectations in `internal/agent/opencode.go` (event `type` values, `part.type`/`part.text` shape); record the actual finish/text/tool event schema in the change notes — `text`/`tool` extraction correct; missing `error` branch is the bug
- [x] 1.3 Decide the corrected extraction (which events carry assistant text + completion) and note it for task 3.2 — recorded in `spike-opencode-json.md` (Task 1.3 section)

## 2. Stall / slowing threshold (item 1)

- [x] 2.1 Change the unset default for `stall_threshold_secs` in `internal/config/config_v2.go` from 120 to 900, updating the explanatory comment to record the global-15m rationale and the accepted opencode-idle trade-off
- [x] 2.2 Confirm the slowing indicator in `internal/monitor/monitor.go` derives from the threshold (`0.6×`) so it now warns at ~9m; leave `reliability.DefaultStallThreshold` (180s) as the bare-code fallback
- [x] 2.3 Update/extend tests for the config default and the slowing-indicator window

## 3. opencode safe fallback + summary normalization (items 5, 6)

- [x] 3.1 In `parseOpenCodeOutput` (`internal/agent/opencode.go`), stop emitting raw stdout as `Summary`; on empty/unparseable output return `Completed=false` with a short bounded indicator
- [x] 3.2 Apply the corrected event extraction from task 1.3: ordered `text` / `part.text`, tool counting, `step_finish` completion detection, process exit status, and top-level `error` events with bounded `error.data.message` / `ref`
- [x] 3.3 Add a runner-level final-snippet normalization path so persisted `TryResult.Summary` uses `laps wrapup` summary when recorded, otherwise parsed final assistant / structured summary text, otherwise a bounded tail or explicit no-finalization/error indicator
- [x] 3.4 Ensure retry context, `tries.jsonl`, and `summary.jsonl` use the normalized final snippet consistently; preserve append-only JSONL storage for `summary.jsonl`
- [x] 3.5 Add tests covering opencode no-text/error fallback (no raw dump), wrapup-as-golden-source, parsed-final-message fallback, bounded-tail fallback, and consistency across retry context / persisted records

## 4. Persisted field cap (item 3)

- [x] 4.1 Add a named final-snippet cap constant of 3000 runes and a head+tail truncation helper (reuse/extract the marker style from `buildRecentContext` in `internal/relay/runner.go`)
- [x] 4.2 Apply the cap to `TryRecord.Summary` and `TryRecord.RemainingWork` when writing try records in `internal/store`
- [x] 4.3 Apply the cap to `summary.jsonl` writes (`internal/progress/store.go` `AppendRunEntry`): `RunEntry.Summary`, `HandoffEntry.Summary`, and each free-text `HandoffEntry.Followups` string
- [x] 4.4 Add tests asserting oversized fields are capped to <= 3000 runes with the marker and within-cap fields are written verbatim

## 5. Retry console + run-level tally (item 2)

- [x] 5.1 Add an inline `retry N/M` field to the live status line (`internal/monitor`) driven by the current attempt/budget; remove any per-retry console block
- [x] 5.2 Change the final relay summary tally (`internal/relay/runner.go` ~600-609) to count each run once — pass if it ever completed, fail only if retries exhausted — aggregating over runs, not raw `TryRecord`s
- [x] 5.3 Add tests for the run-level tally (retry-then-success ⇒ 1 pass; all-exhausted ⇒ 1 fail) and the status-line retry field

## 6. gitx gitignore tolerance (item 4)

- [x] 6.1 In `CommitRallyState` (`internal/gitx/git.go`), detect the "paths are ignored by .gitignore" condition on `git add` for `.rally` operational paths and skip that path silently (no `-f`, no error), continuing with remaining tracked paths
- [x] 6.2 Ensure default tracked `.rally` paths explicitly include `.rally/config.toml` and `.rally/summary.jsonl`; do not extend this skip behavior to `.laps/laps.json`
- [x] 6.3 Add a test that a gitignored `.rally` tracked path is skipped without error and other `.rally` paths still commit

## 7. Agent prompt restructure (item 7 + headless)

- [x] 7.1 Rename `internal/prompt` → `internal/user_prompt` and update all import sites
- [x] 7.2 Create `internal/agent_prompt` with `general/` and `roles/` subfolders and a `go:embed` of the `.md` tree
- [x] 7.3 Add `general/finalize.md` (commit + `laps done`/`laps handoff` + up-front `laps wrapup`) and `general/headless.md` (non-interactive; take intent from referenced planning docs)
- [x] 7.4 Migrate `.rally/agents/{junior,senior,ui,verify}.md` content into `roles/*.md` as embedded defaults, stripping the shared finalize block now provided by `general/`
- [x] 7.5 Document and implement prompt composition as a template: shared `general/` snippets are always included, the role slot is filled by on-disk `.rally/agents/<role>.md` when present or embedded `roles/<role>.md` otherwise, then existing task context is appended; preserve explicit `RunOptions.Prompt` override semantics and existing prompt sections for project instructions, role/persona guidance, task name/requirements, inbox/relay messages, previous summary, and recent try context
- [ ] 7.6 Extend `rally routes check` so it always lists detected roles and approximate token count per role prompt, leaves custom prompts untouched, and emits advisory overlap diagnostics when custom role files reference `laps done`, `laps handoff`, `laps wrapup`, or `headless`, printing the embedded finalize/headless snippets for comparison
- [x] 7.7 Add a note to `AGENTS.md` that prompt package naming reflects who is being prompted (`user_prompt` vs `agent_prompt`)
- [ ] 7.8 Add tests for embedded-default loading, on-disk override precedence, composed-prompt inclusion of finalize + headless guidance, preserved `RunOptions` prompt sections, and `rally routes check` role/token diagnostics plus advisory conflict output

## 8. Release wiring & verification

- [ ] 8.1 Run `just fmt` and the full test suite; fix fallout from the package rename
- [ ] 8.2 Bump `internal/buildinfo/VERSION` to 0.8.3
- [ ] 8.3 Manually verify a short relay: confirm no false "slowing" during reasoning, `retry N/M` shows inline, final tally counts runs once, `tries.jsonl`/`summary.jsonl` entries are bounded, and `TryResult.Summary` matches the `laps wrapup` summary when wrapup was recorded
