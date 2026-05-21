## ADDED Requirements

### Requirement: Adapter capability declarations
The system SHALL extend the executor adapter interface with capability-declaration methods that the relay-runner inspects to choose the right invocation path:

- `ResumeSupported() bool` — whether the adapter accepts resume parameters on retry
- `RotateSupported() bool` — whether the adapter accepts in-place model rotation
- `LivenessProbeSupported() bool` — whether the adapter tolerates a side-channel probe prompt
- `CharsPerToken() float64` — the per-harness divisor for the v0.3.0 token estimator (`0` means "cannot estimate")

Existing adapters SHALL return safe defaults (`false` / `0`); per-harness implementations land per-adapter and may be enabled incrementally.

#### Scenario: Adapter returns capability defaults
- **WHEN** an executor adapter has not yet implemented resume / rotation / probe support
- **THEN** the corresponding capability methods SHALL return `false` and the relay-runner SHALL fall back to the v0.6.0 baseline path

#### Scenario: Capability methods inspected at runtime
- **WHEN** the relay-runner is about to invoke a retry, advance, or freeze-probe
- **THEN** it SHALL call the appropriate capability method on the active adapter and route accordingly

### Requirement: Session-id surfaced from `Execute`
The system SHALL extend `TryResult` with a `SessionID string` field. Adapters that support resume SHALL populate this field with the harness-specific session identifier captured during the try. Adapters that don't support resume SHALL leave it empty.

#### Scenario: Resume-capable adapter populates SessionID
- **WHEN** a resume-capable adapter completes a try (successfully or unsuccessfully)
- **THEN** `TryResult.SessionID` SHALL contain the harness-specific session identifier

#### Scenario: Non-resume adapter leaves SessionID empty
- **WHEN** an adapter without resume support completes a try
- **THEN** `TryResult.SessionID` SHALL be the empty string

### Requirement: `RotateModel` and `ProbeLiveness` adapter methods
The system SHALL extend the executor adapter interface with `RotateModel(newModel string) error` and `ProbeLiveness(ctx context.Context) (bool, error)` methods. Adapters declaring `RotateSupported() == false` SHALL implement `RotateModel` as a no-op returning a not-supported error. Adapters declaring `LivenessProbeSupported() == false` SHALL implement `ProbeLiveness` as a no-op returning `(false, error)`.

#### Scenario: RotateModel on supported adapter
- **WHEN** the relay-runner calls `RotateModel("new-model")` on an adapter that supports rotation
- **THEN** the adapter SHALL update its internal model state and SHALL return `nil`; subsequent `Execute` calls SHALL use the new model

#### Scenario: ProbeLiveness on supported adapter
- **WHEN** the relay-runner calls `ProbeLiveness(ctx)` on an adapter that supports probing
- **THEN** the adapter SHALL send a side-channel prompt, await an OK response within a bounded timeout, and return `(true, nil)` on success or `(false, err)` on timeout/error
