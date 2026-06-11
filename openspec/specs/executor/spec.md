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
The system SHALL provide a `ClaudeExecutor` that invokes `claude -p <prompt> --output-format stream-json --verbose` as a subprocess, parses the NDJSON stream inline, and returns a `TryResult`.

#### Scenario: Claude run with model override
- **WHEN** a Claude model is specified in configuration
- **THEN** the executor SHALL pass `--model <model>` to the claude CLI

#### Scenario: Claude stream-json parsing
- **WHEN** the claude subprocess completes
- **THEN** the executor SHALL parse each line as a `claudeStreamEvent` JSON object and extract the `result` field from events with `type: "result"`

### Requirement: CodexExecutor
The system SHALL provide a `CodexExecutor` that invokes `codex exec` as a subprocess with appropriate flags and returns a `TryResult`.

#### Scenario: Codex run with full-auto mode
- **WHEN** a codex run is executed
- **THEN** the executor SHALL pass `--full-auto` and `--dangerously-bypass-approvals-and-sandbox` flags

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
The system SHALL provide an `OpenCodeExecutor` that invokes `opencode run <prompt> --format json` as a subprocess and returns a `TryResult`. The executor SHALL parse opencode's newline-delimited JSON event stream using the live schema captured in the rally-083 spike. When event parsing yields no usable final text, the executor SHALL NOT emit the raw subprocess output as the result `Summary`.

#### Scenario: OpenCode JSON event parsing
- **WHEN** the opencode subprocess completes
- **THEN** the executor SHALL parse each line as an `opencodeJSONEvent` JSON object
- **AND** it SHALL concatenate assistant text from ordered events with top-level `type: "text"` and nested `part.text`
- **AND** it SHALL count tool usage from top-level `type: "tool_use"` or nested `part.type: "tool"`

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

