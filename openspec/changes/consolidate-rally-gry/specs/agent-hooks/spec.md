## ADDED Requirements

### Requirement: Structured output collection
Each agent executor SHALL collect a structured progress report after session completion. The strategy is **resume-and-report**: after the agent session ends, resume the same session with a reporting prompt, preserving input token caching. All four CLIs support this pattern. The hook system serves as the trigger mechanism.

**Collection strategy by agent:**

| Agent | Resume command | Schema support | Output format | Fallback |
|-------|---------------|----------------|---------------|----------|
| Claude Code | `claude -c -p "<prompt>" --json-schema '<schema>' --output-format json` | Yes (`--json-schema`) | Top-level JSON | Stop hook block-and-report |
| Codex | `codex exec resume --last "<prompt>" --output-schema ./schema.json -o ./report.json` | Yes (`--output-schema`) | Output file | Stop hook block-and-report |
| Gemini CLI | `gemini --resume -p "<prompt>" --output-format json` | No (prompt-guided) | `{"response": "...", "session_id": "...", "stats": {...}}` | None needed |
| OpenCode | `opencode run --continue "<prompt>" --format json` | No (prompt-guided) | JSON events | None needed |

**Docs:**
- Claude Code CLI: https://code.claude.com/docs/en/cli-usage.md
- Claude Code hooks: https://code.claude.com/docs/en/hooks.md
- Codex hooks: https://developers.openai.com/codex/hooks
- Gemini CLI hooks: https://geminicli.com/docs/hooks/
- OpenCode plugins: https://opencode.ai/docs/plugins

### Requirement: Resume-and-report strategy
After a session completes, the executor SHALL resume the same session with a reporting prompt that requests the structured JSON report. This preserves the full conversation context and leverages input token caching — the agent already has complete knowledge of what it did.

#### Scenario: Claude Code resume-and-report
- **WHEN** a ClaudeExecutor session completes
- **THEN** the executor SHALL run `claude -c -p "<reporting prompt>" --json-schema '<schema>' --output-format json`
- **AND** parse the JSON output into a `SessionResult`
- **NOTE**: `-c` continues the most recent conversation. `--json-schema` validates the output against the report schema.

#### Scenario: Codex resume-and-report
- **WHEN** a CodexExecutor session completes
- **THEN** the executor SHALL run `codex exec resume --last "<reporting prompt>" --output-schema ./schema.json -o ./report.json`
- **AND** parse the output file into a `SessionResult`

#### Scenario: Gemini CLI resume-and-report
- **WHEN** a GeminiExecutor session completes
- **THEN** the executor SHALL run `gemini --resume -p "<reporting prompt>" --output-format json`
- **AND** extract the report from the `response` field of the top-level JSON output
- **NOTE**: Gemini's `--output-format json` wraps the response in `{"session_id": "...", "response": "...", "stats": {...}}`. The report JSON is inside the `response` string and must be parsed separately. Gemini has no schema validation flag — the reporting prompt must instruct the agent to produce the exact JSON shape. Gemini stderr is noisy (MCP server registration messages etc.) — capture and discard stderr.

#### Scenario: OpenCode resume-and-report
- **WHEN** an OpenCodeExecutor session completes
- **THEN** the executor SHALL run `opencode run --continue "<reporting prompt>" --format json`
- **AND** parse the JSON output into a `SessionResult`
- **NOTE**: OpenCode has no schema validation flag — the reporting prompt must instruct the agent to produce the exact JSON shape.

### Requirement: Stop hook as trigger and fallback
The executor SHALL register stop hooks as the mechanism to trigger the resume-and-report flow. When a stop hook fires, it signals the executor that the session has ended and a resume command should be issued.

For Claude Code and Codex, stop hooks also serve as a **fallback** via block-and-report if the resume approach fails:
1. On first `Stop` event (`stop_hook_active: false`), return `{"decision": "block", "reason": "<reporting prompt>"}` to force a reporting turn.
2. On second `Stop` event (`stop_hook_active: true`), parse `last_assistant_message` for the JSON report.

**Hook reference:**

| Agent | Event | Config location | Key stdin fields |
|-------|-------|----------------|-----------------|
| Claude Code | `Stop` | `.claude/settings.json` | `session_id`, `transcript_path`, `last_assistant_message`, `stop_hook_active` |
| Codex | `Stop` | `.codex/hooks.json` | `session_id`, `transcript_path`, `last_assistant_message`, `stop_hook_active` |
| Gemini CLI | `SessionEnd` | `.gemini/settings.json` | Undocumented (advisory) |
| OpenCode | `session.idle` | `.opencode/plugins/` (JS/TS) | `ctx` with `project`, `directory`, `worktree` |

#### Scenario: Claude/Codex fallback to block-and-report
- **WHEN** the resume-and-report command fails for Claude Code or Codex
- **THEN** the executor SHALL fall back to the block-and-report stop hook strategy

### Requirement: Structured report schema
The structured output SHALL produce a JSON report with the fields: `completed` (boolean), `summary` (string), `remaining_work` (string), `message_addressed` (boolean or null), and `files_changed` (array of strings).

#### Scenario: Report maps to SessionResult
- **WHEN** the output JSON is parsed
- **THEN** each field SHALL map to the corresponding `SessionResult` field used by the Executor interface

### Requirement: Output parsing
The executor SHALL parse the structured output and construct a `SessionResult`. If parsing fails, the executor SHALL treat the session as incomplete with an error summary.

#### Scenario: Valid JSON parsed successfully
- **WHEN** the output is well-formed JSON with all required fields
- **THEN** the executor SHALL return a populated `SessionResult` with all fields set

#### Scenario: Malformed output treated as failure
- **WHEN** the output cannot be parsed as valid JSON
- **THEN** the executor SHALL return a `SessionResult` with `Completed: false` and a summary indicating parse failure

#### Scenario: Missing optional fields handled gracefully
- **WHEN** the output JSON omits `message_addressed` (no inbox message was consumed)
- **THEN** the executor SHALL set `SessionResult.MessageAddressed` to nil
