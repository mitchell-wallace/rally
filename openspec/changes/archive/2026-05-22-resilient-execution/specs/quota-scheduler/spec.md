## ADDED Requirements

### Requirement: Cheap-rotation hint on same-harness advance
The system SHALL allow the quota-scheduler to expose the previous entry alongside the next entry on each `Next()` call, so the relay-runner can detect same-harness advances and route them to the cheap-rotation path. The scheduler's behaviour itself (selection logic, quota counters, exhausted/frozen flags) SHALL be unchanged from v0.6.0; this requirement only formalises the previous-entry plumbing.

#### Scenario: Scheduler exposes previous entry
- **WHEN** the relay-runner calls `Next()` on the scheduler
- **THEN** the returned record SHALL include both `prev` (the entry just used) and `current` (the entry to use next), so callers can compare `prev.harness == current.harness` for cheap-rotation routing

### Requirement: Freeze-detection feeds scheduler hooks
The system SHALL accept `OnAgentFailed(entry, reason)` calls with `reason = "freeze"` from the v0.7.0 freeze detector and SHALL mark the entry frozen for the current cycle exactly as it does for `reason = "retry-budget-exhausted"` or `reason = "rate-limit"`. The scheduler SHALL NOT distinguish between failure reasons in its selection logic — the reason is metadata for telemetry and operator UI only.

#### Scenario: Freeze marks entry frozen
- **WHEN** the freeze detector emits `OnAgentFailed(entry, "freeze")`
- **THEN** the scheduler SHALL mark the entry frozen for the current cycle and SHALL skip it in subsequent selections until cycle wrap or recovery

### Requirement: `OnAgentRecovered` clears detector-driven freezes
The system SHALL accept `OnAgentRecovered(entry)` calls. When the entry was previously marked frozen via detector signal (freeze, rate-limit), the flag SHALL be cleared. When the entry was marked exhausted via retry-budget exhaustion, recovery SHALL NOT auto-clear (operator must restart or wait for cycle wrap to retry).

#### Scenario: Detector-driven freeze recovers
- **WHEN** an entry was marked frozen via `OnAgentFailed(entry, "freeze")` and the detector later observes recovery (e.g. resume retry succeeds)
- **THEN** `OnAgentRecovered(entry)` SHALL clear the frozen flag for that entry

#### Scenario: Budget exhaustion does not auto-clear
- **WHEN** an entry was marked exhausted via `OnAgentFailed(entry, "retry-budget-exhausted")` and `OnAgentRecovered(entry)` is invoked
- **THEN** the exhausted flag SHALL NOT be cleared by this call; the entry recovers only on cycle wrap (per v0.6.0 behaviour)
