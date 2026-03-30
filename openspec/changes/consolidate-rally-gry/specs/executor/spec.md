## ADDED Requirements

### Requirement: Executor interface
The system SHALL define an `Executor` interface with a single method `Execute(ctx context.Context, opts RunOptions) (*RunResult, error)` that abstracts agent lifecycle.

#### Scenario: Executor returns structured result
- **WHEN** an Executor implementation completes a run
- **THEN** it SHALL return a `RunResult` containing: `Completed` (bool), `Summary` (string), `RemainingWork` (string), `MessageAddressed` (*bool), `FilesChanged` ([]string), `CommitHash` (string)

#### Scenario: Executor respects context cancellation
- **WHEN** the provided context is cancelled during execution
- **THEN** the executor SHALL terminate the agent subprocess and return a context cancellation error

### Requirement: RunOptions prompt building
The system SHALL build agent prompts from `RunOptions` fields: `Persona`, `TaskName`, `TaskRequirements`, `InboxMessage`, `PreviousSummary`, and an optional explicit `Prompt` override.

#### Scenario: Explicit prompt overrides built prompt
- **WHEN** `RunOptions.Prompt` is non-empty
- **THEN** the executor SHALL use it verbatim instead of building a prompt from other fields

#### Scenario: Previous summary included on retry
- **WHEN** `RunOptions.PreviousSummary` is non-empty
- **THEN** the built prompt SHALL include a "Previous Attempt Summary" section containing the summary text

### Requirement: ClaudeExecutor
The system SHALL provide a `ClaudeExecutor` that invokes `claude -p <prompt>` as a subprocess, captures stdout, and returns a `RunResult`.

#### Scenario: Claude run with model override
- **WHEN** a Claude model is specified in configuration
- **THEN** the executor SHALL pass `--model <model>` to the claude CLI

#### Scenario: Claude run captures output
- **WHEN** the claude subprocess completes
- **THEN** the executor SHALL capture stdout and stderr, and record the terminal transcript

### Requirement: CodexExecutor
The system SHALL provide a `CodexExecutor` that invokes `codex exec` as a subprocess with appropriate flags and returns a `RunResult`.

#### Scenario: Codex run with full-auto mode
- **WHEN** a codex run is executed
- **THEN** the executor SHALL pass `--full-auto` and `--dangerously-bypass-approvals-and-sandbox` flags

### Requirement: GeminiExecutor
The system SHALL provide a `GeminiExecutor` that invokes the `gemini` CLI as a subprocess and returns a `RunResult`.

#### Scenario: Gemini uses JSON output mode
- **WHEN** a Gemini run is executed
- **THEN** the executor SHALL configure JSON output mode since Gemini streams everything otherwise

### Requirement: OpenCodeExecutor
The system SHALL provide an `OpenCodeExecutor` that invokes `opencode run` as a subprocess and returns a `RunResult`.

#### Scenario: OpenCode run execution
- **WHEN** an opencode run is executed
- **THEN** the executor SHALL invoke `opencode run <prompt>` and capture the result

### Requirement: FixtureExecutor
The system SHALL provide a `FixtureExecutor` that replays precomputed git diffs and canned JSON outputs without invoking any real agent CLI.

#### Scenario: Fixture applies diff and returns canned result
- **WHEN** a fixture executor is invoked with a diff path and output path
- **THEN** it SHALL apply the diff via `git apply`, commit the changes, and return the RunResult parsed from the output JSON file

#### Scenario: Fixture supports configurable delay
- **WHEN** a delay duration is configured on the fixture executor
- **THEN** it SHALL sleep for that duration before returning, simulating agent execution time

#### Scenario: Fixture handles already-applied diffs
- **WHEN** the diff has already been applied (e.g., retry scenario)
- **THEN** the executor SHALL detect this via `git apply --reverse --check` and skip re-application
