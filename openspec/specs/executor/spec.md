# executor Specification

## Purpose
TBD - created by archiving change consolidate-rally-gry. Update Purpose after archive.
## Requirements
### Requirement: Executor interface
The system SHALL define an `Executor` interface with a single method `Execute(ctx context.Context, opts RunOptions) (*TryResult, error)` that abstracts agent lifecycle. Each call to Execute represents one try (one agent CLI invocation). The `TryResult` SHALL carry an optional `FailureEvidence` value that an executor MAY populate when it can extract structured failure information (category, provider, quota scope, status code, and reset/retry timing) from the agent's output. The field is optional: when an executor cannot supply it — including process-level failures where the executor returns an error with a nil or partial `TryResult` — consumers SHALL fall back to runner-side classification rather than requiring the evidence to be present.

#### Scenario: Executor returns structured result
- **WHEN** an Executor implementation completes a try
- **THEN** it SHALL return a `TryResult` containing: `Completed` (bool), `Summary` (string), `RemainingWork` (string), `MessageAddressed` (*bool), `FilesChanged` ([]string), and an optional `Evidence` (*FailureEvidence)
- **NOTE**: `CommitHash` is NOT part of `TryResult` — it is determined by the relay runner after the executor returns, by comparing HEAD before and after the try.

#### Scenario: Executor populates failure evidence when available
- **WHEN** an executor parses a structured provider/quota/config error from the agent's output (e.g. a usage-limit event with a reset window)
- **THEN** it SHALL set `TryResult.Evidence` with the corresponding category and any parsed reset/retry timing and quota scope

#### Scenario: Evidence absent for process-level failure
- **WHEN** a try fails before producing a usable `TryResult` (e.g. `fork/exec`, non-zero process exit) so the executor returns an error with a nil or partial `TryResult`
- **THEN** the absence of `Evidence` SHALL NOT be an error, and the relay runner SHALL classify the failure from the log tail instead

#### Scenario: Executor respects context cancellation
- **WHEN** the provided context is cancelled during execution
- **THEN** the executor SHALL terminate the agent subprocess and return a context cancellation error

### Requirement: RunOptions prompt building
The system SHALL build agent prompts from `RunOptions` fields: `Persona`, `TaskName`, `TaskRequirements`, `InboxMessage`, `PreviousSummary`, `RecentTryContext` (summaries from recent tries), and an optional explicit `Prompt` override. The built prompt is also written to `.rally/current_task.md` for agent reference.

#### Scenario: Explicit prompt overrides built prompt
- **WHEN** `RunOptions.Prompt` is non-empty
- **THEN** the executor SHALL use it verbatim instead of building a prompt from other fields

#### Scenario: Previous summary included on retry
- **WHEN** `RunOptions.PreviousSummary` is non-empty
- **THEN** the built prompt SHALL include a "Previous Attempt Summary" section containing the summary text

### Requirement: ClaudeExecutor
The system SHALL provide a `ClaudeExecutor` that invokes `claude -p <prompt> --output-format stream-json --verbose` as a subprocess, parses the NDJSON stream inline, and returns a `TryResult`. When a Claude model is specified, the executor SHALL pass it with `--model`. When a resolved reasoning effort is specified for Claude, the executor SHALL pass it with `--effort`.

#### Scenario: Claude run with model override
- **WHEN** a Claude model is specified in configuration
- **THEN** the executor SHALL pass `--model <model>` to the claude CLI

#### Scenario: Claude run with reasoning effort
- **WHEN** a Claude reasoning effort is resolved for the run
- **THEN** the executor SHALL pass `--effort <value>` to the claude CLI

#### Scenario: Claude stream-json parsing
- **WHEN** the claude subprocess completes
- **THEN** the executor SHALL parse each line as a `claudeStreamEvent` JSON object and extract the `result` field from events with `type: "result"`

### Requirement: CodexExecutor
The system SHALL provide a `CodexExecutor` that invokes `codex exec` as a subprocess with appropriate flags and returns a `TryResult`. When a resolved reasoning effort is specified for Codex, the executor SHALL inject it as a config override with `-c model_reasoning_effort=<value>`, not as a nonexistent CLI reasoning flag. The executor SHALL merge subprocess stderr into stdout via the standard Go `cmd.StdoutPipe()` + `cmd.Stderr = cmd.Stdout` (before `cmd.Start()`) idiom — this is the library-recommended merge pattern and is not a race. When the subprocess exits non-zero with no in-band parser-matchable signal, the executor SHALL enrich `FailureEvidence` from codex's own session log under `$CODEX_HOME/sessions/` (default `~/.codex/sessions/`) when a matching session exists.

#### Scenario: Codex run with approval bypass mode
- **WHEN** a codex run is executed
- **THEN** the executor SHALL pass `--dangerously-bypass-approvals-and-sandbox`

#### Scenario: Codex run with reasoning effort
- **WHEN** a Codex reasoning effort is resolved for the run
- **THEN** the executor SHALL pass `-c model_reasoning_effort=<value>` to `codex exec`

#### Scenario: Codex structured output
- **WHEN** structured output is requested
- **THEN** the executor SHALL pass `--output-schema ./schema.json -o ./report.json` and parse the output file

#### Scenario: Codex stderr is merged into the parser buffer
- **WHEN** codex writes to its stderr file descriptor
- **THEN** the executor SHALL merge stderr into the same buffer passed to `ParseCodexError` via the standard `cmd.Stderr = cmd.Stdout` (post-`StdoutPipe`, pre-`Start()`) idiom
- **AND** no separate stderr-capture goroutine or `io.Pipe` SHALL be required for this change (the existing merge idiom, shared with `runLoggedCommand`, is sufficient)

#### Scenario: Codex silent exit enriched from session log
- **WHEN** codex exits non-zero and the in-band stdout/stderr buffer contains no parser-matchable signal
- **AND** a `rollout-*.jsonl` file exists under `$CODEX_HOME/sessions/YYYY/MM/DD/` whose first-line `session_meta.cwd` matches the run's `WorkspaceDir` and whose `session_meta.timestamp` is within the try window
- **THEN** the executor SHALL populate `FailureEvidence` with `Source = "codex_session_log"`, `Message` derived from the last `event_msg` subtype, and a bounded `RawSignal` built from the `session_meta` line plus the last `event_msg` line
- **AND** it SHALL explicitly skip `token_count`, `response_item`, `turn_context`, and any payload field named `base_instructions` to avoid the verbosity hazard
- **AND** it SHALL NOT rely on `session_meta` for the resolved model name — `session_meta` carries only `model_provider` (e.g. `openai`); the resolved model name lives in `turn_context.payload.model`. The executor's own `model` local is the authoritative source for `TryResult.ResolvedModel`, not the session log

#### Scenario: Codex silent exit with no matching session log
- **WHEN** codex exits non-zero, the in-band buffer has no parser-matchable signal, and no session-log file matches the run's `WorkspaceDir` within the try window
- **THEN** the executor SHALL populate `FailureEvidence` with `Category = harness_launch`, `Source = "codex_no_session_log"`, and `Message = "codex launched but wrote no session log"`
- **AND** because the executor supplies typed Evidence with a Category, `ClassifyError` Priority 1 SHALL resolve the failure directly as `harness_launch`, yielding the existing `StrategyFreshRestart` + `FailureInfra` semantics (retry within budget with a fresh session; infra-class freeze pressure after 2+ failures caps a runner that repeatedly fails to launch)
- **AND** the intent is to label the failure correctly and surface the `codex_no_session_log` repro marker so the launch issue can be reproduced and fixed, NOT to skip retrying — the runner keeps retrying codex launch failures up to the budget

### Requirement: OpenCodeExecutor
The system SHALL provide an `OpenCodeExecutor` that invokes `opencode run <prompt> --format json` as a subprocess and returns a `TryResult`. The executor SHALL parse opencode's newline-delimited JSON event stream using the live schema captured in the rally-083 spike. When event parsing yields no usable final text, the executor SHALL NOT emit the raw subprocess output as the result `Summary`. When a resolved reasoning variant is specified for opencode, the executor SHALL pass it with `--variant`.

#### Scenario: OpenCode JSON event parsing
- **WHEN** the opencode subprocess completes
- **THEN** the executor SHALL parse each line as an `opencodeJSONEvent` JSON object
- **AND** it SHALL concatenate assistant text from ordered events with top-level `type: "text"` and nested `part.text`
- **AND** it SHALL count tool usage from top-level `type: "tool_use"` or nested `part.type: "tool"`

#### Scenario: OpenCode run with reasoning variant
- **WHEN** an opencode reasoning variant is resolved for the run
- **THEN** the executor SHALL pass `--variant <value>` to the opencode CLI

#### Scenario: OpenCode clean completion
- **WHEN** the opencode subprocess exits with status 0, no top-level `type: "error"` event was seen, and the stream contains assistant text or a `type: "step_finish"` event
- **THEN** the executor SHALL treat the opencode run as cleanly completed for parser purposes

#### Scenario: OpenCode error event
- **WHEN** the opencode event stream contains a top-level `type: "error"` event with no `part`
- **THEN** the executor SHALL return `Completed=false`
- **AND** it SHALL build a short bounded summary from `error.data.message`, optional `error.data.ref`, and fallback `error.name`
- **AND** it SHALL NOT place the raw subprocess stdout into `Summary`

#### Scenario: Parse yields no text
- **WHEN** the opencode subprocess completes but no `text` parts are extracted from its output
- **THEN** the executor SHALL return a `TryResult` with `Completed=false` and a short, bounded `Summary` indicating no parseable result
- **AND** the executor SHALL NOT place the raw subprocess stdout into `Summary`

### Requirement: AntigravityExecutor
The system SHALL provide an `AntigravityExecutor` that invokes `agy --print <prompt>` as a subprocess and returns a `TryResult`.

#### Scenario: Antigravity print-mode execution
- **WHEN** an Antigravity run is executed
- **THEN** the executor SHALL pass `--dangerously-skip-permissions`, `--print-timeout=<duration>`, and `--print <prompt>` to the `agy` CLI

#### Scenario: Antigravity model override
- **WHEN** an Antigravity model label is specified in configuration
- **THEN** the executor SHALL temporarily set that label in `~/.gemini/antigravity-cli/settings.json` for the duration of the run and restore the prior setting afterwards

#### Scenario: Antigravity conversation id capture
- **WHEN** the `agy` subprocess writes a print-mode conversation id to its log
- **THEN** the executor SHALL return that conversation id as the `TryResult.SessionID`

### Requirement: FixtureExecutor
The system SHALL provide a `FixtureExecutor` that replays precomputed git diffs and canned JSON outputs without invoking any real agent CLI.

#### Scenario: Fixture applies diff and returns canned result
- **WHEN** a fixture executor is invoked with a diff path and output path
- **THEN** it SHALL apply the diff via `git apply`, commit the changes, and return the TryResult parsed from the output JSON file

#### Scenario: Fixture supports configurable delay
- **WHEN** a delay duration is configured on the fixture executor
- **THEN** it SHALL sleep for that duration before returning, simulating agent execution time

#### Scenario: Fixture handles already-applied diffs
- **WHEN** the diff has already been applied (e.g., retry scenario)
- **THEN** the executor SHALL detect this via `git apply --reverse --check` and skip re-application

### Requirement: Executor summaries are bounded final text
Executors SHALL emit bounded final assistant text, structured `TryResult.Summary` text, or a short bounded fallback indicator rather than a full run transcript or start-of-run narration. The exact 3000-rune persisted final-snippet cap is enforced at the persistence boundary; executor-local bounds are for safe fallback behavior and do not define the durable storage limit.

#### Scenario: Final assistant summary
- **WHEN** an executor parses a final assistant message or structured summary
- **THEN** it SHALL return that bounded final text as `TryResult.Summary`

#### Scenario: Text tail fallback
- **WHEN** no parsed final assistant text is available
- **THEN** the executor SHALL return a bounded tail of process text or an explicit no-finalization/error indicator rather than the raw transcript

### Requirement: Unsupported reasoning injection
The system SHALL avoid passing reasoning-effort flags to executors whose harnesses do not expose a Rally-usable flag. Gemini SHALL receive no reasoning flag. Antigravity SHALL receive no separate reasoning flag; reasoning preferences for antigravity SHALL be represented by selecting an appropriate model label through existing model selection.

#### Scenario: Gemini reasoning skipped
- **WHEN** a reasoning effort is configured for a Gemini run
- **THEN** the executor SHALL NOT pass a reasoning, effort, or variant flag to the `gemini` CLI

#### Scenario: Antigravity reasoning uses model label only
- **WHEN** a reasoning preference is configured for an Antigravity run
- **THEN** the executor SHALL NOT pass a separate reasoning flag to `agy`
- **AND** any reasoning-specific behavior SHALL come from the selected Antigravity model label

### Requirement: Reasoning effort propagation
The system SHALL propagate resolved reasoning effort from routing/configuration into executor invocation through typed runner options rather than ad hoc string parsing in executors. The resolved effort SHALL be available on `agent.ResolvedAgent` and `agent.RunOptions` or equivalent typed structures before an executor builds its subprocess arguments.

#### Scenario: Resolved effort reaches executor options
- **WHEN** route resolution selects a runner with a reasoning effort
- **THEN** the runner SHALL pass that effort to the executor through typed run options

#### Scenario: Executor does not parse route specs
- **WHEN** an executor builds subprocess arguments
- **THEN** it SHALL read model and reasoning effort from typed options and SHALL NOT parse route specification strings directly

### Requirement: opencode provider usage-limit evidence
The system SHALL detect opencode subscription-provider usage limits and supply `FailureEvidence` with category `usage_limit` and parsed reset timing, so the runner benches the quota scope instead of classifying the failure `agent_error`. Detection SHALL recognize the limit text regardless of opencode's error wrapper: provider limits arrive as `AI_APICallError` or `AI_RetryError` (e.g. `Failed after N attempts. Last error: …`), not opencode's native `UsageLimitError`/`QuotaExceededError`, and the authoritative carrier is opencode's server log, where the error is a flat field `error.error="<Wrapper>: <message>"` on a `level=ERROR message="stream error"` line rather than a nested JSON `data.message`. Detection SHALL therefore match the limit text in the error name, the error message, and the flat server-log `error.error` value, including the substrings `usage limit reached`, `monthly usage limit`, and `usage limit reached for`. The system SHALL parse opencode's reset phrasings into `ResetAfter`/`ResetAt`: space-separated spans (`Resets in 7 days`, `… 5 hour`, `… 30 minutes`) and absolute timestamps (`reset at <YYYY-MM-DD HH:MM:SS>`, `will reset at …`), interpreting the absolute timestamp — which carries no timezone marker — in local time and treating it as approximate. Because opencode retries provider errors internally and may emit nothing to the `--format json` stream before Rally's stall threshold fires, the executor SHALL, when a try stalls or errors without a usable result, surface usage-limit evidence read from the tail of opencode's server log (`~/.local/share/opencode/log/opencode.log`), correlating the run's session without relying on stdout by matching opencode's session-creation line (`message=created … directory=<WorkspaceDir>`) to recover the session id, with a `providerID=<provider>` plus try-window fallback.

#### Scenario: opencode subscription-provider monthly limit
- **WHEN** an opencode run fails with a provider message such as `Monthly usage limit reached. Resets in 7 days.` (directly or wrapped as `AI_APICallError`/`AI_RetryError`/`UnknownError`)
- **THEN** the system SHALL produce `FailureEvidence` with category `usage_limit` and a reset window of approximately 7 days

#### Scenario: opencode subscription-provider rolling limit with absolute reset
- **WHEN** an opencode run fails with a provider message such as `Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51`
- **THEN** the system SHALL produce `FailureEvidence` with category `usage_limit` and a `ResetAt` parsed from the absolute reset timestamp

#### Scenario: Limit observable despite stalled JSON stream
- **WHEN** an opencode try is stall-killed or ends without a usable result while opencode is retrying a provider usage limit internally (emitting nothing to stdout), and opencode's server log records a usage-limit `error.error="<Wrapper>: <message>"` line for the session whose `message=created … directory=` matches the run's `WorkspaceDir`
- **THEN** the executor SHALL surface that usage-limit signature as `FailureEvidence` rather than letting the failure default to `agent_error`, correlating the session from the server log without depending on stdout

### Requirement: Unified disk-log fallback for all harnesses
The system SHALL provide a disk-log fallback for every harness (claude, codex, opencode, antigravity) so that when a try fails with no in-band parser-matchable signal, the executor reads the harness's native session or server log and populates `FailureEvidence` with a bounded diagnostic tail. Every fallback SHALL: produce a non-nil `FailureEvidence` with a non-empty `Category` — either a recognised category when the log shows a known error shape, or `unidentified_issue` when it does not; set a harness-specific `Source` marker (`codex_session_log`, `claude_session_log`, `opencode_disk_log`, `antigravity_glog`, `codex_no_session_log`); bound `RawSignal` to 256 runes; explicitly skip any payload containing prompts, credentials, or user content; and treat missing/unreadable log files as a non-error (the fallback produces no evidence and the runner falls through to the existing classification path). When the in-band output already produced usable evidence, the disk-log fallback SHALL NOT override it — the in-band evidence is authoritative. When the disk-log fallback produces evidence and the in-band output did not, the disk-log evidence SHALL populate `TryResult.Evidence`.

#### Scenario: Disk log fallback does not override in-band evidence
- **WHEN** an executor's in-band output already produced a non-nil `FailureEvidence` with a recognised `Category`
- **THEN** the disk-log fallback SHALL NOT replace it
- **AND** the in-band evidence SHALL be authoritative

#### Scenario: Disk log fallback covers missing in-band signal
- **WHEN** an executor's in-band output has no parser-matchable signal and the try failed
- **AND** the harness's native session or server log exists and is readable
- **THEN** the executor SHALL read the log, extract a bounded structural tail, and populate `FailureEvidence` with a non-empty `Category` and the harness-specific `Source` marker
- **AND** the `Category` SHALL be `unidentified_issue` when no known error shape is recognised in the log tail

#### Scenario: Missing disk log is not an error
- **WHEN** the harness's native session or server log does not exist or is unreadable
- **THEN** the executor SHALL NOT treat it as an error
- **AND** the executor SHALL fall through to the existing classification path (runner-side `ClassifyError` or `safe_exec_error`)

### Requirement: OpenCode try-budget exhaustion evidence
The system SHALL surface a bounded diagnostic signal from the opencode server log when an opencode try times out without producing a parseable result, so try-budget exhaustion is distinguishable from a real opencode crash in telemetry. This requirement EXTENDS the existing opencode disk-log fallback machinery (`attachOpenCodeFailureEvidence` / `openCodeServerLogFailureEvidence` / `readOpenCodeServerLogTail` / `openCodeEvidenceFromServerLog` in `internal/agent/opencode.go`) — it does not introduce a parallel session-id correlation mechanism, since the existing locator already correlates by opencode session id (from the `message=created id=… directory=<WorkspaceDir>` line via `openCodeCreatedSessionID`) with a `providerID=<provider>` + try-window fallback (`openCodeLogLineInWindow`). When the opencode subprocess is killed by the runner-side try or run budget without ever emitting a usable `--format json` result, the executor SHALL additionally keep `level=WARN` and `level=ERROR` lines plus the structural `message=created` / `message="loop session.id=…"` / `message=stream` markers from `$XDG_DATA_HOME/opencode/log/opencode.log` (default `~/.local/share/opencode/log/opencode.log`), bounded to at most sixteen lines, alongside the existing usage-limit extraction path. The resulting `FailureEvidence` SHALL set `Source = "opencode_disk_log"`, `Message` from the last error line (or `"try budget exhausted; no parseable output"` when no error line is present), and `RawSignal` from the bounded filtered tail. The executor SHALL explicitly skip per-token and per-permission log lines, which are the verbosity hazard in the opencode log.

#### Scenario: Budget-exhausted opencode try carries disk-log tail
- **WHEN** an opencode try is killed by the runner-side try or run budget without producing a parseable `--format json` result
- **AND** the opencode server log contains WARN or ERROR lines for the try's session id
- **THEN** the executor SHALL populate `FailureEvidence` with `Source = "opencode_disk_log"` and a bounded `RawSignal` containing the WARN/ERROR lines and structural markers
- **AND** telemetry SHALL distinguish the failure from a real opencode crash via the `failure_evidence.source` value

#### Scenario: Budget-exhausted opencode try without log signal
- **WHEN** an opencode try is killed by the runner-side try or run budget without producing a parseable result
- **AND** the opencode server log contains no WARN/ERROR lines for the try's session id (opencode made progress but never finished)
- **THEN** the executor SHALL populate `FailureEvidence` with `Source = "opencode_disk_log"`, `Message = "try budget exhausted; no parseable output"`, and a `RawSignal` built from the structural `loop`/`stream` markers alone

#### Scenario: Verbose log lines are not surfaced
- **WHEN** the opencode server log contains per-token, per-tool-call, or per-permission log lines alongside the WARN/ERROR and structural markers
- **THEN** the executor SHALL exclude them from the bounded `RawSignal`
- **AND** the resulting evidence SHALL NOT exceed the standard 256-rune signal bound

### Requirement: Antigravity-named reliability parser
The system SHALL name the antigravity reliability parser `ParseAntigravityError` (renamed from the inherited `ParseGeminiError`), reflecting that antigravity is the only Google-owned harness after the gemini CLI removal. The parser's matching behaviour (RESOURCE_EXHAUSTED, Individual quota reached, Resets in, HTTP 429, IneligibleTierError, UNSUPPORTED_CLIENT, no longer supported for Gemini Code Assist) SHALL be unchanged. Only the `gemini auth or unsupported client` text-pattern entry in `ErrorPatterns` (scoped `Harness: "gemini"`) SHALL be removed, because it scoped to the removed harness and the antigravity-scoped eligibility duplicate already covers the same text. The `gemini-cli exit 1` pattern (currently scoped `Harness: "antigravity"` because antigravity shells out to the `gemini-cli` binary) SHALL be RETAINED but RENAMED to `antigravity gemini-cli exit 1` — it is a real classification path for antigravity's exit-1-with-no-other-signal cases.

#### Scenario: Parser name reflects the surviving harness
- **WHEN** the antigravity executor captures an error buffer for reliability classification
- **THEN** it SHALL invoke `ParseAntigravityError` (not `ParseGeminiError`)

#### Scenario: Removed gemini-only text pattern does not match
- **WHEN** the harness-scoped text-pattern table is consulted for a failure
- **THEN** no pattern scoped to the removed `gemini` harness SHALL exist
- **AND** no pattern with `Harness: "gemini"` SHALL exist (the `gemini auth or unsupported client` pattern is removed)

#### Scenario: Antigravity-scoped exit-1 pattern is retained
- **WHEN** an antigravity try exits 1 with no other parser-matchable signal in the log tail
- **THEN** the renamed `antigravity gemini-cli exit 1` pattern (scoped `Harness: "antigravity"`) SHALL still match and classify the failure as `agent_error`
- **AND** antigravity eligibility errors (`IneligibleTierError`, `UNSUPPORTED_CLIENT`, `no longer supported for Gemini Code Assist`) SHALL continue to classify as `auth_or_proxy` for the antigravity harness

