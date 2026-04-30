## ADDED Requirements

### Requirement: Cheap in-place rotation for same-harness advances
The system SHALL extend the executor adapter interface with `RotateSupported() bool` and `RotateModel(newModel string) error`. When the scheduler advances from entry A to entry B and `A.harness == B.harness` AND the adapter declares `RotateSupported() == true`, the relay-runner SHALL call `RotateModel(B.model)` on the existing adapter instance instead of tearing down and respawning the harness process. Cross-harness advances SHALL continue to use the v0.6.0 teardown/respawn path.

#### Scenario: Same-harness advance uses RotateModel
- **WHEN** the scheduler advances from `opencode:zai-coding-plan/glm-5.1` to `opencode:opencode-go/kimi-k2.6` and the opencode adapter declares `RotateSupported() == true`
- **THEN** the relay-runner SHALL call `RotateModel("opencode-go/kimi-k2.6")` on the existing adapter instance and proceed with the next iteration on the same process

#### Scenario: Cross-harness advance uses teardown
- **WHEN** the scheduler advances from `opencode:...` to `claude:opus-4.7`
- **THEN** the relay-runner SHALL tear down the opencode adapter and instantiate a fresh claude adapter for the next iteration

#### Scenario: Adapter does not declare rotation support
- **WHEN** the scheduler advances same-harness but the adapter returns `RotateSupported() == false`
- **THEN** the relay-runner SHALL fall back to teardown/respawn for that advance

#### Scenario: RotateModel error
- **WHEN** `RotateModel` returns a non-nil error
- **THEN** the relay-runner SHALL fall back to teardown/respawn and SHALL log the rotation error for diagnostics

### Requirement: Provider rotation in no-backend mode
The system SHALL apply same-harness cheap rotation to the `default` route in no-backend mode just as it does for any other route. The rotation path SHALL NOT depend on whether a bead's `assignee` matched a non-default route.

#### Scenario: Cheap rotation on default route
- **WHEN** rally is in no-backend mode and `[routes].default = ["op:z", "op:gk"]` (both opencode shortcuts)
- **THEN** the advance from `op:z` to `op:gk` SHALL use `RotateModel` if the opencode adapter supports it
