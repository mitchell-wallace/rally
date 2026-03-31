## ADDED Requirements

### Requirement: Structured output collection
Each agent executor SHALL collect a structured progress report after session completion. The collection strategy varies per agent to account for different CLI maturity levels and known issues. All CLI flags referenced below have been tested and verified against actual agent CLIs.

**Collection strategy by agent:**

| Agent | Primary strategy | Command | Schema support | Fallback |
|-------|-----------------|---------|----------------|----------|
| Claude Code | Block-and-report (stop hook) | Stop hook with `decision: "block"` | Yes (prompt-guided in hook context) | Resume: `claude -c -p "<prompt>" --json-schema '<schema>' --output-format json` |
| Codex | Resume-and-report | `codex exec resume --last "<prompt>" --output-schema ./schema.json -o ./report.json` | Yes (`--output-schema`) | Stop hook block-and-report |
| Gemini CLI | Resume-and-report | `gemini --resume -p "<prompt>" --output-format json` | No (prompt-guided) | None needed |
| OpenCode | Resume-and-report | `opencode run --continue "<prompt>" --format json` | No (prompt-guided) | None needed |

**Why per-agent strategies**: Claude Code has a potential cache invalidation bug with resume behavior, making block-and-report more reliable as the primary strategy. Codex hooks are flagged as experimental, so resume is preferred there with hooks as fallback. Gemini and OpenCode only support resume.

**Docs:**
- Claude Code CLI: https://code.claude.com/docs/en/cli-usage.md
- Claude Code hooks: https://code.claude.com/docs/en/hooks.md
- Codex hooks: https://developers.openai.com/codex/hooks
- Gemini CLI hooks: https://geminicli.com/docs/hooks/
- OpenCode plugins: https://opencode.ai/docs/plugins

### Requirement: Claude Code block-and-report (primary)
The ClaudeExecutor SHALL use the stop hook block-and-report strategy as its primary output collection method.

#### Scenario: Claude block-and-report flow
- **WHEN** a ClaudeExecutor session reaches the `Stop` event (`stop_hook_active: false`)
- **THEN** the hook SHALL return `{"decision": "block", "reason": "<reporting prompt>"}` to force a reporting turn
- **AND** on the second `Stop` event (`stop_hook_active: true`), parse `last_assistant_message` for the JSON report

#### Scenario: Claude fallback to resume
- **WHEN** the block-and-report hook fails to produce valid output
- **THEN** the executor MAY fall back to resume: `claude -c -p "<reporting prompt>" --json-schema '<schema>' --output-format json`
- **NOTE**: `-c` continues the most recent conversation. `--json-schema` validates output. This fallback is available but demoted due to potential cache invalidation.

### Requirement: Codex resume-and-report (primary)
The CodexExecutor SHALL use resume-and-report as its primary output collection method, with stop hook block-and-report as fallback.

#### Scenario: Codex resume-and-report
- **WHEN** a CodexExecutor session completes
- **THEN** the executor SHALL run `codex exec resume --last "<reporting prompt>" --output-schema ./schema.json -o ./report.json`
- **AND** parse the output file into a `SessionResult`

#### Scenario: Codex fallback to block-and-report
- **WHEN** the resume command fails for Codex
- **THEN** the executor SHALL fall back to the block-and-report stop hook strategy
- **NOTE**: Codex hooks are flagged as experimental, so this is the fallback rather than primary.

### Requirement: Gemini CLI resume-and-report
The GeminiExecutor SHALL use resume-and-report for output collection.

#### Scenario: Gemini resume-and-report
- **WHEN** a GeminiExecutor session completes
- **THEN** the executor SHALL run `gemini --resume -p "<reporting prompt>" --output-format json`
- **AND** extract the report from the `response` field of the top-level JSON output
- **NOTE**: Gemini's `--output-format json` wraps the response in `{"session_id": "...", "response": "...", "stats": {...}}`. The report JSON is inside the `response` string and must be parsed separately. Gemini has no schema validation flag — the reporting prompt must instruct the agent to produce the exact JSON shape. Gemini stderr is noisy (MCP server registration messages etc.) — capture and discard stderr.

### Requirement: OpenCode resume-and-report
The OpenCodeExecutor SHALL use resume-and-report for output collection.

#### Scenario: OpenCode resume-and-report
- **WHEN** an OpenCodeExecutor session completes
- **THEN** the executor SHALL run `opencode run --continue "<reporting prompt>" --format json`
- **AND** parse the JSON output into a `SessionResult`
- **NOTE**: OpenCode has no schema validation flag — the reporting prompt must instruct the agent to produce the exact JSON shape.

### Requirement: Stop hooks as triggers
The executor SHALL register stop hooks to detect session completion and trigger the appropriate collection strategy.

**Hook reference:**

| Agent | Event | Config location | Key stdin fields |
|-------|-------|----------------|-----------------|
| Claude Code | `Stop` | `.claude/settings.json` | `session_id`, `transcript_path`, `last_assistant_message`, `stop_hook_active` |
| Codex | `Stop` | `.codex/hooks.json` | `session_id`, `transcript_path`, `last_assistant_message`, `stop_hook_active` |
| Gemini CLI | `SessionEnd` | `.gemini/settings.json` | Undocumented (advisory) |
| OpenCode | `session.idle` | `.opencode/plugins/` (JS/TS) | `ctx` with `project`, `directory`, `worktree` |

#### Scenario: Hook signals session end
- **WHEN** an agent's stop/end hook fires
- **THEN** the executor SHALL proceed with the agent's primary collection strategy (block-and-report for Claude, resume for others)

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
