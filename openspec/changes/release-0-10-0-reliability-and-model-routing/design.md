# Design: 0.10.0 Reliability, Routing Semantics, and CLI Visibility

## 1) Lap pin mismatch as warning + handoff-only for this release

### Current behavior

- `validatePinnedLap` sets `lapPinMismatch=true` and marks the attempt as failed.
- this feeds `issueWorthy` and can emit Sentry/failure pathways.
- `runOne` currently exits with a terminal failure path and telemetry tags indicate an error-class outcome.

### 1.1 target behavior

- Keep mismatch reason capture (`wrong_lap_consumed`, `multi_lap_consumed`) for diagnostics.
- Route outcome should be warning only:
  - remove lap mismatch from issue-worthy capture for now
  - keep as non-terminal mismatch signal for scheduler handoff and operator visibility
  - preserve retry budget behavior (no extra in-run retries)

### 1.2 implementation steps

1. In `runOne`, split `lapPinMismatch` handling from failure taxonomy:
   - do not force operator escalation on mismatch.
   - keep fail/failure record (`FailReason` includes mismatch reason) for audit.
   - keep it outside `FailureCategory`; 0.9.x reserves failure categories for
     `OutcomeFailed` causes, while mismatch is a warning diagnostic.
2. In mismatch branch, allow normal route fallback as the “next action” by leaving scheduler state as failed but not escalating as infra/agent failure.
3. Add optional post-run state preflight before next cycle:
   - if pinned lap already completed elsewhere, mark the current run as complete and route next.
   - if a different single lap appears active/recorded incorrectly, keep warning path but still advance route.
4. Update telemetry tags/comments:
   - include `event_kind=lap_pin_mismatch` and
     `mismatch_reason=wrong_lap_consumed|multi_lap_consumed` tags.
   - do not attach `failure_category` to mismatch-only warning events.
   - include warning text in run log and run summary.

### 1.3 docs and tests

- Add tests for warning-only behavior and unchanged retry behavior.
- Regression test should keep previous short/no-op mismatch coverage and only remove escalation condition.

## 2) Run indexing wording and role-aware headers

### Current behavior

- `style.RenderHeader` uses `[run/total]` without explicit labeling for non-laps and shows model line separately.

### 2.1 target behavior

- Non-laps header renders something equivalent to: `run: 2/18`.
- Header should prefer role and runner context:
  - `VERIFY: codex - g55-xh - started 16:40`
  - then include attempts inline where useful (`attempt N`).

### 2.2 implementation steps

1. Add `RoleLabel` to `style.HeaderOptions` and include it in render.
2. Change non-laps counter formatting from bare `[N/M]` to `run: N/M`.
3. Ensure tests in `internal/style/style_test.go` are updated from bracket token assertions to `run:` assertions.
4. Update run context in `runner` header call to pass role when available (`task.Assignee`).
5. Keep lap header behavior as-is for lap-backed paths (title + `laps: X/Y`) to avoid regressing laps semantics.

## 3) Cancelled state for operator-controlled exits

### Current behavior

- Ctrl+S skip cancels the current attempt and currently reaches normal executor error handling, so the try can render as `failed: harness error`.
- Quit-now and any graceful-stop path that cancels/drains a running attempt can similarly inherit process/context cancellation as a harness failure.
- Failed styling and failure telemetry are misleading for these paths because the operator, not the harness, chose the outcome.

### 3.1 target behavior

- Add a first-class cancelled outcome for attempts/runs:
  - source values: `skip`, `graceful_stop`, `quit_now`
  - display label: `cancelled`
  - styling: muted/grey via the existing style layer
- Ctrl+X graceful stop changes semantics in this release: after the operator
  requests graceful stop, Rally should cancel/drain the active attempt, persist
  it as `cancelled` with source `graceful_stop`, and then halt without starting
  another run. This supersedes the current "let the active try finish naturally"
  behavior.
- Cancellation source overrides normal executor exit handling after the attempt drains:
  - do not classify as `harness error`
  - do not run failure taxonomy or retry classification
  - do not increment retry, infra, pause, or freeze counters
  - do not emit Sentry failure captures
  - do not synthesize an `incomplete_finalization` stub/failure capture for the
    cancelled run after the attempt loop.
- Persist cancellation status and source in try/run records so summaries, tail context, and future resume logic do not need to infer it from text.

### 3.2 implementation steps

1. Add a typed cancellation source to the action loop result (for example
   `CancellationSourceNone|Skip|GracefulStop|QuitNow`) instead of inferring from
   `actionTaken`, `skipFlag`, and `stopFlag`.
2. Track explicit operator action on the runner when Ctrl+S, Ctrl+X graceful stop, or Ctrl+C quit-now cancels the active attempt.
3. After draining `tryCh`, derive attempt outcome in this order:
   - explicit operator cancellation
   - successful completion/finalization
   - lap-pin mismatch warning
   - ordinary executor/failure classification
4. Persist cancellation with concrete store fields:
   - extend the existing 0.9.x `reliability.TryOutcome` enum with `OutcomeCancelled = "cancelled"`
   - store cancellation as `TryRecord.Outcome = OutcomeCancelled`
   - `TryRecord.CancellationSource string json:"cancellation_source,omitempty"` (`skip`, `graceful_stop`, `quit_now`)
   - derive legacy `Completed=false` for cancelled records for backward compatibility.
5. Update downstream consumers so cancelled records do not become failed summaries:
   - `tallyRuns` / final relay summary counts
   - `style.RenderSummary` / footer output
   - recent-try context strings
   - telemetry log fields and failure-capture guards
   - `maybeWriteStubAndClearState` / post-loop unfinalized-run handling
6. Add muted footer/header rendering for cancelled outcomes and ensure summaries print `cancelled` rather than `failed`.
7. Keep skip route semantics unchanged: Ctrl+S advances to the next scheduler candidate, but the skipped try is recorded as cancelled.

### 3.3 docs and tests

- Add unit tests covering Ctrl+S, graceful stop cancellation, and quit-now cancellation overriding a context/process error.
- Add telemetry/store tests verifying cancelled attempts do not produce failure captures, freeze increments, or retry attempts.
- Add style tests verifying cancelled output uses muted/grey formatting and not success/failure colors.
- Add summary/tally tests proving cancelled attempts are not counted as failed runs.

## 4) Reasoning/variant support with aliases

### Design goal

Keep existing `[routes]` parsing and harness model aliases, and layer role-level variant defaults plus harness-specific effort injection where the CLI supports it.

### 4.1 configuration

Add top-level config table:

```toml
[reasoning]
verify = 'g55-xh'
junior = 'g55-l'
```

- Keys are role names (case-insensitive).
- Values are harness-aware reasoning/model preferences. A value MAY be a
  harness-scoped model alias such as `op:g55-xh` / `cc:opus-high`, or a
  harness-supported effort token applied to the route-selected harness.
- Bare model aliases are resolved only after the selected harness is known; the
  resolver must not treat `verify = "g55-xh"` as a global alias.
- Existing `[defaults.*_model]` and `[harness.<name>.models]` continue unchanged.
- Where a role maps to an effort value for the same model, executor-specific injection is:
  - codex: `-c model_reasoning_effort=<value>`
  - claude: `--effort <value>`
  - opencode: `--variant <value>`
  - gemini: unsupported; warn and skip
  - antigravity: unsupported as a flag; use model aliases such as high/low model names
- Unknown effort values should warn, not hard-fail. Missing harness-scoped model
  aliases are validation errors in `rally routes check`, because they are likely
  operator typos. The spike confirmed claude/opencode ignore many unsupported
  effort values and codex ultimately rejects invalid values at API time.

### 4.2 runtime resolution

1. Keep parsed route entries available until a concrete task/role is selected, or add a second role-aware resolution step after `ActiveRoute(task.Assignee)` chooses the route in `routeRuntime.next`.
2. Apply role-level variant fallback only after the selected route entry's harness is known and only when the entry has no explicit model token.
3. Preserve explicit route models with higher precedence.
4. Resolve role fallback to a named model alias in the selected harness before accepting raw model strings.
5. Allow the resolver to return both the selected model and optional reasoning/variant effort so each executor can inject it using its own mechanism.
6. Propagate the optional effort through `agent.ResolvedAgent`, `agent.RunOptions`, `routeSelection`, `relay.Config.Resolver`, and each executor's argument builder.
7. Add clear errors for unresolved harness-scoped model aliases and include role name in diagnostics.

### 4.3 compatibility

- Support existing built-in aliases (`cc`, `cx`, `ge`, etc.) unchanged.
- Existing `--agent` override behavior remains unchanged unless a role context can be inferred from route mode.

## 5) Tail reliability and live behavior

### Current behavior

- `rally tail --try 0` selects `AllTries()[last]`, which is stale while a run is in-flight.
- On a fresh workspace with only an in-flight try, `rally tail` errors with `no tries recorded in this workspace` because the active try is not appended to the store until the executor returns.
- The active log file exists and is written incrementally, but `run-state.json` currently has only run/lap bookkeeping and no active try/log pointer.

### 5.1 target behavior

1. Keep explicit `--try N` behavior.
2. For `--try 0`, pick the current active try/log when available.
   - prefer run-state metadata indicating an active run/session and its latest try path, when present.
   - fallback to newest try only when no active run is present.
3. If active target points at missing path, fall back to newest completed try with warning output.

### 5.2 syntax highlighting options

- Add optional `--highlight` mode on tail:
  - `off` (default): existing plain copy
  - `heuristic`: token-aware coloring for errors, JSON-like lines, timestamps, commands, URLs
  - `chroma` (optional): rich coloring behind dependency path when enabled and available

### 5.3 persistence/integration

- Extend progress/run-state with `active_relay_id`, `active_run_id`, `active_try_id`,
  `active_log_path`, and `active_started_at`, written at try start before the
  executor runs and cleared after the try is recorded.
- Clear active try metadata field-wise only through a helper that preserves
  `RunID`, `PinnedLapID`, `RecordedLaps`, `LapsAttempted`, `HandoffState`, and
  `SessionID`. Active-tail cleanup must not call `progress.ClearRunState`.
- Treat active metadata as stale when it belongs to no unfinished relay/run, when
  `active_started_at` is implausibly old, or when the active log path is missing;
  stale metadata produces a warning and falls back to newest completed try history.
- Add tests around active-run tail selection when both active and completed streams exist.

## 6) opencode usage-limit detection and reset parsing

### Current behavior (spike-2)

- `reliability.ParseOpencodeError` (`internal/reliability/opencode.go`) enters
  the `usage_limit` branch only on opencode-native tokens (`usagelimit`,
  `quotaexceeded`, `resourceexhausted` in the name; `usage limit`,
  `quota exceeded`, `resource_exhausted` in the message).
- Real subscription-provider limits arrive as Vercel-AI-SDK wrappers
  (`AI_APICallError`, `AI_RetryError: Failed after 3 attempts. Last error: …`)
  surfaced under opencode's catch-all `UnknownError`, whose `data.message` is
  often the generic "Unexpected server error. Check server logs for details."
  So the failure falls through to the `agent_error` default and the quota scope
  is never benched. Verified in Sentry: `RALLY-Q` (`opencode:zai-coding-plan/
  glm-5.2`), `RALLY-K`/`RALLY-D` (`opencode:opencode-go/*`) all `agent_error`.
- opencode retries the provider error internally before emitting anything to the
  `--format json` stream; live reproductions emitted **zero stdout** for 180s+
  (a 320s run was still silent at 7 min). Rally's `DefaultStallThreshold` is 180s
  (`internal/reliability/stall.go:16`) and opencode has no liveness probe, so the
  try is usually stall-killed before the limit text is ever produced.
- Reset timing does not parse: `parseResetsIn` reuses
  `geminiResetsInRe = Resets\s+in\s+(\S+)`, so "Resets in 7 days" yields the
  bare token "7" → `parseGoDuration("7")` fails → 0; "Your limit will reset at
  2026-06-16 18:29:51" is an absolute timestamp with no "resets in" phrasing. So
  `benchResetDeadline` (`internal/relay/runner.go:1187`) falls back to
  `BenchDefaultDuration = 5h` even for a monthly/weekly limit.

### Live signatures (opencode server log)

Confirmed third-pass (`opencode.log`, 2026-06-16 20:58) the error is a **flat
field** on a single line — `error.error="<Wrapper>: <message>"` — not a nested
JSON object, and it appears only in `opencode.log` (not the per-run timestamped
`*.log` files):

```
timestamp=…Z level=ERROR run=… message="stream error" providerID=zai-coding-plan \
  modelID=glm-5.2 session.id=ses_… small=false agent=build mode=primary \
  error.error="AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51"
```

```
opencode-go : AI_APICallError: Monthly usage limit reached. Resets in 7 days. …
              AI_RetryError: Failed after 3 attempts. Last error: Monthly usage limit reached. Resets in 7 days. …
zai-coding-plan : AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51
                  AI_RetryError: Failed after 3 attempts. Last error: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51
```

Each session emits the limit twice — `small=false agent=build` and `small=true
agent=title` (opencode's title-generation small model) — and opencode logs a
`message=created … id=ses_… directory=<dir> …` line at session start that carries
the workspace directory (the correlation handle for 6.1.3).

### 6.1 target behavior

1. **Broaden classification.** Match the usage-limit signatures across the
   `AI_APICallError` / `AI_RetryError` / `UnknownError` wrappers and against
   `name`, `data.message`, and the flat server-log `error.error="<Wrapper>:
   <message>"` value (confirmed third-pass: this flat field, not a nested
   `data.message`, is the authoritative carrier). Add substrings `"usage limit
   reached"`, `"monthly usage limit"`, `"usage limit reached for"`. Keep
   `usage_limit` winning over `short_rate_limit` when both could match.
2. **opencode reset parsing.** Add reset parsing for space-separated spans
   ("Resets in 7 days" / "… 5 hour" / "… 30 minutes") and absolute timestamps
   ("reset at 2026-06-16 18:29:51", "will reset at …"), feeding `ResetAfter` /
   `ResetAt`. Do not overload the gemini regex. The absolute timestamp has **no
   timezone marker** (opencode's own `timestamp=` is UTC, but the message reset is
   local); parse it in `time.Local` and treat as approximate.
3. **Observability despite internal retry.** When an opencode try ends without a
   usable result (stall kill or error event), have `OpenCodeExecutor` read the
   tail of opencode's server log (`~/.local/share/opencode/log/opencode.log` —
   only this file carries `message="stream error"` lines) and surface any provider
   usage-limit signature as `FailureEvidence`. Correlate the session **without
   stdout** (empty during the stall): match `message=created … directory=
   <WorkspaceDir>` to recover the `session.id=`, then scan that session's
   `level=ERROR message="stream error"` lines (either `agent=build` or
   `agent=title`); fall back to `providerID=<provider>` within the try window, and
   narrow by `--session` id when already known. This reliably carries the
   structured provider error that the JSON stream withholds. (Alternatives
   considered: treat a silent-backoff stall on a subscription provider as
   usage-limit-suspected — rejected as too coarse; shorten opencode's interim
   error exposure — out of Rally's control.)
4. **No bench-side change.** `routing.QuotaScope` already keys
   `opencode:zai-coding-plan` / `opencode:opencode-go`, and the runner benches on
   `Category == CategoryUsageLimit` (`runner.go:794`). Fixing 1–3 is sufficient.

### 6.2 generality

Any harness whose CLI retries provider errors internally (codex/claude/gemini)
can mask a usage limit behind a stall the same way; the observability fix (6.1.3)
is the general mitigation. The codex/claude parsers already cover their own limit
phrasings when the text reaches stderr within budget, so no new per-harness
classification is required beyond opencode for this release.

### 6.3 docs and tests

- Add opencode fixtures for the exact zai and opencode-go signatures (both
  `AI_APICallError` and `AI_RetryError` wrappers, plus the `UnknownError`
  generic-message case) asserting `CategoryUsageLimit` and correct
  `ResetAfter`/`ResetAt`.
- Add a server-log-tail evidence test for the stall-then-limit path, including
  session correlation by `directory=<WorkspaceDir>` (provider+window fallback).
- Finding A resolved (third-pass live log re-inspection, 2026-06-16 20:58): the
  structured error never reaches stdout; it is carried only as the flat
  server-log `error.error="<Wrapper>: <message>"` field under `AI_APICallError` /
  `AI_RetryError`. The matcher list is finalized and the server-log-tail path is
  required, not optional. See `spike-2-report.md` §"Third-pass confirmation".

## 7) Rollout plan and review

This release is intentionally staged:

1. Core mismatch warning behavior and run/header updates first.
2. Cancelled state plumbing and muted output for operator-controlled exits.
3. Reasoning alias/config path with compatibility checks and executor-specific injection.
4. `tail` active-run detection and highlighting modes.
5. opencode usage-limit detection, reset parsing, and server-log-tail evidence.

### Resolved review decisions

1. Missing harness-scoped model aliases are errors in `rally routes check`; unsupported effort values warn and skip/fall back.
2. Run headers include model on the same line as role/harness.
3. Richer syntax highlighting stays opt-in for this release; plain output remains default.
4. Ctrl+X graceful stop cancels/drains the current attempt and records source `graceful_stop`.
5. Lap mismatch diagnostics use telemetry `LevelWarning` but do not capture Sentry Issues by default.
6. Cancelled attempts extend the existing `TryOutcome` lifecycle model rather
   than introducing a parallel try status field.
