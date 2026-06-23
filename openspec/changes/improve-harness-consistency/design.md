## Context

Rally classifies harness failures via `reliability.ClassifyError`
(`internal/reliability/patterns.go:299`), invoked once per failed try at
`internal/relay/runner.go:2204`. The function returns a `StrategyDecision`
and uses a strict priority order:

1. Typed executor `Evidence` (Priority 1, line 302)
2. Provider/config/quota detection (Priority 2, placeholder)
3. Dirty-tree incomplete check (Priority 3, line 346)
4. Harness-scoped text patterns from `ErrorPatterns` (Priority 4, line 357)
5. Default `agent_error` (Priority 5, line 387)

Two `applyEvidenceToFailureState` helpers in `runner.go` populate the
`failure_evidence` context block on the emitted `RallyFailure` event:
`applyEvidenceToFailureState` (line 237, executor path) and
`applySafeExecErrorEvidence` (line 256, last-resort fallback when
`execErr != nil`). Priority 3, 4, and most of 5 leave the context block
empty — the decision struct carries `Category` / `Reason` / `Strategy` /
`FailureClass` / `DisplayLabel` / `Cooldown` but **not** the matching log
tail.

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

Routing-fallback events are emitted at `runner.go:794` via `EmitTryLog`,
producing `RallyTry` custom events with `from_runner`/`to_runner` populated
but no `outcome`, `attempt`, or `try_id`. 39 such events over 30 days
inflate any `RallyTry`-based failure-rate query.

Codex keeps session logs at `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`
(verified on a host running codex 0.141.0): one JSONL file per session,
newline-delimited structured events. First line is always `session_meta`
(carrying `cli_version`, `cwd`, `git.commit_hash`, `git.branch`,
`git.repository_url`, resolved `model`, full base instructions). Subsequent
events: `event_msg` subtypes `task_started`, `task_complete`, `turn_aborted`,
`token_count` (~45/file in a 50-file sample — the verbosity hazard),
`response_item` (full messages), `turn_context` (per-turn model + reasoning
effort). Also present: `~/.codex/logs_2.sqlite` (SQLite database),
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
  own session log, and are categorised as `harness_launch` when codex
  never wrote a session log at all.
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

- The first line (`session_meta`) for resolved `model`, `git.branch`,
  `git.commit_hash`, `cli_version`.
- The last `event_msg` line of any subtype (`task_started`,
  `task_complete`, `turn_aborted`) — its subtype is the diagnostic.

Explicitly skip `token_count`, `response_item`, and the
`base_instructions` payload. Bound the resulting `RawSignal` to 256 runes
using the existing `truncateSignal` helper. Populate `FailureEvidence`
with `Source = "codex_session_log"`, `Message` from the last event's
subtype, `RawSignal` from the bounded structural tail.

If no session-log file exists with a matching `cwd`, codex never got far
enough to do real work: classify as `harness_launch` (StrategyRotate,
agent-class so no freeze increment) and emit a
`failure_evidence.source = "codex_no_session_log"` marker. This is the
distinct signal that today's `agent_error`-and-burn-retry-budget pattern
in the 0.11.2 burst was missing. Alternative considered: spawn a
diagnostic `codex doctor` subprocess — rejected because it doubles process
overhead on a failure path; the session log is already authoritative.

Also tighten `runCodexCommand` (`internal/agent/codex.go:223`): the
current `cmd.Stderr = cmd.Stdout` assignment runs *after* `StdoutPipe()`,
so a stderr write that races the pipe close bypasses the override. Switch
to an explicit `io.Pipe` for stderr drained by a dedicated goroutine, so
stderr is captured regardless of timing. This is in-band only; the
session-log fallback is the authoritative safety net.

**3. `failure_evidence` on every classification priority via
`StrategyDecision.Evidence`.** Add an optional
`Evidence *FailureEvidence` field to `StrategyDecision` in
`internal/reliability/patterns.go`. Populate it inside `ClassifyError`
for every branch:

- Priority 1 (executor Evidence, line 302): pass through the executor's
  `Evidence` unchanged; set `Source = "executor_evidence"` if unset.
- Priority 3 (dirty-tree incomplete, line 346): build an Evidence with
  `Category = CategoryIncompleteFinalization`, `Message =
  "agent exited without finalization"`, `Source = "dirty_tree"`, and
  `RawSignal` from the changed-paths list (the runner already computes
  this via `filesChangedList` at `runner.go:3189`).
- Priority 4 (text patterns, line 357): capture the matching log line at
  match time inside `Pattern.Match` (extend `Match` to optionally return
  the matched line, or have `ClassifyError` re-scan after a match to
  extract it). Set `Source = "text_pattern"`, `Message = pattern.Name`,
  `RawSignal` from the bounded matching tail.
- Priority 5 (default agent_error, line 387): set `Source = "unmatched"`,
  `Message = "no recognised provider signal"`, `RawSignal` from a bounded
  log tail so we always have something.

The runner's existing `applyEvidenceToFailureState` then handles all
four sources uniformly; `applySafeExecErrorEvidence` becomes the
lowest-priority fallback that fires only when both `result.Evidence` and
`decision.Evidence` are nil. Alternative considered: re-parse inside the
runner for each priority — rejected because it duplicates the matching
logic and re-reads the log; carrying Evidence through the decision struct
is a single source of truth.

**4. Try-budget-exhaustion classification, distinct labels, no cascade
change.** When `loopOut.timedOut` is true (`runner.go:1849`) and no
Evidence was produced (neither executor Evidence nor the new session-log
/ disk-log fallbacks), set `failureCategory = transient_infra` and
`failReason = "try budget exhausted; no output"`. This maps to
`FailureInfra` which **does** feed the freeze counter today — and that
is wrong for budget exhaustion (the harness did not fail, we killed it).
Override the mapping at the runner call site for this single case: set
`failureClass = FailureAgent` directly so the freeze counter does not
increment, while leaving the category as `transient_infra` for NRQL
filtering. Alternative considered: add a new
`CategoryTryBudgetExhausted` — rejected because it would force a new
telemetry tag value and a new branch in every consumer; reusing
`transient_infra` with a distinct `fail_reason` is enough signal for
NRQL and requires no schema change.

**5. `RallyRoute` event for routing decisions.** Add
`Sink.EmitRouteEvent(ctx, fields)` and a `RallyRoute` custom event type.
Move the route-fallback emission at `runner.go:794` from `EmitTryLog` to
`EmitRouteEvent`. The event carries `relay_id`, `run_id`, `lap_id`,
`role`, `from_runner`, `to_runner`, `repo`, `repo_name`, and any fallback
cause fields. This keeps `RallyTry` pure (every event is a real try
outcome with a non-empty `outcome` tag) and gives routing decisions their
own schema for NRQL. Alternative considered: keep using `RallyTry` but
always set `outcome = "routed"` and document the filter — rejected
because it leaves a confusing dual-purpose event type and forces every
reporting query to remember the filter.

After this change, audit every `EmitTryLog` site (`runner.go:794, 2057,
2524, 2996`) and assert at the telemetry boundary that `outcome` is
non-empty for `RallyTry`. The audit is the close-the-gap guarantee.

**6. `ResolvedModel` on `TryResult`, runner-tag fallback.** Add
`ResolvedModel string` to `agent.TryResult` (`internal/agent/agent.go`).
Each executor sets it to the model actually passed to the CLI: codex
sets it from the `model` local at `codex.go:170-173` after the
`opts.Model` fallback; claude / opencode / antigravity likewise. At the
three telemetry sites that build the `runner` tag
(`runner.go:2057, 2370, 2947`), pass `result.ResolvedModel` to
`telemetry.RunnerLabel` when non-empty, falling back to `picked.Model`
otherwise. The route-resolved model stays authoritative when set; this
is a fallback for the bare-alias case only. Alternative considered:
resolve the default model at route-selection time — rejected because the
executor owns the resolution (config-default vs CLI-default vs
model-alias expansion) and reporting it back through `TryResult` is the
single, accurate source.

**7. Parser rename: `ParseGeminiError` → `ParseAntigravityError`.** With
gemini gone, the function serves antigravity only; the shared name is a
leftover. Rename the function and its tests, and remove the
`gemini-cli exit 1` and `gemini auth or unsupported client` patterns
from `ErrorPatterns` (`patterns.go:224-248`). The eligibility regex
(`IneligibleTierError`, `UNSUPPORTED_CLIENT`, `no longer supported for
Gemini Code Assist`) stays — it now applies only to antigravity.

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
  mitigation: correlate by opencode session id (extracted from the
  `message=created id=… directory=<WorkspaceDir>` line at executor
  startup) and filter on that session's events. Without a session id
  (opencode never started), fall through to `safe_exec_error`.
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

- Should `Pattern.Match` be extended to return the matched line (cleaner
  extraction for Decision 3's text-pattern Evidence), or should
  `ClassifyError` re-scan after a match to find it (no API change)?
  Leaning towards re-scan: the patterns table is small and the re-scan
  runs once per failure.
- Should the codex no-session-log case emit a separate
  `RallyDiagnostic` event (for visibility) in addition to classifying as
  `harness_launch`? Likely yes, mirroring the existing
  `event_kind=limit_signal` pattern.
- Should `EmitRouteEvent` also cover the recovery-cap-hit path at
  `runner.go:804`, or only the route-fallback path? Both are
  routing-shaped and neither belongs in `RallyTry`.
