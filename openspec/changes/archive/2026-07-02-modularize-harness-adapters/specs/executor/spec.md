## MODIFIED Requirements

### Requirement: Executor interface

The system SHALL define an `Executor` interface with the six current lifecycle
methods: `Execute(ctx context.Context, opts RunOptions) (*TryResult, error)`,
`ResumeSupported() bool`, `RotateSupported() bool`,
`LivenessProbeSupported() bool`, `RotateModel(newModel string) error`, and
`ProbeLiveness(ctx context.Context) (bool, error)`. Each call to `Execute`
represents one try (one agent CLI invocation). The `TryResult` SHALL carry an
optional `FailureEvidence` value that an executor MAY populate when it can
extract structured failure information (category, provider, quota scope, status
code, and reset/retry timing) from the agent's output, and SHALL carry a
`ResolvedModel` string reflecting the model actually passed to the underlying CLI
on this try (the executor's resolved default when the route did not supply an
explicit model). These fields are optional in the sense that consumers SHALL
tolerate empty values: when an executor cannot supply `Evidence` — including
process-level failures where the executor returns an error with a nil or partial
`TryResult` — consumers SHALL fall back to runner-side classification; when
`ResolvedModel` is empty, the runner SHALL fall back to the route-supplied model.
The executor SHALL always attempt to populate `ResolvedModel` from the same value
it passes to the CLI's `--model` (or equivalent) flag, so telemetry reports the
model the agent actually used rather than the route-resolved model whenever the
two differ.

#### Scenario: Executor returns structured result

- **WHEN** an Executor implementation completes a try
- **THEN** it SHALL return a `TryResult` containing: `Completed` (bool),
  `Summary` (string), `RemainingWork` (string), `MessageAddressed` (*bool),
  `FilesChanged` ([]string), `SessionID` (string), `ResolvedModel` (string),
  `ToolCalls` (int), and an optional `Evidence` (*FailureEvidence)
- **NOTE**: `CommitHash` is NOT part of `TryResult` — it is determined by the
  relay runner after the executor returns, by comparing HEAD before and after the
  try.

#### Scenario: Executor exposes lifecycle support methods

- **WHEN** the runner needs to decide whether a harness supports resume, model
  rotation, or liveness probing
- **THEN** it SHALL use the executor's `ResumeSupported`, `RotateSupported`,
  `LivenessProbeSupported`, `RotateModel`, and `ProbeLiveness` methods rather
  than type assertions against concrete adapter types

#### Scenario: Executor populates failure evidence when available

- **WHEN** an executor parses a structured provider/quota/config error from the
  agent's output (e.g. a usage-limit event with a reset window)
- **THEN** it SHALL set `TryResult.Evidence` with the corresponding category and
  any parsed reset/retry timing and quota scope

#### Scenario: Executor reports the resolved model

- **WHEN** an executor invokes the agent CLI with a `--model` (or equivalent)
  flag
- **THEN** `TryResult.ResolvedModel` SHALL be set to the same value the flag
  received, including the executor's own default when the route did not supply a
  model
- **AND** the runner SHALL use `ResolvedModel` in the `runner` telemetry tag
  whenever it is non-empty

#### Scenario: Evidence absent for process-level failure

- **WHEN** a try fails before producing a usable `TryResult` (e.g. `fork/exec`,
  non-zero process exit) so the executor returns an error with a nil or partial
  `TryResult`
- **THEN** the absence of `Evidence` SHALL NOT be an error, and the relay runner
  SHALL classify the failure from the log tail (or session log) instead

#### Scenario: Executor respects context cancellation

- **WHEN** the provided context is cancelled during execution
- **THEN** the executor SHALL terminate the agent subprocess and return a context
  cancellation error

### Requirement: ClaudeExecutor

The system SHALL provide a Claude adapter in `internal/harness/claude`, exposing
`claude.New(model string) harnessapi.Executor` over its concrete
`claude.Executor` type. The adapter SHALL invoke the `claude` CLI with
`-p <prompt> --output-format stream-json --verbose`, parse the NDJSON stream
inline, and return a `TryResult`. When a Claude model is specified, the executor
SHALL pass it with `--model`. When a resolved reasoning effort is specified for
Claude, the executor SHALL pass it with `--effort`. No exported
`agent.ClaudeExecutor` compatibility type SHALL remain.

#### Scenario: Claude run with model override

- **WHEN** a Claude model is specified in configuration
- **THEN** the executor SHALL pass `--model <model>` to the claude CLI

#### Scenario: Claude run with reasoning effort

- **WHEN** a Claude reasoning effort is resolved for the run
- **THEN** the executor SHALL pass `--effort <value>` to the claude CLI

#### Scenario: Claude stream-json parsing

- **WHEN** the claude subprocess completes
- **THEN** the executor SHALL parse each line as a `claudeStreamEvent` JSON object
  and extract the `result` field from events with `type: "result"`

### Requirement: CodexExecutor

The system SHALL provide a Codex adapter in `internal/harness/codex`, exposing
`codex.New(model string) harnessapi.Executor` over its concrete `codex.Executor`
type. The adapter SHALL invoke `codex exec` as a subprocess with appropriate
flags and return a `TryResult`. When a resolved reasoning effort is specified for
Codex, the executor SHALL inject it as a config override with
`-c model_reasoning_effort=<value>`, not as a nonexistent CLI reasoning flag. The
executor SHALL merge subprocess stderr into stdout via the standard Go
`cmd.StdoutPipe()` + `cmd.Stderr = cmd.Stdout` (before `cmd.Start()`) idiom —
this is the library-recommended merge pattern and is not a race. When the
subprocess exits non-zero with no in-band parser-matchable signal, the executor
SHALL enrich `FailureEvidence` from codex's own session log under
`$CODEX_HOME/sessions/` (default `~/.codex/sessions/`) when a matching session
exists. No exported `agent.CodexExecutor` compatibility type SHALL remain.

#### Scenario: Codex run with approval bypass mode

- **WHEN** a codex run is executed
- **THEN** the executor SHALL pass `--dangerously-bypass-approvals-and-sandbox`

#### Scenario: Codex run with reasoning effort

- **WHEN** a Codex reasoning effort is resolved for the run
- **THEN** the executor SHALL pass `-c model_reasoning_effort=<value>` to
  `codex exec`

#### Scenario: Codex structured output

- **WHEN** structured output is requested
- **THEN** the executor SHALL pass `--output-schema ./schema.json -o ./report.json`
  and parse the output file

#### Scenario: Codex stderr is merged into the parser buffer

- **WHEN** codex writes to its stderr file descriptor
- **THEN** the executor SHALL merge stderr into the same buffer passed to
  `ParseCodexError` via the standard `cmd.Stderr = cmd.Stdout` (post-`StdoutPipe`,
  pre-`Start()`) idiom
- **AND** no separate stderr-capture goroutine or `io.Pipe` SHALL be required for
  this change (the existing merge idiom, shared with `RunLoggedCommand`, is
  sufficient)

#### Scenario: Codex silent exit enriched from session log

- **WHEN** codex exits non-zero and the in-band stdout/stderr buffer contains no
  parser-matchable signal
- **AND** a `rollout-*.jsonl` file exists under `$CODEX_HOME/sessions/YYYY/MM/DD/`
  whose first-line `session_meta.cwd` matches the run's `WorkspaceDir` and whose
  `session_meta.timestamp` is within the try window
- **THEN** the executor SHALL populate `FailureEvidence` with
  `Source = "codex_session_log"`, `Message` derived from the last `event_msg`
  subtype, and a bounded `RawSignal` built from the `session_meta` line plus the
  last `event_msg` line
- **AND** it SHALL explicitly skip `token_count`, `response_item`, `turn_context`,
  and any payload field named `base_instructions` to avoid the verbosity hazard
- **AND** it SHALL NOT rely on `session_meta` for the resolved model name —
  `session_meta` carries only `model_provider` (e.g. `openai`); the resolved model
  name lives in `turn_context.payload.model`. The executor's own `model` local is
  the authoritative source for `TryResult.ResolvedModel`, not the session log

#### Scenario: Codex silent exit with no matching session log

- **WHEN** codex exits non-zero, the in-band buffer has no parser-matchable signal,
  and no session-log file matches the run's `WorkspaceDir` within the try window
- **THEN** the executor SHALL populate `FailureEvidence` with
  `Category = harness_launch`, `Source = "codex_no_session_log"`, and
  `Message = "codex launched but wrote no session log"`
- **AND** because the executor supplies typed Evidence with a Category,
  `ClassifyError` Priority 1 SHALL resolve the failure directly as
  `harness_launch`, yielding the existing `StrategyFreshRestart` + `FailureInfra`
  semantics (retry within budget with a fresh session; infra-class freeze pressure
  after 2+ failures caps a runner that repeatedly fails to launch)
- **AND** the intent is to label the failure correctly and surface the
  `codex_no_session_log` repro marker so the launch issue can be reproduced and
  fixed, NOT to skip retrying — the runner keeps retrying codex launch failures up
  to the budget

### Requirement: OpenCodeExecutor

The system SHALL provide an OpenCode adapter in `internal/harness/opencode`,
exposing `opencode.New(model string) harnessapi.Executor` over its concrete
`opencode.Executor` type. The adapter SHALL invoke the `opencode` CLI with
`run <prompt> --format json` and return a `TryResult`. The executor SHALL parse
opencode's newline-delimited JSON event stream using the live schema captured in
the rally-083 spike. When event parsing yields no usable final text, the executor
SHALL NOT emit the raw subprocess output as the result `Summary`. When a resolved
reasoning variant is specified for opencode, the executor SHALL pass it with
`--variant`. No exported `agent.OpenCodeExecutor` compatibility type SHALL remain.

#### Scenario: OpenCode JSON event parsing

- **WHEN** the opencode subprocess completes
- **THEN** the executor SHALL parse each line as an `opencodeJSONEvent` JSON object
- **AND** it SHALL concatenate assistant text from ordered events with top-level
  `type: "text"` and nested `part.text`
- **AND** it SHALL count tool usage from top-level `type: "tool_use"` or nested
  `part.type: "tool"`

#### Scenario: OpenCode run with reasoning variant

- **WHEN** an opencode reasoning variant is resolved for the run
- **THEN** the executor SHALL pass `--variant <value>` to the opencode CLI

#### Scenario: OpenCode clean completion

- **WHEN** the opencode subprocess exits with status 0, no top-level
  `type: "error"` event was seen, and the stream contains assistant text or a
  `type: "step_finish"` event
- **THEN** the executor SHALL treat the opencode run as cleanly completed for
  parser purposes

#### Scenario: OpenCode error event

- **WHEN** the opencode event stream contains a top-level `type: "error"` event
  with no `part`
- **THEN** the executor SHALL return `Completed=false`
- **AND** it SHALL build a short bounded summary from `error.data.message`,
  optional `error.data.ref`, and fallback `error.name`
- **AND** it SHALL NOT place the raw subprocess stdout into `Summary`

#### Scenario: Parse yields no text

- **WHEN** the opencode subprocess completes but no `text` parts are extracted from
  its output
- **THEN** the executor SHALL return a `TryResult` with `Completed=false` and a
  short, bounded `Summary` indicating no parseable result
- **AND** the executor SHALL NOT place the raw subprocess stdout into `Summary`

### Requirement: AntigravityExecutor

The system SHALL provide an Antigravity adapter in
`internal/harness/antigravity`, exposing
`antigravity.New(model string) harnessapi.Executor` over its concrete
`antigravity.Executor` type. The adapter SHALL invoke `agy --print <prompt>` as
a subprocess and return a `TryResult`.
No exported `agent.AntigravityExecutor` compatibility type SHALL remain, and the
default model constant SHALL move to `antigravity.DefaultModel`.

#### Scenario: Antigravity print-mode execution

- **WHEN** an Antigravity run is executed
- **THEN** the executor SHALL pass `--dangerously-skip-permissions`,
  `--print-timeout=<duration>`, and `--print <prompt>` to the `agy` CLI

#### Scenario: Antigravity model override

- **WHEN** an Antigravity model label is specified in configuration
- **THEN** the executor SHALL temporarily set that label in
  `~/.gemini/antigravity-cli/settings.json` for the duration of the run and
  restore the prior setting afterwards

#### Scenario: Antigravity conversation id capture

- **WHEN** the `agy` subprocess writes a print-mode conversation id to its log
- **THEN** the executor SHALL return that conversation id as the
  `TryResult.SessionID`

### Requirement: FixtureExecutor

The system SHALL provide a fixture adapter in `internal/harness/fixture`, exposing
`fixture.New(...) harnessapi.Executor` over its concrete `fixture.Executor` type.
The adapter SHALL replay precomputed git diffs and canned JSON outputs without
invoking any real agent CLI. No exported `agent.FixtureExecutor` compatibility
type SHALL remain.

#### Scenario: Fixture applies diff and returns canned result

- **WHEN** a fixture executor is invoked with a diff path and output path
- **THEN** it SHALL apply the diff via `git apply`, commit the changes, and return
  the TryResult parsed from the output JSON file

#### Scenario: Fixture supports configurable delay

- **WHEN** a delay duration is configured on the fixture executor
- **THEN** it SHALL sleep for that duration before returning, simulating agent
  execution time

#### Scenario: Fixture handles already-applied diffs

- **WHEN** the diff has already been applied (e.g., retry scenario)
- **THEN** the executor SHALL detect this via `git apply --reverse --check` and
  skip re-application

### Requirement: OpenCode try-budget exhaustion evidence

The system SHALL surface a bounded diagnostic signal from the opencode server log
when an opencode try times out without producing a parseable result, so try-budget
exhaustion is distinguishable from a real opencode crash in telemetry. This
requirement EXTENDS the existing opencode disk-log fallback machinery
(`attachOpenCodeFailureEvidence` / `openCodeServerLogFailureEvidence` /
`readOpenCodeServerLogTail` / `openCodeEvidenceFromServerLog`) in the relocated
OpenCode adapter files under `internal/harness/opencode` — it does not introduce
a parallel session-id correlation mechanism, since the existing locator already
correlates by opencode session id (from the `message=created id=…
directory=<WorkspaceDir>` line via `openCodeCreatedSessionID`) with a
`providerID=<provider>` + try-window fallback (`openCodeLogLineInWindow`). When
the opencode subprocess is killed by the runner-side try or run budget without
ever emitting a usable `--format json` result, the executor SHALL additionally
keep `level=WARN` and `level=ERROR` lines plus the structural `message=created` /
`message="loop session.id=…"` / `message=stream` markers from the existing
server-log locator (currently `~/.local/share/opencode/log/opencode.log` via
`defaultOpenCodeServerLogPath`), bounded to at most sixteen lines, alongside the
existing usage-limit extraction path. The resulting
`FailureEvidence` SHALL set `Source = "opencode_disk_log"`, `Message` from the
last error line (or `"try budget exhausted; no parseable output"` when no error
line is present), and `RawSignal` from the bounded filtered tail. The executor
SHALL explicitly skip per-token and per-permission log lines, which are the
verbosity hazard in the opencode log.

#### Scenario: Budget-exhausted opencode try carries disk-log tail

- **WHEN** an opencode try is killed by the runner-side try or run budget without
  producing a parseable `--format json` result
- **AND** the opencode server log contains WARN or ERROR lines for the try's
  session id
- **THEN** the executor SHALL populate `FailureEvidence` with
  `Source = "opencode_disk_log"` and a bounded `RawSignal` containing the
  WARN/ERROR lines and structural markers
- **AND** telemetry SHALL distinguish the failure from a real opencode crash via
  the `failure_evidence.source` value

#### Scenario: Budget-exhausted opencode try without log signal

- **WHEN** an opencode try is killed by the runner-side try or run budget without
  producing a parseable result
- **AND** the opencode server log contains no WARN/ERROR lines for the try's
  session id (opencode made progress but never finished)
- **THEN** the executor SHALL populate `FailureEvidence` with
  `Source = "opencode_disk_log"`,
  `Message = "try budget exhausted; no parseable output"`, and a `RawSignal`
  built from the structural `loop`/`stream` markers alone

#### Scenario: Verbose log lines are not surfaced

- **WHEN** the opencode server log contains per-token, per-tool-call, or
  per-permission log lines alongside the WARN/ERROR and structural markers
- **THEN** the executor SHALL exclude them from the bounded `RawSignal`
- **AND** the resulting evidence SHALL NOT exceed the standard 256-rune signal
  bound
