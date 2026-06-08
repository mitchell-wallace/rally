## MODIFIED Requirements

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
