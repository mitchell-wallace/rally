## MODIFIED Requirements

### Requirement: Executor interface
The system SHALL define an `Executor` interface with a single method `Execute(ctx context.Context, opts RunOptions) (*TryResult, error)` that abstracts agent lifecycle. Each call to Execute represents one try (one agent CLI invocation). The `TryResult` SHALL carry an optional `FailureEvidence` value that an executor MAY populate when it can extract structured failure information (category, provider, quota scope, status code, and reset/retry timing) from the agent's output, and SHALL carry a `ResolvedModel` string reflecting the model actually passed to the underlying CLI on this try (the executor's resolved default when the route did not supply an explicit model). Both fields are optional in the sense that consumers SHALL tolerate empty values: when an executor cannot supply `Evidence` — including process-level failures where the executor returns an error with a nil or partial `TryResult` — consumers SHALL fall back to runner-side classification; when `ResolvedModel` is empty, the runner SHALL fall back to the route-supplied model. The executor SHALL always attempt to populate `ResolvedModel` from the same value it passes to the CLI's `--model` (or equivalent) flag, so telemetry reports the model the agent actually used rather than the route-resolved model whenever the two differ.

#### Scenario: Executor returns structured result
- **WHEN** an Executor implementation completes a try
- **THEN** it SHALL return a `TryResult` containing: `Completed` (bool), `Summary` (string), `RemainingWork` (string), `MessageAddressed` (*bool), `FilesChanged` ([]string), an optional `Evidence` (*FailureEvidence), and a `ResolvedModel` (string)
- **NOTE**: `CommitHash` is NOT part of `TryResult` — it is determined by the relay runner after the executor returns, by comparing HEAD before and after the try.

#### Scenario: Executor populates failure evidence when available
- **WHEN** an executor parses a structured provider/quota/config error from the agent's output (e.g. a usage-limit event with a reset window)
- **THEN** it SHALL set `TryResult.Evidence` with the corresponding category and any parsed reset/retry timing and quota scope

#### Scenario: Executor reports the resolved model
- **WHEN** an executor invokes the agent CLI with a `--model` (or equivalent) flag
- **THEN** `TryResult.ResolvedModel` SHALL be set to the same value the flag received, including the executor's own default when the route did not supply a model
- **AND** the runner SHALL use `ResolvedModel` in the `runner` telemetry tag whenever it is non-empty

#### Scenario: Evidence absent for process-level failure
- **WHEN** a try fails before producing a usable `TryResult` (e.g. `fork/exec`, non-zero process exit) so the executor returns an error with a nil or partial `TryResult`
- **THEN** the absence of `Evidence` SHALL NOT be an error, and the relay runner SHALL classify the failure from the log tail (or session log) instead

#### Scenario: Executor respects context cancellation
- **WHEN** the provided context is cancelled during execution
- **THEN** the executor SHALL terminate the agent subprocess and return a context cancellation error

## REMOVED Requirements

### Requirement: GeminiExecutor
**Reason**: The standalone `gemini` CLI has been deprecated upstream. Its own auth error tells operators to migrate (`IneligibleTierError: This client is no longer supported for Gemini Code Assist for individuals. To continue using Gemini, please migrate to the Antigravity suite of products`). Antigravity already serves the Gemini model family on the same provider account, so removing the gemini harness does not remove any capability operators need.
**Migration**: Operators with `[harness.ge.models]`, `gemini_model`, or `routes x = ["ge:pro"]` / `["gemini:..."]` blocks SHALL receive a one-time warning on the first relay after upgrade pointing them at `antigravity`. The `ge`/`gemini` aliases SHALL fail to resolve. The Gemini model family is reachable via Antigravity model labels (`Gemini 3.5 Flash (High)`, `Gemini 3.1 Pro (High)`, etc.) configured under `[harness.ag.models]`.

## MODIFIED Requirements

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

## ADDED Requirements

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
