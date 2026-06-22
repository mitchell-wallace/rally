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
The system SHALL provide a `CodexExecutor` that invokes `codex exec` as a subprocess with appropriate flags and returns a `TryResult`. When a resolved reasoning effort is specified for Codex, the executor SHALL inject it as a config override with `-c model_reasoning_effort=<value>`, not as a nonexistent CLI reasoning flag.

#### Scenario: Codex run with approval bypass mode
- **WHEN** a codex run is executed
- **THEN** the executor SHALL pass `--dangerously-bypass-approvals-and-sandbox`

#### Scenario: Codex run with reasoning effort
- **WHEN** a Codex reasoning effort is resolved for the run
- **THEN** the executor SHALL pass `-c model_reasoning_effort=<value>` to `codex exec`

#### Scenario: Codex structured output
- **WHEN** structured output is requested
- **THEN** the executor SHALL pass `--output-schema ./schema.json -o ./report.json` and parse the output file

### Requirement: GeminiExecutor
The system SHALL provide a `GeminiExecutor` that invokes the `gemini` CLI with `--output-format json` as a subprocess and returns a `TryResult`.

#### Scenario: Gemini JSON output parsing
- **WHEN** the gemini subprocess completes
- **THEN** the executor SHALL parse the JSON output, extract the `response` field from the `{"response": "...", "session_id": "...", "stats": {...}}` wrapper, and re-parse the response content
- **AND** stderr SHALL be discarded (noisy with MCP server messages)

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

