## ADDED Requirements

### Requirement: Stop hook registration
Each agent executor SHALL register a stop hook using the agent CLI's native hook mechanism (Claude Code stop hooks, Gemini AfterAgent, Codex and OpenCode equivalents). The hook SHALL execute a rally-provided prompt that collects structured output from the agent at run end.

#### Scenario: Claude stop hook registered
- **WHEN** a ClaudeExecutor prepares a run
- **THEN** it SHALL configure a Claude Code stop hook that invokes the rally reporting prompt

#### Scenario: Gemini AfterAgent hook registered
- **WHEN** a GeminiExecutor prepares a run
- **THEN** it SHALL configure the Gemini AfterAgent event handler with the rally reporting prompt

### Requirement: Structured report prompt
The stop hook SHALL ask the agent to produce a JSON report with the fields: `completed` (boolean), `summary` (string), `remaining_work` (string), `message_addressed` (boolean or null), and `files_changed` (array of strings).

#### Scenario: Hook prompt produces valid JSON
- **WHEN** the stop hook fires at the end of a run
- **THEN** the agent SHALL receive a prompt requesting a JSON object with the required fields

#### Scenario: Report maps to RunResult
- **WHEN** the hook output JSON is parsed
- **THEN** each field SHALL map to the corresponding `RunResult` field used by the Executor interface

### Requirement: Hook output parsing
The executor SHALL parse the stop hook's JSON output and construct a `RunResult`. If parsing fails, the executor SHALL treat the run as incomplete with an error summary.

#### Scenario: Valid JSON parsed successfully
- **WHEN** the stop hook returns well-formed JSON with all required fields
- **THEN** the executor SHALL return a populated `RunResult` with all fields set

#### Scenario: Malformed output treated as failure
- **WHEN** the stop hook output cannot be parsed as valid JSON
- **THEN** the executor SHALL return a `RunResult` with `Completed: false` and a summary indicating parse failure

#### Scenario: Missing optional fields handled gracefully
- **WHEN** the stop hook JSON omits `message_addressed` (no inbox message was consumed)
- **THEN** the executor SHALL set `RunResult.MessageAddressed` to nil

### Requirement: Gemini JSON output exception
The GeminiExecutor SHALL use JSON output mode rather than stop hooks for structured output, since Gemini streams all output. The JSON output format SHALL match the same schema as the stop hook report.

#### Scenario: Gemini output parsed from JSON mode
- **WHEN** a Gemini run completes
- **THEN** the executor SHALL parse the structured JSON from Gemini's output stream rather than from a stop hook
