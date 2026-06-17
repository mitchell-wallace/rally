# Spike 1 Report: Empirical Harness and Tail Behavior Checks

Date: 2026-06-16. All checks performed with real CLIs on this machine and the
New Relic APM dashboard against `moved-by-the-word/rally`. No assumption was
relied on from documentation alone; every row below is backed by a live command.

## 1. Reasoning effort by harness

### Method

For each harness I ran `--help` against the installed binary and then drove the
smallest real invocation, including an **invalid** reasoning value to discover
validation behavior. Versions under test:

- `codex` codex-cli 0.139.0
- `claude` 2.1.178 (Claude Code)
- `opencode` 1.17.7
- `gemini` 0.40.1
- `agy` (antigravity) 1.0.8

### Findings table

| Harness | Reasoning supported? | Mechanism (exact) | Accepted values | Behavior on unsupported/invalid value | Recommended Rally handling |
|---------|----------------------|-------------------|-----------------|---------------------------------------|----------------------------|
| **codex** | Yes | **Config key only** — no CLI flag. Set `model_reasoning_effort` in `~/.codex/config.toml`, override per-invocation via `-c model_reasoning_effort=<v>`. Rally's `CodexExecutor` (internal/agent/codex.go) currently passes only `--model`; it does NOT pass reasoning. | `none`, `minimal`, `low`, `medium`, `high`, `xhigh` (returned by the OpenAI API on rejection) | Codex does **not** validate at config-load; it forwards to the API, which returns HTTP 400 `invalid_request_error … [reasoning.effort] [invalid_enum_value]`. Run fails. | **Config-validation error** is too strict (codex itself doesn't validate). Pass through `-c model_reasoning_effort=<v>` and let the provider reject bad values; warn at Rally config load only if the value is outside the known set. |
| **claude** | Yes | **CLI flag** `--effort <level>`. `ClaudeExecutor` (internal/agent/claude.go) does not pass it today. | `low`, `medium`, `high`, `xhigh`, `max` | Forgiving: prints `Warning: Unknown --effort value '<v>' — ignoring it and using the default effort. Valid values: …` and runs with the default. No failure. | **Silently ignored** by the harness already; Rally should warn at config load but never hard-fail. |
| **opencode** | Yes | **CLI flag** `--variant <v>` (described as "provider-specific reasoning effort, e.g., high, max, minimal"). `OpenCodeExecutor` (internal/agent/opencode.go) does not pass it today. | Provider-specific; help cites `high`, `max`, `minimal`. Verified applied on `opencode/gpt-5.5` (reasoning tokens emitted). | Very forgiving: an unknown variant (`bogus`) and an unsupported model+variant combo (`claude-haiku-4-5 --variant high`) both **run without error or warning**; reasoning tokens stay 0 on non-reasoning models. | **Silently ignored** (best-effort). Warning at most; never error. |
| **gemini** | **No** | No reasoning flag exists in `gemini --help` (0.40.1). Only `-m/--model`, `-p`, approval modes, etc. Reasoning can only be influenced by choosing a different model string. | n/a | n/a — there is no flag to misuse. | Represent as **unsupported**: Rally config should warn that `gemini` ignores reasoning aliases, and skip injection. Do not error. |
| **antigravity** | **No (flag)** | No reasoning flag in `agy --help` (1.0.8). Reasoning level is **baked into the model string**: `agy models` returns `Gemini 3.5 Flash (Low/Medium/High)`, `Claude Opus 4.6 (Thinking)`, etc. `AntigravityExecutor` already selects model via settings.json. | Encoded in model name (`Low`/`Medium`/`High`/`Thinking`). | n/a — no separate flag. | Reasoning aliases for antigravity should **map to a model string** (e.g. `agy:flash-high` → `Gemini 3.5 Flash (High)`) rather than a flag. Treat flag-based reasoning as unsupported. |

### Prior-assumption scorecard

| Assumption (from spike task) | Verdict |
|------------------------------|---------|
| codex supports reasoning effort | ✅ Confirmed (config-key, not flag) |
| claude supports reasoning effort/level | ✅ Confirmed (`--effort`) |
| some opencode models support reasoning effort/level | ✅ Confirmed (`--variant`; takes effect on reasoning models, silently ignored elsewhere) |
| gemini does not expose Rally-usable reasoning effort | ✅ Confirmed (no flag at all) |
| antigravity does not expose Rally-usable reasoning effort | ✅ Confirmed (no flag; reasoning is part of the model string) |

### Commands cited

- `codex exec --skip-git-repo-check -c model_reasoning_effort="bogus_value" "Print exactly: PONG"`
  → API 400 listing accepted values `none, minimal, low, medium, high, xhigh`.
- `codex --version` → `codex-cli 0.139.0`; `codex exec --help` shows **no** `--reasoning` flag.
- `claude -p "…" --effort bogus`
  → `Warning: Unknown --effort value 'bogus' — ignoring it and using the default effort. Valid values: low, medium, high, xhigh, max.`
- `claude --help` lists `--effort <level> (low, medium, high, xhigh, max)`.
- `opencode run "…" --format json --variant bogus` → exits 0, returns `PONG` (silent).
- `opencode run --help` lists `--variant  model variant (provider-specific reasoning effort, e.g., high, max, minimal)`.
- `gemini --help` (0.40.1) — no reasoning/effort/variant flag present.
- `agy --help` (1.0.8) — no reasoning flag; `agy models` shows level embedded in model names.

## 2. Live `rally tail` behavior

### Setup

Built a disposable repo at `/tmp/opencode/rally-tail-test` with a tiny Python
API (`api.py` + `test_api.py`), ran `rally init` + `rally init roles`, then
`rally start -i 1 -m opencode "Add a multiply(a,b) function…"`. Installed rally
binary is **v0.8.10**.

### Observed behavior

1. **`rally tail` and `rally tail --try 0` fail completely during an active run**
   on a fresh workspace:
   ```
   $ rally tail
   Error: no tries recorded in this workspace
   ```
   This is **worse than "stale"**: the in-flight try is not appended to
   `.rally/state/tries.jsonl` until `store.AppendTry` runs *after* the executor
   returns (internal/relay/runner.go:1725). With no prior completed tries the
   store is empty, so `tailTarget` (cmd/rally/tail.go:55-63) picks
   `tries[len(tries)-1]` from a zero-length slice and errors out.

2. **`rally tail --try 1`** fails the same way for the same reason.

3. **The active try's log file DOES exist on disk in real time**, under
   `~/.local/share/rally/tries/<workspace-hash>/try-<N>.log`, plus a
   `try-<N>.netstat.jsonl` liveness probe. The log is created before execution
   (runner.go:1279) and written incrementally by `runLoggedCommand` via
   `io.MultiWriter(&buf, logFile)` (internal/agent/log.go:62-66). So the bytes
   are followable — Rally just can't *target* the active try.

4. **`run-state.json` carries no active-try pointer.** During the live run it
   contained only:
   ```json
   { "run_id": "relay-1-run-1", "handoff_state": 0, "recorded_laps": [] }
   ```
   There is no `active_try_id` or `active_log_path` field. So even an
   active-aware `tail` has no metadata to read today — task 4.1 ("Extend
   progress run-state with active try metadata") is required, not optional.

5. **Follow mechanism itself works.** With a synthetic persisted try I appended
   lines to `try-1.log` in the background and ran `rally tail --try 1`: it
   printed the existing content then streamed `appended-line-2/3/4` as they were
   written (500ms poll, cmd/rally/tail.go:84). `followFile` is fine; the gap is
   purely target selection.

6. **Default `--try 0` picks the newest *persisted* try**, verified by seeding
   two completed try records: `rally tail` returned try-2 (newest), `--try 1`
   returned try-1 (oldest, 1-based).

7. **Output is plain and contextless.** No color, no syntax highlighting, and
   **no run/try/role header** on the streamed lines — the user gets raw log
   bytes with no indication of which run/try/role they're watching.

8. **Slow first byte on opencode.** The active `try-1.log` stayed at 0 bytes
   for 90s+ while `try-1.netstat.jsonl` updated each second — opencode buffers
   its JSON event stream until the model emits. Even with correct active-run
   targeting, live tailing opencode has a perceptible silent lead-in. (The run
   eventually stalled and was killed; this does not affect the tail findings.)

### Header format observed (relevant to tasks §2)

The live run header rendered as:
```
  [1/1] opencode — started 14:06
  model: opencode-go/kimi-k2.6
```
i.e. bare `[N/M]` (not `run: N/M`) with the model on a **separate** line and no
role label. This matches design.md §2's description of current behavior.

## 3. Telemetry evidence

Source: New Relic APM, project `moved-by-the-word/rally`.
Location on all events: Sydney, AU (AEST, UTC+10).

### Issue-level summary

| Issue | Title | Level | Priority | Events | First seen | repo_name | runner | role |
|-------|-------|-------|----------|--------|------------|-----------|--------|------|
| RALLY-4 | relay 1 run 3 try 16 failed: **wrong_lap_consumed** | **error** | High | 1 | 6/16 09:17 | Prayer-app | antigravity:Claude Opus 4.6 (Thinking) | SENIOR |
| RALLY-6 | relay 2 run 1 try 14 failed: **wrong_lap_consumed** | **error** | High | 2 | 6/16 10:47 | Prayer-app | (SENIOR) | SENIOR |
| RALLY-C | relay 2 run 3 try 16 failed: **wrong_lap_consumed** | **error** | High | 2 | 6/16 11:47 | rally | (SENIOR) | SENIOR |
| RALLY-8 | relay 2 run 2 try 15 failed: **multi_lap_consumed** | **error** | High | 2 | 6/16 11:07 | rally | codex:gpt-5.5 | SENIOR |
| RALLY-9 | relay 1 run 3 try 3 failed: **multi_lap_consumed** | **error** | High | 1 | 6/16 11:26 | rally | opencode:zai-coding-plan/glm-5.2 | JUNIOR |
| RALLY-2 | relay 1 run 20 try 37 **provider limit signal: rate limit, waiting 1m** | **info** | High | 17 | 6/12 → 6/16 13:27 | Prayer-app | codex:gpt-5.5 | SENIOR |
| RALLY-3 | relay 1 run 3: agent exited without finalizing | error | High | 1 | 6/16 09:17 | Prayer-app | — | — |
| RALLY-B | relay 2 run 3: agent exited without finalizing | error | High | 2 | 6/16 11:47 | rally | — | — |

### Tag evidence (the core signal)

- **wrong_lap_consumed / multi_lap_consumed events (RALLY-4/6/8/9/C):** tagged
  `level=error` only. They carry the standard relay/run/try tags
  (`relay_id`, `run_id`, `try_id`, `lap_id`, `role`, `runner`, `repo_name`,
  `attempt`, `max_attempts`, `agent_state=active`, `relay_guid`,
  `machine_id_prefix`) but **NO `event_kind` and NO `failure_category` tag**.
  Example (RALLY-4, event `a03e2198`): `lap_id=pray-60f3`, `role=SENIOR`,
  `runner=antigravity:Claude Opus 4.6 (Thinking)`, `repo_name=Prayer-app`,
  `level=error`.

- **Rate-limit signal (RALLY-2, event `fb47c7ef`):** tagged `level=info`,
  `event_kind=limit_signal`, `failure_category=short_rate_limit`,
  `lap_id=pray-a26c`, `repo_name=Prayer-app`, `runner=codex:gpt-5.5`. This
  **exactly matches** the draft's claim and is the proven tag shape for
  non-terminal observability.

### The 2026-06-16 ~11:11 AEST incident

Two distinct replays bracket the window:

1. **rally repo (workspac-c52d, machine `c42afdb411e4`)** — relay started
   2026-06-16T00:47:33Z (10:47 AEST). Produced the multi_lap spike:
   RALLY-8 at **11:07**, RALLY-9 at **11:26**, RALLY-C + RALLY-B at 11:47.
   This is the cluster around 11:11 AEST. These are operators running rally
   *on rally itself* and hitting lap pinning races.
2. **Prayer-app repo (workspac-c52d, machine `96b49aa4c61d`)** — relay started
   2026-06-11T16:04:42Z, still active. RALLY-4 (wrong_lap_consumed, 09:17) and
   RALLY-3 (agent exited without finalizing, 09:17) co-occurred on the same
   run/trace, interleaved with many RALLY-2 rate-limit signals in the span tree.

**"dune-vm":** not present as a literal string in any issue, event title, tag,
or query result (APM query `message:"dune"` → "No events found").
Only `machine_id_prefix` (a hash) is captured; the host name is not. The
identifiable incident around the window is the Prayer-app / rally-repo lap-pin
cluster above; treat "dune-vm" as a host label that New Relic APM doesn't expose.

### Implications for error handling

- The premise behind tasks §1 is **confirmed**: `wrong_lap_consumed` and
  `multi_lap_consumed` are currently `error`-level, High priority, with no
  failure-category taxonomy — they fire operator-grade alerts for what the
  proposal treats as a handoff signal.
- The target shape already exists in-codebase (RALLY-2's
  `event_kind=limit_signal` + `failure_category=short_rate_limit` at `info`).
  Downgrading mismatches to warning + adding `event_kind=lap_pin_mismatch` is a
  proven pattern, not a new one. **Erratum after 0.9.0 recovery/outcome work:**
  do not add `failure_category=lap_pin_mismatch`; `failure_category` is now
  reserved for failed lifecycle outcomes. Use `mismatch_reason` for the mismatch
  detail instead.
- Mismatches co-occur with "agent exited without finalizing" (RALLY-3/RALLY-B)
  and rate limits in the same traces — consistent with the proposal's view that
  they arise in already-messy, transient relay states and should route to the
  next candidate rather than fail the relay.

## 4. Recommended changes to the change artifacts

The spike **confirms** every prior assumption; no assumption was disproved.
The recommended changes below add precision so implementation doesn't have to
re-discover what this spike found.

### draft.md

- Section 4 (Reasoning levels / variants): add a sentence noting the mechanism
  differs per harness — claude `--effort`, opencode `--variant`, codex
  `-c model_reasoning_effort=`, gemini/antigravity unsupported (antigravity via
  model string). State explicitly that unsupported variants are **silently
  ignored** by claude/opencode, so Rally should warn at config load, never
  hard-fail on them.
- "Current signal quality" section: the telemetry evidence is now fully
  verified — no change to IDs, but note that mismatch events currently carry
  **no** `event_kind`/`failure_category` tags (those are to be added).

### proposal.md

- Decision 1 / Decision 4: confirmed. Add the per-harness mechanism note from
  §1 above so the "Config-driven reasoning variants" outcome is unambiguous
  about *how* each harness receives the value.

### design.md

- §3 (Reasoning/variant support): the `[reasoning]` map resolves a role to a
  *model alias* today. That covers antigravity (model-string) and re-selection
  of a different model, but **does not** cover setting claude `--effort`,
  opencode `--variant`, or codex `-c model_reasoning_effort=` on the *same*
  model. Recommend either (a) widening the resolver to emit a
  `{model, reasoning_effort}` pair that each executor injects via its own
  mechanism, or (b) explicitly scoping 0.10.0 to model-alias-only variants and
  deferring flag-based effort to a later release. The current design's example
  (`verify = 'g55-xh'`) implies model-alias-only; confirm that scope.
- §3.1: clarify that for codex the injection is `-c model_reasoning_effort=<v>`
  (not a flag), and for claude/opencode it's `--effort`/`--variant`.
- §4 (Tail): the finding is **stronger** than stated. Update §4 "Current
  behavior" from "selects `AllTries()[last]`, which is stale while a run is
  in-flight" to: "errors with *no tries recorded in this workspace* on a fresh
  workspace, because the active try is not appended to the store until it
  completes." Tasks 4.1–4.4 stand and are the correct fix.
- §4.1: note that `run-state.json` today has only `run_id`/`handoff_state`/
  `recorded_laps`; adding `active_try_id` + `active_log_path` is required.
- §2.2: confirmed the live header is `[1/1] <harness>` with `model:` on a
  second line and no role label — matches the described current state.

### tasks.md

- Task 3.2/3.3: add an explicit sub-task per harness for *how* the resolved
  reasoning value is injected (codex config-key, claude `--effort`, opencode
  `--variant`), or scope the task to model-alias-only as noted above.
- Task 3.x (reasoning): add a validation task — warn (don't error) on
  reasoning values that are unknown for the target harness, since claude and
  opencode silently ignore them and even codex only fails at the API call.
- Task 4.1: confirm the new run-state fields (`active_try_id`, `active_log_path`)
  are written at try *start* (before executor runs), not at completion, so tail
  can target them mid-flight.
- Task 5.2: the issue set is verified — `RALLY-2, RALLY-4, RALLY-8, RALLY-6,
  RALLY-9, RALLY-C, RALLY-B` all exist as listed. (RALLY-3 is the companion
  "agent exited without finalizing" on the Prayer-app run; consider adding it
  to the release-notes incident list.)

## 5. Constraints respected

- No product code was changed; only this report was written.
- Disposable repo used for the live tail test (`/tmp/opencode/rally-tail-test`);
  its synthetic seeded state was cleaned up afterward.
- claude invocation was blocked by a monthly spend limit mid-test, but the
  `--effort` flag-parsing behavior (the only thing this spike needed from
  claude) was captured from the warning output before the limit applied.
