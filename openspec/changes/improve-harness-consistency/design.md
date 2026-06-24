## Context

Rally classifies harness failures via `reliability.ClassifyError`
(`internal/reliability/patterns.go:299`), invoked once per failed try at
`internal/relay/runner.go:2254`. The function returns a `StrategyDecision`
and uses a strict priority order:

1. Typed executor `Evidence` (Priority 1, line 302)
2. Provider/config/quota detection (Priority 2, placeholder)
3. Dirty-tree incomplete check (Priority 3, line 346)
4. Harness-scoped text patterns from `ErrorPatterns` (Priority 4, line 357)
5. Default `agent_error` (Priority 5, line 387)

Two helpers in `runner.go` populate the `failure_evidence` context block on
the emitted `RallyFailure` event: `applyEvidenceToFailureState` (line 237,
executor path) and `applySafeExecErrorEvidence` (line 256, last-resort
fallback when `execErr != nil`). The runner threads evidence into both
calls via `resetEvidence = result.Evidence` (`runner.go:2258`); both
`applyEvidenceToFailureState` call sites (`runner.go:2454` for the try-log
span/fields, `runner.go:2530` inside the operator-worthy
`ShouldCaptureIssue()` capture path) hard-code the source string
`"executor_evidence"`, so the emitted `failure_evidence.source` tag is
always `executor_evidence` or `safe_exec_error` regardless of which
classifier priority actually produced the category. Priority 3, 4, and most
of 5 leave the context block empty — the decision struct carries
`Category` / `Reason` / `Strategy` / `FailureClass` / `DisplayLabel` /
`Cooldown` but **not** the matching log tail.

Per-harness parsers (`internal/reliability/{claude,codex,antigravity,
opencode}.go`) are correct for the shapes they recognise. New Relic
`RallyDiagnostic` events of form `provider limit signal: …` confirm real
detections across claude (5h + 168h windows), codex (no-reset usage limit),
and opencode (45m reset). The parsers are not the problem; the integration
around them is.

`telemetry.RunnerLabel(harness, model)` (`internal/telemetry/tags.go:26`)
collapses to bare `harness` when `model == ""`. The model is resolved
inside each executor (`CodexExecutor.Model` falls back to `cfg.CodexModel`,
etc.) but never reported back to the runner.

Routing-fallback events are emitted at `runner.go:830` via `EmitTryLog`,
producing `RallyTry` custom events with `from_runner`/`to_runner` populated
but no `outcome`, `attempt`, or `try_id`. 39 such events over 30 days
inflate any `RallyTry`-based failure-rate query.

Codex keeps session logs at `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`
(verified on a host running codex 0.142.0): one JSONL file per session,
newline-delimited structured events. First line is always `session_meta`
(carrying `cli_version`, `cwd`, `git.commit_hash`, `git.branch`,
`git.repository_url`, `model_provider`, full `base_instructions` — note
`model_provider` is the provider slug like `openai`, NOT the resolved model
name; the resolved model lives in later `turn_context` events' `payload.model`
field). Subsequent event types: `event_msg` (subtypes `task_started`,
`task_complete`, `turn_aborted`), `token_count` (~45/file in a 50-file sample
— the verbosity hazard), `response_item` (full messages), `turn_context`
(per-turn model + reasoning effort + the authoritative `payload.model`).
Also present: `~/.codex/logs_2.sqlite` (SQLite database),
`~/.codex/auth.json` (credentials — must never enter telemetry),
`~/.codex/config.toml`.

OpenCode keeps a logfmt log at `~/.local/share/opencode/log/opencode.log`
(~4.9 MB on a live host — verbose). Lines are
`timestamp=… level=INFO run=<run_id> message="…" <kv>`. Structural events:
`message="creating instance"`, `message="created id=<session_id> …
model.id=<id> model.providerID=<id>"`, `message="loop session.id=<id>
step=<n>"`, `level=ERROR`. The `token_count`-equivalent verbosity lives in
per-tool-call and per-permission log lines.

## Goals / Non-Goals

**Goals:**

- Every categorised `RallyFailure` event is self-contained: regardless of
  which `ClassifyError` priority produced the category, the
  `failure_evidence` context block is populated.
- Codex silent-exit failures carry diagnostic context read from codex's
  own session log, and are categorised as `harness_launch` (with the
  `codex_no_session_log` source marker when no session log exists) so the
  failure carries the right category label and repro data instead of
  burning retries as an uncategorised `agent_error`. The existing
  `harness_launch` retry semantics (FreshRestart within budget, infra-class
  freeze pressure after 2+ failures) apply unchanged — the runner keeps
  retrying codex launch failures up to the budget, and the freeze cascade
  caps it when codex repeatedly fails to launch.
- OpenCode try-budget-exhaustion failures carry a bounded disk-log tail
  and are distinguishable from real opencode crashes in NRQL.
- The `runner` telemetry tag always carries the resolved model.
- Routing decisions stop polluting `RallyTry`.
- `gemini` CLI harness is removed for 0.12.0.

**Non-Goals:**

- The larger adapter conformance suite and capability matrix (deferred
  past 0.12.0).
- Rewriting the resilience cascade, the freeze counter, or the
  stall-detection paths.
- Antigravity-via-Claude route policy (a separate routing question; the
  2% completion on 50 tries is not addressed here).
- Historic 0.9.x data cleanup — those failures are already fixed.

## Decisions

**1. Hard-cut `gemini` with no transitional alias.** The upstream CLI's
own auth error tells operators to migrate; a transitional alias that maps
`ge`/`gemini` to `antigravity` would extend the surface we are trying to
cut and would mis-route operators who meant a real `gemini-3.x-pro`
backend. Operators with `[routes] x = ["ge:pro"]` get a one-time
resolution-failure warning pointing them at `antigravity` and the
configuration is otherwise ignored silently. Alternative considered: keep
`ge` as a deprecated alias mapping to `antigravity` for one release —
rejected because the model labels do not line up (`gemini-3.1-pro-preview`
vs Antigravity's free-form `"Gemini 3.1 Pro (High)"`) and the silent
re-routing would mask configuration errors.

**2. Codex session-log fallback, structural events only.** When codex
returns an error and the in-band `out` buffer has no parser-matchable
signal, locate the latest `rollout-*.jsonl` under
`~/.codex/sessions/YYYY/MM/DD/` whose first-line `session_meta.cwd`
matches `opts.WorkspaceDir` and whose `session_meta.timestamp` is within
the try window. Read **only**:

- The first line (`session_meta`) for `cwd`, `git.branch`,
  `git.commit_hash`, `cli_version`, and `model_provider`. (The resolved
  `model` is NOT in `session_meta` — only the provider slug is. It lives
  in `turn_context.payload.model`; the executor's own `model` local at
  `codex.go:170-173` remains the authoritative source for `ResolvedModel`
  per Decision 6, so the session log does not need to provide it.)
- The last `event_msg` line of any subtype (`task_started`,
  `task_complete`, `turn_aborted`) — its subtype is the diagnostic.

Explicitly skip `token_count`, `response_item`, `turn_context` (except as
noted above), and the `base_instructions` payload. Bound the resulting
`RawSignal` to 256 runes using the existing `truncateSignal` helper.
Populate `FailureEvidence` with `Source = "codex_session_log"`, `Message`
from the last event's subtype, `RawSignal` from the bounded structural tail.

If no session-log file exists with a matching `cwd`, codex never got far
enough to do real work: populate `FailureEvidence{Category:
CategoryHarnessLaunch, Source: "codex_no_session_log", Message: "codex
launched but wrote no session log"}` at the executor level, so
`ClassifyError` Priority 1 (typed executor Evidence) picks it up directly
— no runner-side classification change is needed for this case. The
existing `CategoryHarnessLaunch` mapping applies as-is:
`StrategyFreshRestart` (retry within budget with a fresh session) +
`FailureInfra` (after 2+ infra failures the resilience cascade freezes
the runner, which is the right "up to a limit" cap for a harness that
keeps failing to launch). This is the distinct signal that today's
uncategorised `agent_error`-and-burn-retry-budget pattern in the 0.11.2
burst was missing: the win is the correct category label and the
`codex_no_session_log` repro marker in `failure_evidence.source` (and the
session-log tail when available), not a change to the retry/freeze
behaviour. Alternative considered: spawn a diagnostic `codex doctor`
subprocess — rejected because it doubles process overhead on a failure
path; the session log is already authoritative. Alternative considered:
use `CategoryAuthOrProxy` to get `StrategyRotate` + `FailureAgent` —
rejected because the user wants the runner to keep retrying codex launch
failures within budget (FreshRestart), not rotate away on the first
failure.

Note on stderr: `runCodexCommand` (`internal/agent/codex.go:223`) uses the
standard Go merge pattern (`cmd.StdoutPipe()` then
`cmd.Stderr = cmd.Stdout` before `cmd.Start()`), which is the
library-recommended way to merge both streams into one pipe and is shared
by `runLoggedCommand` (`internal/agent/log.go:44`). It is not a race — the
assignment precedes `Start()`, so the child inherits the merged fd. The
real silent-exit-1 problem is that codex writes nothing to *either* stream
before dying on some failure modes; the session-log fallback above is the
authoritative safety net for that case. No `runCodexCommand` refactor is
required for this change.

**3. `failure_evidence` on every classification priority via
`StrategyDecision.Evidence`.** Add an optional
`Evidence *FailureEvidence` field to `StrategyDecision` in
`internal/reliability/patterns.go`. `FailureEvidence` (in
`internal/reliability/category.go`) gains a new `Source string` field so
the runner can emit the correct `failure_evidence.source` tag per priority
without a parallel sidecar. Populate both inside `ClassifyError` for every
branch:

- Priority 1 (executor Evidence, line 302): pass through the executor's
  `Evidence` unchanged; set `Source = "executor_evidence"` if unset.
- Priority 3 (dirty-tree incomplete, line 346): build an Evidence with
  `Category = CategoryIncompleteFinalization`, `Message =
  "agent exited without finalizing"`, `Source = "dirty_tree"`, and
  `RawSignal` from the changed-paths list (the runner already computes
  this via `filesChangedList` at `runner.go:3239`).
- Priority 4 (text patterns, line 357): capture the matching log line at
  match time inside `Pattern.Match` (extend `Match` to optionally return
  the matched line, or have `ClassifyError` re-scan after a match to
  extract it). Set `Source = "text_pattern"`, `Message = pattern.Name`,
  `RawSignal` from the bounded matching tail.
- Priority 5 (default agent_error, line 387): set `Source = "unmatched"`,
  `Message = "no recognised provider signal"`, `RawSignal` from a bounded
  log tail so we always have something.

The runner plumbs `decision.Evidence` into the existing `resetEvidence`
local at `runner.go:2258` (`if resetEvidence == nil { resetEvidence =
decision.Evidence }`), so both `applyEvidenceToFailureState` call sites
(`runner.go:2454` for the try-log span/fields, `runner.go:2530` inside the
operator-worthy `ShouldCaptureIssue()` capture path) pick it up
automatically. `applyEvidenceToFailureState` is updated to read its
`source` argument from `ev.Source` when non-empty (falling back to the
hard-coded `"executor_evidence"` literal only when the caller is passing
genuine executor evidence with no Source set, preserving backward
compat for existing executor-evidence callers). `applySafeExecErrorEvidence`
becomes the lowest-priority fallback that fires only when both
`result.Evidence` and `decision.Evidence` are nil. Alternative considered:
re-parse inside the runner for each priority — rejected because it
duplicates the matching logic and re-reads the log; carrying Evidence
through the decision struct is a single source of truth.

**4. Try-budget-exhaustion classification, distinct labels, no cascade
change.** The runner already distinguishes per-try-cap timeouts from
run-budget timeouts: `loopOut.timedOut && !loopOut.runBudgetExhausted`
identifies the try-cap-only case, and the existing block at
`runner.go:2311` (`if failed && runBudgetExhausted`) already handles the
run-budget case by clearing `failureCategory`, setting
`failReason = "run timeout"`, and forcing `attemptFailureClass =
FailureAgent`. We add the new classification for the **try-cap-only** case
so the two budget kinds stay separately reportable: when
`loopOut.timedOut && !loopOut.runBudgetExhausted` and no Evidence was
produced (neither executor Evidence nor the new session-log / disk-log
fallbacks), set `failureCategory = transient_infra` and `failReason =
"try budget exhausted; no output"`. This maps to `FailureInfra` which
**does** feed the freeze counter today — and that is wrong for budget
exhaustion (the harness did not fail, we killed it). Override the mapping
at the runner call site for this single case: set
`failureClass = FailureAgent` directly so the freeze counter does not
increment, while leaving the category as `transient_infra` for NRQL
filtering. Alternative considered: add a new
`CategoryTryBudgetExhausted` — rejected because it would force a new
telemetry tag value and a new branch in every consumer; reusing
`transient_infra` with a distinct `fail_reason` is enough signal for
NRQL and requires no schema change.

The run-budget path at `runner.go:2311` is left unchanged. NRQL separates
the two cases by combining `fail_reason` with the existing `timeout_kind`
tag on the try log (`runner.go:2468`, which already emits
`timeout_kind = "try_cap" | "run_budget" | "handoff"`) plus the
diagnostic signals `runtime_ms`, `tool_calls`, `files_changed`, and
`last_output_age_ms` that the try log already carries. The "slow model
getting the job done → revise time budget" vs "underqualified model →
review routing" question is answerable today by filtering on those tags
(e.g. high `tool_calls` + small `last_output_age_ms` near the kill =
active work in progress; low `tool_calls` + large `last_output_age_ms` =
stalled or underqualified); the new `transient_infra` +
`"try budget exhausted; no output"` labels add a clean category/reason
filter on top of those signals without duplicating them into
`failure_evidence`.

**5. `RallyRoute` event for routing decisions.** Add
`Sink.EmitRouteEvent(ctx, fields)` and a `RallyRoute` custom event type.
Move the route-fallback emission at `runner.go:830` from `EmitTryLog` to
`EmitRouteEvent`. The event carries `relay_id`, `run_id`, `lap_id`,
`role`, `from_runner`, `to_runner`, `repo`, `repo_name`, and any fallback
cause fields. This keeps `RallyTry` pure (every event is a real try
outcome with a non-empty `outcome` tag) and gives routing decisions their
own schema for NRQL. Alternative considered: keep using `RallyTry` but
always set `outcome = "routed"` and document the filter — rejected
because it leaves a confusing dual-purpose event type and forces every
reporting query to remember the filter.

The recovery-cap-hit path at `runner.go:834` already emits a `RallyFailure`
event (operator-worthy `needs_user`) via `CaptureFailure`, NOT a polluted
`RallyTry` — so it is NOT one of the NULL-outcome `RallyTry` polluters.
For parity with the route-fallback shape, the runner SHALL ALSO emit a
`RallyRoute` event carrying the cap-hit context, while keeping the existing
`RallyFailure` capture intact. The two events serve different audiences:
`RallyFailure` is the operator-worthy alert; `RallyRoute` is the routing
audit trail.

After this change, audit every `EmitTryLog` site (`runner.go:830, 2100,
2574, 3046`) and assert at the telemetry boundary that `outcome` is
non-empty for `RallyTry`. Only the route-fallback site at `runner.go:830`
currently lacks `outcome`; the other three already set it from a non-empty
`TryOutcome`. The audit is the close-the-gap guarantee.

**6. `ResolvedModel` on `TryResult`, runner-tag fallback.** Add
`ResolvedModel string` to `agent.TryResult` (`internal/agent/executor.go`,
where `TryResult` is defined at line 48 — NOT `internal/agent/agent.go`,
which is an empty package file). Each executor sets it to the model
actually passed to the CLI: codex sets it from the `model` local at
`codex.go:170-173` after the `opts.Model` fallback; claude / opencode /
antigravity likewise. At the three telemetry sites that build the
`runner` tag (`runner.go:2107, 2420, 2997`), pass `result.ResolvedModel`
to `telemetry.RunnerLabel` when non-empty, falling back to `picked.Model`
otherwise. The route-resolved model stays authoritative when set; this
is a fallback for the bare-alias case only. Alternative considered:
resolve the default model at route-selection time — rejected because the
executor owns the resolution (config-default vs CLI-default vs
model-alias expansion) and reporting it back through `TryResult` is the
single, accurate source.

**7. Parser rename: `ParseGeminiError` → `ParseAntigravityError`.** With
gemini gone, the function serves antigravity only; the shared name is a
leftover. Rename the function (in `internal/reliability/antigravity.go:23`)
and its tests, and update the caller at
`internal/agent/antigravity.go:106` (the only surviving caller after
`internal/agent/gemini.go` is deleted in Decision 1).

The `ErrorPatterns` table needs a finer touch than a flat "remove both
gemini patterns" — the two patterns have different harness scopes today:

- `gemini auth or unsupported client` (`patterns.go:237-248`,
  `Harness: "gemini"`) is genuinely dead after the cut (no failures will
  carry harness=`"gemini"`). Remove it. The antigravity-scoped duplicate
  at `patterns.go:249-260` (`Harness: "antigravity"`) already covers the
  eligibility-text path and stays.
- `gemini-cli exit 1` (`patterns.go:223-236`) is currently scoped to
  `Harness: "antigravity"` (line 235), NOT `"gemini"` — antigravity
  shells out to the `gemini-cli` binary under the hood, so the pattern
  matches antigravity's exit-1-with-no-other-signal cases and is covered
  by `runner_test.go`'s "gemini exit status 1 resumes retry" assertion.
  Keep this pattern, but rename it (`antigravity gemini-cli exit 1`) so
  the name no longer implies a `gemini`-harness scope. Do NOT delete it.

The eligibility regex (`IneligibleTierError`, `UNSUPPORTED_CLIENT`,
`no longer supported for Gemini Code Assist`) stays unchanged and
continues to apply only to antigravity.

## Risks / Trade-offs

- **Codex session-log path varies across hosts** (`CODEX_HOME` override,
  container layouts) → mitigation: read `$CODEX_HOME` first, fall back to
  `~/.codex`, and treat the session-log fallback as best-effort. Missing
  or unreadable log is not an error; we fall through to the existing
  `safe_exec_error` path.
- **Codex `session_meta.payload` contains full base instructions**
  (potentially many KB) → mitigation: parse only the top-level scalar
  fields (`cwd`, `git.commit_hash`, `model`, `cli_version`); never copy
  `base_instructions` into `RawSignal`. The 256-rune `truncateSignal`
  bound is defence-in-depth.
- **OpenCode disk log is shared across concurrent opencode processes** →
  mitigation: this is already handled by the existing fallback. The
  locator (`openCodeServerLogFailureEvidence` in `opencode.go:153`)
  already correlates by opencode session id (extracted from the
  `message=created id=… directory=<WorkspaceDir>` line via
  `openCodeCreatedSessionID`) with a `providerID=<provider>` +
  try-window fallback (`openCodeLogLineInWindow`). This change extends
  that existing machinery to also keep WARN/ERROR lines and structural
  loop/stream markers when no parseable result was produced; it does not
  stand up a parallel correlation mechanism. Without a session id
  (opencode never started), the existing fallback returns nil and the
  runner falls through to `safe_exec_error`.
- **`RallyRoute` is a new event type** → operators with existing NRQL
  dashboards that filter `RallyTry WHERE outcome IS NOT NULL` will see
  the 39 routing events disappear. This is the intended cleanup;
  document it in the release notes.
- **Bare-alias routes now report a different `runner` tag** (e.g.
  `codex` → `codex:gpt-5.5`) → operators with NRQL alerts keyed on
  `runner = 'codex'` will need to widen the filter to
  `runner LIKE 'codex%'`. Document in release notes.
- **Hard-cut gemini breaks operators who pinned `gemini` routes** →
  mitigation: the one-time warning prints the lap / route / resolved
  alias that failed, so the operator knows exactly what to update.

## Migration Plan

1. Ship codex session-log enrichment, `ResolvedModel`, `failure_evidence`
   plumbing, try-budget classification, and `RallyRoute` first — these
   are additive and independently shippable.
2. Ship the `gemini` cut as the final lap of the change, paired with the
   one-time route-resolution warning. This minimises the surface area
   that is simultaneously broken.
3. Release notes call out: removed `gemini`/`ge` aliases; `runner` tag
   now includes resolved model; `RallyRoute` event type replaces the
   NULL-outcome entries in `RallyTry`; routing-events filter no longer
   needed in dashboards.
4. No rollback path is required for the gemini cut — restoring the
   alias is a follow-up patch if it turns out an operator depended on
   it. The other workstreams are backward-compatible.

## Open Questions

- **RESOLVED — Pattern.Match vs re-scan (was Q1).** Decision: re-scan
  inside `ClassifyError` after a match to extract the matching line. The
  patterns table is small, the re-scan runs once per failure, and the
  alternative (extending `Match` to return the matched line) would change
  the `Pattern` API and force every implementation to surface the match.
  Task 3.5 reflects this.
- **Open — codex no-session-log diagnostic event (was Q2).** Should the
  codex no-session-log case emit a separate `RallyDiagnostic` event (for
  visibility) in addition to classifying as `harness_launch`? Likely yes,
  mirroring the existing `event_kind=limit_signal` pattern. Leaving open
  until implementation; the spec already permits it without requiring it.
- **RESOLVED — EmitRouteEvent coverage of recovery-cap-hit (was Q3).**
  Decision: the recovery-cap-hit path at `runner.go:834` already emits a
  `RallyFailure` (operator-worthy `needs_user`), NOT a polluted
  `RallyTry`. Keep that `CaptureFailure` call intact AND additionally emit
  a `RallyRoute` event so the routing audit trail is uniform. Task 7.5
  reflects this.
