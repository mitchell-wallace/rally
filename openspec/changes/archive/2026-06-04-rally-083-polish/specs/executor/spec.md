## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Executor summaries are bounded final text
Executors SHALL emit bounded final assistant text, structured `TryResult.Summary` text, or a short bounded fallback indicator rather than a full run transcript or start-of-run narration. The exact 3000-rune persisted final-snippet cap is enforced at the persistence boundary; executor-local bounds are for safe fallback behavior and do not define the durable storage limit.

#### Scenario: Final assistant summary
- **WHEN** an executor parses a final assistant message or structured summary
- **THEN** it SHALL return that bounded final text as `TryResult.Summary`

#### Scenario: Text tail fallback
- **WHEN** no parsed final assistant text is available
- **THEN** the executor SHALL return a bounded tail of process text or an explicit no-finalization/error indicator rather than the raw transcript
