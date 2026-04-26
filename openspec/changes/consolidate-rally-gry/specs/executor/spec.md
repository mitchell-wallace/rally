## ADDED Requirements

### Requirement: Executor interface
The system SHALL define an `Executor` interface with a single method `Execute(ctx context.Context, opts RunOptions) (*TryResult, error)` that abstracts agent lifecycle. Each call to Execute represents one try (one agent CLI invocation).

#### Scenario: Executor returns structured result
- **WHEN** an Executor implementation completes a try
- **THEN** it SHALL return a `TryResult` containing: `Completed` (bool), `Summary` (string), `RemainingWork` (string), `MessageAddressed` (*bool), `FilesChanged` ([]string)
- **NOTE**: `CommitHash` is NOT part of `TryResult` — it is determined by the relay runner after the executor returns, by comparing HEAD before and after the try.

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
The system SHALL provide an `OpenCodeExecutor` that invokes `opencode run <prompt> --format json` as a subprocess and returns a `TryResult`.

#### Scenario: OpenCode JSON event parsing
- **WHEN** the opencode subprocess completes
- **THEN** the executor SHALL parse each line as an `opencodeJSONEvent` JSON object and extract `text` from events with `type: "text"`

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
