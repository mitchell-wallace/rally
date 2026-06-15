## ADDED Requirements

### Requirement: Bounded handoff-only resume
When a run's wall-clock budget across retries is exhausted (see relay-runner "Run/try timeout and bounded handoff recovery"), the system SHALL reuse the honest session-resume mechanism to attempt one bounded, handoff-only continuation of the same session, subject to the same resume preconditions: the harness MUST report `ResumeSupported()` as true and a session ID MUST have been captured. The handoff-only resume SHALL pass the harness's resume flag (as for any tracked-session resume) together with a handoff-only prompt, and SHALL be bounded by a separate hard limit (`handoff_timeout_secs`, default 300 seconds) that is not counted against the run budget and never exceeds the per-try cap.

In the handoff-only phase the agent SHALL NOT continue implementation; the phase exists only to capture a clean handoff. The silence-based stall detector SHALL NOT be applied to this short phase — only the handoff limit bounds it. When the harness does not support resume or no session ID exists, the system SHALL skip the bounded handoff-only resume and proceed directly to the `handoff_timeout` outcome.

#### Scenario: Budget exhaustion triggers a handoff-only resume when supported
- **WHEN** a run's retry budget across attempts is exhausted, the harness supports resume, and a session ID exists
- **THEN** the system SHALL resume the session with the harness's resume flag and a handoff-only prompt, bounded by the handoff limit

#### Scenario: Handoff-only phase forbids implementation
- **WHEN** the bounded handoff-only resume runs
- **THEN** the prompt SHALL direct the agent to summarize the blocker and call `laps handoff` + `laps wrapup`, and SHALL NOT direct it to continue implementation

#### Scenario: No resume support skips the handoff-only resume
- **WHEN** the run budget is exhausted but the harness does not support resume or no session ID was captured
- **THEN** the system SHALL NOT attempt a handoff-only resume and SHALL proceed to the `handoff_timeout` outcome

### Requirement: RECOVERY role default boundary
The default RECOVERY role SHALL be a recovery-and-continuation role, distinct from both SENIOR and VERIFY: reasoning-heavy like VERIFY (it reasons carefully about the prior state, evidence, plan validity, and risk) but with the authority and coding ability of an implementation role (it modifies code, reconciles dirty leftover state, and continues the task when appropriate). It SHALL default to a senior-class model and SHALL NOT simply reuse the SENIOR role document.

The RECOVERY role SHALL first classify the prior incomplete/handed-off state into exactly one of `continue`, `discard`, `course_correct`, `repair_plan`, or `needs_user`, and SHALL then act on that classification rather than stopping at a diagnosis — except when the correct classification is `needs_user`, the reluctant escape hatch reserved for risky scope/product/destructive decisions outside the lap's authority. The RECOVERY role MAY add follow-up laps when that reduces risk or creates a cleaner work split, but SHALL still leave the working tree coherent (or hand off a coherent next slice) rather than using follow-up laps to dodge recovery. The generic RECOVERY role document SHALL remain OpenSpec-agnostic, consistent with the other default role documents.

#### Scenario: RECOVERY classifies then acts
- **WHEN** a RECOVERY run inspects the prior dirty/handed-off state
- **THEN** it SHALL record one of the five classifications and SHALL act on it, not stop at a diagnosis, unless the classification is `needs_user`

#### Scenario: needs_user defers a risky decision
- **WHEN** the cleanest path requires a risky scope/product/destructive decision outside the lap's authority
- **THEN** the RECOVERY role SHALL classify `needs_user` and defer rather than act autonomously

#### Scenario: RECOVERY may add follow-up laps without dodging recovery
- **WHEN** a RECOVERY run decides a cleaner work split reduces risk
- **THEN** it MAY add follow-up laps but SHALL still leave the tree coherent or hand off a coherent next slice

#### Scenario: RECOVERY default model is senior-class and distinct from SENIOR
- **WHEN** the RECOVERY route is used with default configuration
- **THEN** it SHALL prefer a senior-class model and SHALL use the RECOVERY role document, not the SENIOR one
