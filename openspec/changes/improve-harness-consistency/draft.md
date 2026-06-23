## Draft: Improve Harness Consistency

Status: re-scoped 2026-06-23 against the **current codebase and New Relic
telemetry** for the `Rally CLI` app (account 8182741, profile `rally`).
Targets the **0.12.0** release, which is also the release that removes the
deprecated `gemini` CLI harness.

The earlier draft of this change leaned on local relay state from one
machine; most of those failures turned out to be from older `0.9.x` builds
and have already been fixed. This revision is driven by 30 days of New
Relic data across all operators, plus targeted source investigation of
each workstream.

## Why

Rally supports multiple harnesses (`claude`, `codex`, `gemini`, `opencode`,
`antigravity`, and generic/custom adapters). Each harness has a different
CLI output schema, but Rally should normalize those differences into a
small, consistent adapter contract. Divergent one-off behavior makes
relays harder to reason about and can make one harness appear less
reliable than another when the real issue is integration shape.

The four per-harness parsers in `internal/reliability/`
(`ParseClaudeError`, `ParseCodexError`, `ParseGeminiError`,
`ParseOpencodeError`) are correct for the shapes they recognise (New Relic
confirms real limit signals are being detected — see "Evidence corpus"
below), but the integration around them is lopsided:

- The parsers live inside the executor adapters and only run when the
  adapter captures a non-empty error buffer. When they match, the runner's
  `ClassifyError` honours the typed `Evidence`. When they don't match,
  classification falls back to harness-scoped text patterns in
  `internal/reliability/patterns.go` and then to a default `agent_error`,
  and **the `failure_evidence` context block on the emitted `RallyFailure`
  event is left empty**. Only 3 of the 77 `RallyFailure` events in the
  last 30 days carry `failure_evidence.source = executor_evidence`.
- Codex's silent exit-1 failures (the 0.11.2 burst) leave no in-band
  signal because codex itself writes nothing to stdout/stderr before
  dying — but codex keeps rich session logs on disk that we are not
  reading. Same shape applies to opencode's try-budget exhaustion.
- The `runner` telemetry tag silently drops the model component when a
  route is configured with a bare alias, breaking per-model NRQL queries
  exactly when they matter most (silent failures).
- `gemini` CLI is now deprecated upstream. Its own auth error tells
  operators to "migrate to the Antigravity suite of products" — see the
  captured `_GaxiosError` / `IneligibleTierError` evidence below. It
  should be cut entirely for 0.12.0.

## Evidence corpus (New Relic, last 30 days, all operators)

Source: `RallyFailure`, `RallyTry`, and `RallyDiagnostic` events for
appName `Rally CLI`, account 8182741, queried via newrelic-cli 0.68.7
(snap profile `rally`). 77 `RallyFailure` events and ~310 `RallyTry`
events over the window.

### Failure volume is trending down

| Version | Failures | % `agent_error` |
|---|---:|---:|
| 0.9.3  | 46 | 57% |
| 0.10.0 | 5  | 80% |
| 0.10.1 | 7  | 0% |
| 0.11.2 | 16 | 62% |
| 0.11.3 | 3  | 0% |

The 0.11.2 spike is a single relay hitting the codex silent-exit issue
(workstream B). 0.11.3 is clean. The earlier "antigravity
RESOURCE_EXHAUSTED mis-categorised as `agent_error`" finding does **not**
appear in current data — that bug existed in 0.9.x and has been fixed.

### Provider parsers work — confirmed by real reset evidence

`RallyDiagnostic` events of form `provider limit signal: …` show the
parsers detecting and tagging real limits:

| Runner | Captured signal |
|---|---|
| `claude` / `claude:claude-opus-4-8` | `usage limit, resets in 5h0m` (×5) |
| `claude:claude-opus-4-8` | `usage limit, resets in 168h0m` (×1, the 7-day window) |
| `codex:gpt-5.4` | `rate limit, waiting 1m` |
| `codex:gpt-5.4` / `codex:gpt-5.5` | `usage limit` (no reset captured) |
| `opencode:zai-coding-plan/glm-5.2` | `usage limit, resets in 45m` |
| `gemini:gemini-3.1-pro-preview` | `rate limit, waiting 1m` |

The per-harness parsers are correct for the shapes they recognise and
the reset-timing formats are real, not remembered. The
`improve-error-categorisation` from-memory assumptions are now validated
for Claude (five-hour and seven-day windows), OpenCode (short
minute-scale resets), and Codex (no-reset usage limit). The Claude
clock-reset code path (`resets at 14:30` / `resets at 2:30 PM`) remains
unvalidated — no sample has appeared yet.

### Antigravity-Claude routing

Of 66 `antigravity:*` tries in the window, 50 ran
`Claude Opus 4.6 (Thinking)` via Antigravity, 14 ran
`Gemini 3.5 Flash (High)`, 2 ran `Gemini 3.1 Pro (High)`. The
antigravity-claude mix is a side effect of how routes are configured
(the Claude-via-Antigravity entry is positioned eagerly in the fallback
chain), not a feature we promote. **Worth flagging in the data:** the 50
antigravity-claude tries complete only ~2% of the time, vs. 64% for
antigravity-gemini-flash — but that is a routing-policy question, not a
harness-consistency one, and is out of scope here. The plan acknowledges
the pattern only so we do not mis-read future NRQL output.

### Gemini CLI deprecation evidence

Real captured auth failures on the gemini harness:

- `_GaxiosError: Resource has been exhausted (e.g. check quota).` —
  emitted from `@google/gemini-cli/bundle/chunk-RCJSF5RP.js` (the CLI's
  own bundled JS).
- `IneligibleTierError: This client is no longer supported for Gemini
  Code Assist for individuals. To continue using Gemini, please migrate
  to the Antigravity suite of products.`

Google's own CLI is telling operators to migrate. Rally should follow.
Antigravity already serves the Gemini model family on the same provider
account, so cutting `gemini` does not remove any capability operators
actually need.

## Open issues visible in current data (workstreams B–F)

### B. Codex silent exit-1 (0.11.2 burst, 10 failures across one relay)

All 10 events show:

- `runner = "codex"` (no `:model` component — see F)
- `failure_category = agent_error`, `error_class = RallyAgentError`
- `failure_evidence.source = safe_exec_error`,
  `failure_evidence.raw_signal = "codex exec failed: exit status 1"`
- `attempt` ranges 1..5 — every retry in the run burned the full budget.

The codex CLI exited 1 with empty captured stdout/stderr, so
`ParseCodexError` had nothing to match and the runner captured only the
wrapper error. **Codex does not, in fact, write nothing — it writes a
full session log to disk that we are not reading.**

Codex session-log layout (verified on a live host running codex 0.141.0):

- `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<session_id>.jsonl`
  — one JSONL file per session, newline-delimited structured events.
- First line is always a `session_meta` event carrying `cli_version`,
  `cwd`, `git.commit_hash`, `git.branch`, `git.repository_url`, the
  resolved `model`, and the full base instructions.
- Subsequent events: `event_msg` of subtype `task_started`
  (`model_context_window`, `turn_id`), `event_msg` of subtype
  `task_complete` (terminal, with `last_agent_message`),
  `event_msg` of subtype `token_count` (high volume — ~45/file in a
  sample of 50 files, this is the verbose one to skip),
  `event_msg` of subtype `turn_aborted`, `response_item` (full
  messages, also verbose), `turn_context` (per-turn model + reasoning
  effort).
- Also present: `~/.codex/logs_2.sqlite` (a SQLite database, unreadable
  without sqlite3 in the environment but compact), `~/.codex/auth.json`
  (credentials — must never be read into telemetry), `~/.codex/config.toml`.

The signal-to-noise ratio is workable **if** we read only the structural
events: the first line (`session_meta`) gives model + git context, and
the last `event_msg` (whatever subtype) tells us whether codex reached
`task_started`, `task_complete`, `turn_aborted`, or died mid-stream. We
must explicitly skip `token_count`, `response_item`, and any event whose
payload contains `base_instructions` or full message bodies — those are
the verbosity hazards.

The codex executor today (`internal/agent/codex.go:223 runCodexCommand`)
sets `cmd.Stderr = cmd.Stdout` *after* taking a `StdoutPipe()` and then
drains through a `bufio.Scanner`. When codex writes its diagnostic to fd
2 *after* the stdout pipe is closed at process exit (or directly via a
syscall that bypasses the override), those bytes never reach `out` and
`ParseCodexError` sees an empty string. Reproducing the failure mode is
the first step.

### C. `failure_evidence` empty on 78% of `RallyFailure` events

Of 77 `RallyFailure` events, only 3 carry
`failure_evidence.source = executor_evidence`:

| `failure_evidence.source` | `evidence_shape` | Count |
|---|---|---:|
| (none) | (none) | 60 |
| `safe_exec_error` | `transcript_tail` | 10 |
| (none) | `plain_text` | 4 |
| `executor_evidence` | `plain_text` | 2 |
| `executor_evidence` | `provider_object` | 1 |

Source-investigation result. The runner builds evidence via two helpers
in `internal/relay/runner.go`:

- `applyEvidenceToFailureState` (line 237) — called only when the
  executor returned typed `Evidence` (Priority 1 in `ClassifyError`),
  setting `EvidenceSource = "executor_evidence"`. For non-limit
  categories it sets `EvidenceRawSignal` / `EvidenceMessage`; for limit
  categories it sets `RawSignal` / `Message` and returns early.
- `applySafeExecErrorEvidence` (line 256) — fills
  `EvidenceRawSignal = err.Error()`, `EvidenceSource = "safe_exec_error"`
  as a last-resort fallback **only when `execErr != nil`**.

When `ClassifyError` returns via Priority 3 (dirty-tree incomplete, line
345) or Priority 4 (text patterns, line 357) or Priority 5 (default
agent_error, line 387), no evidence fields are populated by the runner
because the decision struct carries only `Category`, `Reason`,
`Strategy`, `FailureClass`, `DisplayLabel`, `Cooldown` — not the
matching log tail. So 78% of failures have a correct category but an
empty `failure_evidence` context block.

### D. OpenCode try-budget exhaustion indistinguishable from real crashes

16 `RallyFailure` events from `opencode:opencode-go/deepseek-v4-pro` and
5 from `opencode:opencode/minimax-m2.5-free` are `agent_error` with no
`failure_evidence` context. Local relay data and runtime patterns
(~900 s try-cap hits) show these are budget-exhaustion cases where
opencode produced no parseable result before the runner killed it.

OpenCode keeps a logfmt log at `~/.local/share/opencode/log/opencode.log`
(verified ~4.9 MB on a live host — verbose). Lines are of the form
`timestamp=… level=INFO run=<run_id> message="…" <structured key=value
pairs>`. Useful structural events for budget-exhaustion diagnosis:

- `message="creating instance"` / `"bootstrapping"` / `message=init` —
  process startup.
- `message="created id=<session_id> … model.id=<id>
  model.providerID=<id> agent=<agent>` — the session was actually
  started; this resolves the real model and agent identity.
- `message="loop session.id=<id> step=<n>"` — agent loop progress, one
  per step. A try that produced 50 `loop` lines was making progress; a
  try that produced zero `loop` lines stalled at startup.
- `level=ERROR … message=<text>` — actual errors, the high-signal lines
  to surface into Evidence.

Filtering by the `run=` ID for the failing try, then keeping only
`level=WARN|ERROR` lines plus the structural `loop`/`created`/`stream`
markers, would give us enough to distinguish "stalled at startup" from
"crashed mid-loop" from "ran the full budget silently" — without the
per-token noise that bloats the file.

Today's opencode executor (`internal/agent/opencode.go`) reads only the
in-band stdout JSON stream; when the budget kills the process before
any `step_finish` event arrives, `ParseOpencodeError` has nothing to
match and we record the generic `agent_error`. The disk log is the only
place the actual reason lives.

### E. `RallyTry.outcome IS NULL` on 39 events — these are routing events, not try outcomes

NRQL inspection of the 39 NULL-outcome `RallyTry` events shows 100% of
them have `event = "route_fallback"`, `from_runner` populated, `to_runner`
populated, and **`try_id = NULL`**. These are not try outcomes at all;
they are routing-decision events emitted at `internal/relay/runner.go:794`
when a route falls back from one runner to another. They are sent to
`EmitTryLog` (which records a `RallyTry` custom event) because there is
no `EmitRouteEvent` API, and they carry no `outcome`/`attempt`/`try_id`
because the runner is not in a try-scope at that point.

The result is that every NRQL query like
`SELECT count(*) FROM RallyTry FACET runner, outcome` mixes real try
outcomes with routing events, and the routing events all sit at
`outcome IS NULL`, polluting the failure-rate math.

### F. `runner` tag missing the model component

`telemetry.RunnerLabel(harness, model)` at `internal/telemetry/tags.go:26`
returns just `harness` when `model == ""`. When a route is configured
with a bare alias (e.g. `routes senior = ["cx", "cc"]` with no
`:model`), `picked.Model` is empty at every site that calls
`RunnerLabel` (`runner.go:2057`, `2370`, `2947`) and the emitted tag
collapses to `"codex"` / `"claude"` — which is exactly what we see for
the 0.11.2 codex burst and 5 `RallyTry` events with `runner = "claude"`.

The executor does resolve a real model — `CodexExecutor.Model` falls
back to `cfg.CodexModel`, `ClaudeExecutor` to `cfg.ClaudeModel`, etc.
— but that resolution happens *inside* the executor and is never
reported back to the runner. So the runner keeps emitting
`runner = "codex"` even though codex was actually invoked with
`--model gpt-5.5`.

## Intent

- Make every categorised `RallyFailure` event self-contained: whether
  classification comes from executor Evidence, runner text patterns, or
  the dirty-tree check, the same `failure_evidence` fields should be
  populated.
- Surface codex's silent-exit reason into Evidence by reading codex's
  own session-log file (filtered to structural events only).
- Surface opencode's try-budget-exhaustion reason by reading the
  opencode disk log filtered to the failing run.
- Distinguish try-budget exhaustion from real harness crashes in the
  recorded category.
- Always populate the `runner` tag with the resolved model, even when
  the route was a bare alias.
- Stop polluting `RallyTry` with non-try routing events — either give
  them a real outcome or move them to a dedicated event type.
- Cut the deprecated `gemini` CLI harness entirely for 0.12.0.

## Candidate work

### A. Cut `gemini` CLI harness (0.12.0 blocker)

Google has deprecated the standalone `gemini` CLI — its own auth error
tells operators to migrate to Antigravity (see evidence above). Remove
all first-class `gemini` support; `antigravity` becomes the only
Google-owned harness and inherits the Gemini model family
(`gemini-3.1-pro-preview`, `gemini-3-flash-preview`, etc.) via its
existing `[harness.ag.models]` list. **Hard cut** per operator decision:
the `ge`/`gemini` aliases fail to resolve with a one-time warning
pointing operators at `antigravity`, with no transitional alias
mapping.

Removal surface (audit complete; edits not yet made):

- **Executor**: `internal/agent/gemini.go` (entire file, 182 lines). Test
  coverage in `internal/agent/agent_test.go` (`TestGeminiExecutor_*`) to
  remove.
- **Reliability**: rename `ParseGeminiError` → `ParseAntigravityError`
  in `internal/reliability/antigravity.go` and its callers
  (`internal/agent/antigravity.go:106`, plus tests). Remove the
  `gemini-cli exit 1` and `gemini auth or unsupported client` patterns
  from `internal/reliability/patterns.go:224-248`.
- **Routing & aliases**: drop the `ge`/`gemini` alias entries from
  `internal/relay/route_runtime.go:715`, `internal/relay/mix.go:97`,
  `internal/config/config_v2.go:47-71` (alias map,
  harness-canonical set, `GeminiModel` field), and
  `internal/cli/routes_check.go:441`.
- **Config**: drop `gemini_model` from `~/.config/rally/config.toml`
  defaults, `[harness.ge.models]` blocks, the `defaults` and `harness`
  sections in `internal/config/config_v2.go`, the `huh.NewInput` prompt
  in `internal/cli/config.go:111`, and the built-in harness list in
  `internal/config/providers.go:511`.
- **Roles**: remove the `Route: []string{"gemini"}` default in
  `cmd/rally/init_roles.go:65`.
- **Tests**: scrub the gemini fixtures from
  `internal/relay/{runner_test,route_runtime_test,resilience_test,
  bench_state_machine_test,runner_real_backend_test,runner_final_snippet_test}.go`,
  `internal/routing/{override_test,quota_scope_test,provider_test,parse_test}.go`,
  `internal/style/style_test.go`, `internal/store/store_test.go`,
  `internal/reliability/{patterns_test,category_test}.go`, and
  `cmd/rally/main_test.go`. The real-backend test in
  `runner_real_backend_test.go:618-680` (the gemini-flash rotation
  smoke) is deleted entirely.
- **README**: drop `gemini` from the harness list, command examples, and
  defaults tables.
- **Warning**: emit a one-time warning when a configured route resolves
  to the removed `ge`/`gemini` alias, so operators who upgrade see
  actionable guidance instead of a silent resolution failure.

### B. Diagnose and fix the codex silent-exit burst

The 0.11.2 codex spike (10 failures across one relay) all show
`raw_signal = "codex exec failed: exit status 1"` with no further
diagnostic. Codex itself writes nothing to stdout/stderr in this case,
but it does write a session log to disk.

1. **Reproduce and confirm the cause.** Run codex with the rally args
   (`codex exec --dangerously-bypass-approvals-and-sandbox --json
   --output-schema … -o … <prompt>`) in the same container image and
   capture both the parent's merged stderr/stdout and what codex writes
   to its session log. Most likely cause is auth expiry / missing
   `~/.codex/auth.json`, an invalid `--model` value, or an OOM kill
   before codex opens its session file.
2. **Tighten the in-band capture** at `internal/agent/codex.go:223
   runCodexCommand` so we are certain stderr reaches `out`. The current
   `cmd.Stderr = cmd.Stdout` assignment runs *after* `StdoutPipe()`,
   which means stderr is wired into the same pipe stdout uses — but
   only after `cmd.Start()`. A `WriteString` to fd 2 during the
   brief window, or a write that bypasses the override, would still be
   lost. Switch to an explicit `io.Pipe` for stderr drained by a
   separate goroutine, so stderr is captured regardless of timing.
3. **Add a codex session-log fallback.** When codex exits non-zero with
   no in-band signal, locate the latest `rollout-*.jsonl` under
   `~/.codex/sessions/YYYY/MM/DD/` whose first-line `session_meta.cwd`
   matches the configured `WorkspaceDir` and whose timestamp is within
   the try window. Read **only** the first line (`session_meta`, for
   resolved model + git context) and the last `event_msg` (whatever
   subtype — `task_started`, `task_complete`, `turn_aborted`). Explicitly
   skip `token_count`, `response_item`, and the `base_instructions`
   payload to avoid the verbosity hazard. Populate `FailureEvidence`
   with `Source = "codex_session_log"`, `Message` from the last
   `event_msg.payload.type`, `RawSignal` from a bounded tail of
   structural events.
4. **Classify the silent-exit shape distinctly.** A codex that exits 1
   without ever writing a `session_meta` line never tried to do real
   work — that is `harness_launch` (rotate immediately, no retry), not
   `agent_error` (which burned the full 5-attempt budget on the 0.11.2
   relay). Add the detection: if no session-log file exists with a
   matching `cwd`, treat as `harness_launch`.

### C. Populate `failure_evidence` on every categorised `RallyFailure`

Today `applyEvidenceToFailureState` (`runner.go:237`) only fires on the
executor-evidence path; the text-pattern and dirty-tree paths leave the
context block empty.

1. **Extend `StrategyDecision`** (in `internal/reliability/patterns.go`)
   with an optional `Evidence *FailureEvidence` field. Populate it inside
   `ClassifyError` for every branch:
   - Priority 3 (dirty-tree incomplete, line 345): set
     `Evidence.Message = "agent exited without finalizing"` and
     `Evidence.Source = "dirty_tree"`. The changed-paths list is already
     computed by `filesChangedList` in the runner — pass it as
     `Evidence.RawSignal` (bounded to the same 256-rune budget the
     parsers use).
   - Priority 4 (text patterns, line 357): set
     `Evidence.Source = "text_pattern"`,
     `Evidence.Message = pattern.Name`, and `Evidence.RawSignal` from
     the matching log tail. The match function already has access to
     `logLines`; extract the matching line at match time so we keep it.
   - Priority 5 (default agent_error, line 387): set
     `Evidence.Source = "unmatched"`, `Evidence.Message =
     "no recognised provider signal"`, and `Evidence.RawSignal` from a
     bounded log tail so we always have something.
2. **Plumb the decision's Evidence through the runner.** At
   `runner.go:2479` (where `resetEvidence` is conditionally assigned
   from `result.Evidence`), also fall back to `decision.Evidence` when
   `result.Evidence` is nil. The downstream
   `applyEvidenceToFailureState` then populates the context block for
   every classification path, not just the executor path.
3. **Telemetry schema.** Record `failure_evidence.source` values
   `codex_session_log` (workstream B), `opencode_disk_log` (workstream
   D), `dirty_tree`, `text_pattern`, `unmatched`, alongside the existing
   `executor_evidence` and `safe_exec_error`. Result:
   `SELECT latest(failure_evidence.raw_signal) FROM RallyFailure` is
   useful for 100% of categorised failures instead of 4%.

### D. Diagnose and label opencode try-budget exhaustion

21 opencode `agent_error` events in the window are almost certainly
budget exhaustion: opencode ran the full ~900 s cap with no output, then
the runner killed it. Two pieces of work:

1. **Surface the disk-log reason.** When the opencode executor returns
   no Evidence and the runner can see that `loopOut.timedOut` is true,
   locate the relevant lines in `~/.local/share/opencode/log/opencode.log`
   filtered by the failing process's `run=` ID (the executor can
   capture this from `opencode run`'s startup output, or by correlating
   timestamps). Keep only `level=WARN|ERROR` lines plus the structural
   `created`/`loop`/`stream` markers — bounded to ~16 lines max.
   Populate `FailureEvidence.Source = "opencode_disk_log"`,
   `Evidence.Message` from the last error line, `Evidence.RawSignal`
   from the bounded filtered tail.
2. **Distinguish budget exhaustion from real crashes in the recorded
   category.** Add a runner-side path: when `loopOut.timedOut` is true
   and no Evidence was produced (no executor evidence, no disk log
   signal), record a new category or use the existing
   `transient_infra` with `fail_reason = "try budget exhausted; no
   output"`. Verify the resilience cascade still treats this as
   agent-class (does-not-freeze) so we don't change freeze behaviour —
   only the recorded labels.

### E. Stop polluting `RallyTry` with routing events

The 39 NULL-outcome `RallyTry` events are `event = "route_fallback"`
emissions from `runner.go:794`. They have no `outcome`, `attempt`, or
`try_id` because they happen outside any try scope. Two options:

- **Preferred**: add a dedicated `EmitRouteEvent` API on the Sink
  (`internal/telemetry/sink.go`) and a `RallyRoute` custom event, so
  routing decisions stop polluting `RallyTry`. The fields
  (`from_runner`, `to_runner`, `role`, `lap_id`, fallback cause) are
  routing-shaped, not try-shaped, and deserve their own schema.
- **Minimal**: keep using `RallyTry` but always set
  `outcome = "routed"` and add an `event = "route_fallback"` filter
  to the NRQL queries in any reporting we ship. Document this in the
  telemetry README.

Either way, every code path that emits a `RallyTry` event must set a
non-empty `outcome` so `RallyTry.outcome IS NULL` becomes impossible.
The paths to audit: `runner.go:794` (routing), `runner.go:2057`
(cancelled), `runner.go:2524` (terminal try), `runner.go:2996`
(handoff continuation).

### F. Always populate `runner` with the resolved model

`telemetry.RunnerLabel(harness, model)` collapses to bare `harness`
when `model == ""`. The executor always resolves a real model
(`CodexExecutor.Model`, `ClaudeExecutor.Model`, …) — we just don't
report it back.

1. **Extend `TryResult`** (`internal/agent/agent.go`) with a
   `ResolvedModel string` field. Each executor sets it to the model
   actually passed to the CLI: `c.Model` for codex (after the
   `opts.Model` fallback at `codex.go:170-173`), equivalent for
   claude / opencode / antigravity.
2. **Use `result.ResolvedModel` in `RunnerLabel`** when non-empty, at
   all three telemetry sites (`runner.go:2057`, `2370`, `2947`). Fall
   back to `picked.Model` for backwards compatibility.
3. **Keep the route-resolved model authoritative** when set — this is
   only a fallback for the bare-alias case, not a replacement for
   explicit `:model` in routes.

### G. Normalize per-harness failure-evidence parsers (data-validated)

With New Relic evidence in hand, finish what `improve-error-categorisation`
started:

- Replace the four ad-hoc parser entry points with one
  `reliability.ParseHarnessError(harness, stderr, model) *FailureEvidence`
  that dispatches to the harness-specific implementation. Each adapter
  then calls one function instead of import-path coupling to a specific
  parser.
- Update `regression_test.go` and the per-parser tests to use real
  captured signal text (anonymised) instead of hand-written samples.
  Real shapes now in the corpus: `_GaxiosError: Resource has been
  exhausted`, `IneligibleTierError: This client is no longer
  supported`, codex `{"type":"thread.started",…}` transcript, opencode
  `error.error="…"` flat server-log carrier, claude
  `usage limit, resets in 5h0m` / `168h0m`.
- Resolve the still-open `improve-error-categorisation` questions:
  - **Claude clock reset** (`resets at 14:30`, `resets at 2:30 PM`):
    unvalidated — leave the code, mark the test `t.Skip` until a sample
    lands in NR.
  - **Claude `rate_limit` vs overload**: confirmed separate — the 529 /
    `overload` regex catches provider overload; the seven-day /
    five-hour windows go to `usage_limit`; bare `rate_limit` text goes
    to `short_rate_limit`. No change needed.
  - **Antigravity short-window tier**: real data shows
    `Resets in 18m21s` phrased as `RESOURCE_EXHAUSTED … Individual
    quota reached`, so it correctly lands in `usage_limit`. No parser
    change required; the validation is the deliverable.

### H. Adapter conformance and capability matrix (deferred past 0.12.0)

Larger-scope work that does not need to land in this release:

- Adapter conformance tests for structured success, unstructured final
  text, no final text, infra/rate-limit error, tool use, and session ID
  behavior.
- Publish a harness capability matrix for liveness probe, resume, model
  rotation, clean completion detection, tool counting, and structured
  output. Note that antigravity is sometimes used to run Claude models
  (an operator route-config decision, not a Rally feature) — the matrix
  should not present cross-provider routing as a first-class capability.

## Initial questions still open

- Should `TryResult` grow explicit fields for adapter-level error
  evidence, retry-after hints, or clean-completion markers, instead of
  encoding them only in summary text/log pattern matching? (Workstream C
  touches this lightly with `ResolvedModel`; a broader refactor can
  wait.)
- For codex silent exit (workstream B), do we treat
  exit-1-with-no-session-log as `harness_launch` (rotate immediately,
  no retry) instead of burning the retry budget?
- Should the routing events move to a new `RallyRoute` event
  (`E.EmitRouteEvent`) or stay in `RallyTry` with `outcome = routed`?
  The former is cleaner; the latter is less code.

## Out of scope for this change

- New harness integrations (use the `add-new-harness` skill).
- Rewriting the resilience cascade or the freeze counter.
- The provider quota-groups / wildcard provider work that landed in
  0.11.x.
- Historic 0.9.x data cleanup — those failures are already fixed; this
  change targets 0.11.3 → 0.12.0 behaviour.
- Antigravity-claude route policy (the 2% completion rate on 50 tries).
  It is a routing-policy question, not a harness-consistency one.
