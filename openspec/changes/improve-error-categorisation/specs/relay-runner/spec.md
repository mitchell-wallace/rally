## MODIFIED Requirements

### Requirement: Failure detection
The system SHALL consider a try failed if the agent reports `Completed: false`, exits with an error, or produces no meaningful work (no file changes and runs less than 3 minutes). The system SHALL assign each failure a stable `FailureCategory` (see "Failure taxonomy and evidence") and SHALL map that category onto one of three resilience classes:
- **infra-class**: short rate limit, provider overload, harness/launch error (e.g. `argument list too long`, `fork/exec`), transient infrastructure error (`transient_infra`: API timeout, network/connection/TLS failure, non-overload 5xx), or liveness stall detection.
- **agent-class**: ordinary agent error or short no-op.
- **incomplete**: file changes were produced but the agent did not finalize the lap (`laps done` or `laps handoff`).

Long usage/quota exhaustion (`usage_limit`), invalid-model/config (`invalid_model`), and authentication/proxy (`auth_or_proxy`) failures SHALL NOT be classified infra-class; they are handled by benching/routing (see "Usage-limit benching and reset recovery") and SHALL NOT increment the pause/freeze counter.

Only repeated infra-class failures SHALL drive the per-agent-type resilience cascade; a single infra-class failure retries without escalation. Agent-class and incomplete failures SHALL fail the try and be retry-eligible but SHALL NOT increment the pause/freeze counter. Rate-limit flags SHALL be tracked per harness-model pair (`harness:model`) using a `ResilienceKey` type, not per harness alone, so that an opencode runner using multiple providers does not freeze wholesale when only one provider hits its rate limit. All resilience methods (`getState`, `PauseAgent`, `RecordHourlyFailure`, `FreezeAgent`, `UnpauseAgent`, `SelectActiveAgent`) and their callers in `runner.go` and `route_runtime.go` SHALL use the `ResilienceKey`.

#### Scenario: Short no-op try detected as failure
- **WHEN** a try produces no file changes and completes in under 3 minutes
- **THEN** the system SHALL treat it as a failed, retry-eligible try, classified agent-class, and SHALL NOT count it toward pause/freeze

#### Scenario: Agent error exit detected as failure
- **WHEN** the agent subprocess exits with a non-zero exit code matching an agent-class pattern
- **THEN** the system SHALL treat it as a failed, retry-eligible try and SHALL NOT count it toward pause/freeze

#### Scenario: Single infra failure does not pause
- **WHEN** a run has exactly one attempt classified as infra-class and the remaining attempts (if any) are agent-class or incomplete
- **THEN** the system SHALL NOT call `PauseAgent` and SHALL NOT increment the freeze counter

#### Scenario: Repeated infra failures drive the cascade
- **WHEN** >1 attempt within a run is classified as infra-class
- **THEN** the system SHALL call `PauseAgent` and count it toward the resilience cascade

#### Scenario: Incomplete try does not escalate
- **WHEN** a try produces file changes but the agent did not finalize
- **THEN** the system SHALL classify it as incomplete, suppress auto-commit, retry the run, and SHALL NOT count it toward pause/freeze

#### Scenario: Usage-limit failure is not infra-class
- **WHEN** a try fails with a `usage_limit`, `invalid_model`, or `auth_or_proxy` category
- **THEN** the system SHALL NOT classify it infra-class and SHALL NOT increment the pause/freeze counter

### Requirement: Incomplete failure class
The system SHALL classify a try as "incomplete" rather than "failed" when file changes produced **by that try** were left uncommitted (dirty working tree) and the agent neither finalized the lap (`laps done`) nor handed off (`laps handoff`). To attribute changes to the try, the system SHALL snapshot the set of already-dirty paths at try start and SHALL consider only the working-tree delta against that snapshot. Uncommitted leftovers inherited from a prior failed try and left untouched SHALL NOT, on their own, make a later try incomplete.

Stronger evidence SHALL take precedence over incomplete classification: when structured executor evidence or a provider/configuration/quota signal (`usage_limit`, `invalid_model`, `auth_or_proxy`, `provider_overloaded`, `short_rate_limit`) is present, the system SHALL classify by that signal even if the working tree is dirty, so a dirty file cannot mask a stronger failure.

Paths that are not task work SHALL be excluded from the meaningful-change determination through a single shared exclusion helper covering Rally's own metadata (`.rally/`, `.laps/`) and known harness-local transient state (starting with `.claude/settings.local.json`). A try whose only dirty paths are excluded SHALL be treated as having produced no meaningful task changes.

An incomplete try SHALL have its auto-commit suppressed, leaving changes uncommitted. The retry run SHALL inherit the uncommitted changes and SHALL receive prompt guidance: "The last run was incomplete. Check any current git changes, finish anything not done, verify correctness, commit when good, then run `laps done`." An incomplete try SHALL be retried but SHALL NOT count toward the pause/freeze resilience cascade.

#### Scenario: Agent produces file changes without finalizing
- **WHEN** a try produces file changes in the working tree but the agent does not call `laps done` or `laps handoff`
- **THEN** the system SHALL classify the try as incomplete, suppress auto-commit, retry the run with prompt guidance, and SHALL NOT call `PauseAgent` or `RecordHourlyFailure`

#### Scenario: No file changes and no finalization
- **WHEN** a try produces no file changes and the agent does not finalize
- **THEN** the system SHALL classify as a normal agent-class failure (retry-eligible, does not escalate)

#### Scenario: Inherited leftover changes do not trigger incomplete
- **WHEN** a try begins with a dirty working tree inherited from a prior failed try, and the try produces no new changes of its own and does not finalize
- **THEN** the system SHALL NOT classify the try as incomplete on the basis of those untouched leftovers

#### Scenario: Touching an inherited leftover attributes it to this try
- **WHEN** a try modifies, reverts, or commits a path that was already dirty at try start
- **THEN** that path SHALL count as a change produced by this try when classifying incompleteness

#### Scenario: Provider error beats dirty harness-local state
- **WHEN** a try logs a provider/quota/config signal (e.g. `RESOURCE_EXHAUSTED`, `model_not_found`, `Failed to authenticate`) and the only dirty path is excluded harness-local state (e.g. `.claude/settings.local.json`)
- **THEN** the system SHALL classify by the provider/quota/config signal and SHALL NOT classify the try as incomplete

#### Scenario: Harness-local-only dirt is not meaningful change
- **WHEN** a try's only dirty paths are covered by the shared exclusion helper
- **THEN** the system SHALL treat the try as having produced no meaningful task changes for incomplete classification

## ADDED Requirements

### Requirement: Failure taxonomy and evidence
The system SHALL classify each failure into one stable `FailureCategory`: `usage_limit`, `short_rate_limit`, `provider_overloaded`, `transient_infra`, `invalid_model`, `auth_or_proxy`, `harness_launch`, `incomplete_finalization`, or `agent_error`. (`transient_infra` covers API timeout, network/connection/TLS failure, and non-overload 5xx; the liveness stall kill is not a log-text category and continues to set an infra class directly via the stall path.) Each category SHALL have a short, human-readable display label distinct from the machine-readable category, and SHALL map to exactly one resilience `FailureClass` per the Category→FailureClass mapping (`usage_limit`, `invalid_model`, and `auth_or_proxy` map to neither infra nor the freeze counter). Classification SHALL prefer, in order: (1) typed `FailureEvidence` supplied by the executor; (2) provider/configuration/quota evidence from structured data or bounded error snippets; (3) meaningful task-file change (`incomplete_finalization`); (4) harness-scoped text patterns; (5) `agent_error` as the default. The system SHALL tolerate absent evidence and fall back to log-tail classification.

#### Scenario: Structured evidence wins over text patterns
- **WHEN** the executor supplies `FailureEvidence` with a category
- **THEN** the system SHALL use that category rather than re-deriving one from log text

#### Scenario: Unknown failure defaults to agent_error
- **WHEN** no evidence, provider/config/quota signal, meaningful change, or harness-scoped pattern matches
- **THEN** the system SHALL classify the failure as `agent_error`

#### Scenario: Each category maps to one resilience class
- **WHEN** a category is assigned
- **THEN** the system SHALL derive its resilience `FailureClass` from a single mapping in which `usage_limit`/`invalid_model`/`auth_or_proxy` are not infra-class

### Requirement: Harness-scoped classification
The system SHALL scope failure classification to the harness that produced the failure, so that a pattern naming or implying one harness cannot match a failure from a different harness. Display labels for generic failures SHALL NOT name a harness unless the failing harness matches that harness and the label is intentionally harness-specific.

#### Scenario: Codex prose does not match a Claude pattern
- **WHEN** a Codex try's log tail incidentally contains the text "rate-limit" or "Claude"
- **THEN** the system SHALL NOT classify or label the failure as a Claude rate limit

#### Scenario: Harness name omitted from generic label
- **WHEN** a generic failure (e.g. a usage limit) is displayed for a non-Claude harness
- **THEN** the display label SHALL NOT name a different harness

### Requirement: Terminal failure categories short-circuit the attempt loop
The system SHALL terminate a run's per-try attempt loop on the first detection of a `usage_limit` or `auth_or_proxy` failure, so the run makes exactly one attempt against an exhausted quota or failed auth rather than consuming its remaining retry budget. The resolved category and any reset evidence SHALL be surfaced from the run to the routing layer so a bench or route-away decision can be made.

#### Scenario: Usage limit makes one attempt
- **WHEN** a try fails with `usage_limit`
- **THEN** the run SHALL NOT make further attempts against the same runner and SHALL surface the category and reset evidence to the routing layer

#### Scenario: Auth failure routes away without looping
- **WHEN** a try fails with `auth_or_proxy`
- **THEN** the run SHALL make exactly one attempt and route away

### Requirement: Usage-limit benching and reset recovery
On a `usage_limit` failure the system SHALL bench every route entry sharing the failed runner's quota scope until the limit resets, instead of waiting a short fixed cooldown and retrying. When reset timing is parseable the bench SHALL last until that reset; otherwise a long conservative default SHALL be used and the runner routed away. A benched-but-active entry SHALL NOT be returned to rotation before its bench deadline by the resilience recovery sync. This guard SHALL apply only to the resilience-active recovery path; it SHALL NOT block the probation one-shot unbench, which is governed by the probation cycle, not by the bench deadline. When every entry in a lane is benched with a future reset, the system SHALL wait until the earliest reset rather than ending the relay as "all agents frozen". The reset deadline SHALL be persisted (with its quota scope) so it survives across relays on the same machine, and on the first selection after a persisted deadline passes the scope SHALL be re-probed once.

#### Scenario: Usage limit benches the quota scope until reset
- **WHEN** a try fails with `usage_limit` carrying a parsed reset window
- **THEN** the system SHALL bench all entries sharing the quota scope until that reset and route away, rather than looping a short cooldown

#### Scenario: Bench is not undone by recovery sync
- **WHEN** the resilience recovery sync runs while a benched entry's reset deadline is still in the future and the entry's resilience state is active
- **THEN** the system SHALL NOT return the entry to rotation

#### Scenario: All-benched lane waits for reset
- **WHEN** every entry in a lane is benched with a future reset deadline
- **THEN** the system SHALL wait until the earliest reset rather than failing the relay as "all agents frozen"
- **NOTE**: this differs from "All agents frozen ends the current pass" (Error resilience cascade) — a frozen lane with no pending reset still ends the pass; only a future bench deadline produces a wait

#### Scenario: Reset persists across relays and re-probes once
- **WHEN** a relay starts and a persisted reset deadline for a quota scope has not yet passed
- **THEN** the scope SHALL remain benched; and once the deadline passes the scope SHALL be re-probed a single time before normal selection resumes

#### Scenario: Re-probe still exhausted re-benches a fresh window
- **WHEN** the single post-deadline re-probe again returns `usage_limit`
- **THEN** the scope SHALL be re-benched for a fresh window (parsed reset or default) rather than treated as permanently exhausted

### Requirement: Harness-aware quota scope
The system SHALL resolve a quota scope key from a runner's harness and model via a single resolver, so benching applies to all route entries sharing an account/quota bucket. The resolver SHALL key antigravity per model family (case-insensitive substring over the free-form display label: `claude` / `flash` / `pro`), opencode per provider (the segment before the first `/` in the model id), and direct harnesses (`claude`, `codex`, `gemini`) per harness with the model ignored.

#### Scenario: Antigravity families are distinct scopes
- **WHEN** quota scopes are resolved for antigravity Claude and antigravity Gemini-flash models
- **THEN** they SHALL resolve to distinct scopes, so exhausting one does not bench the other

#### Scenario: Opencode keys on provider
- **WHEN** a quota scope is resolved for an opencode model `provider/model`
- **THEN** the scope SHALL be keyed on the provider segment before the first `/`

#### Scenario: Direct harness keys per harness
- **WHEN** a quota scope is resolved for a direct harness (claude/codex/gemini)
- **THEN** the scope SHALL be keyed on the harness and SHALL NOT be mis-split by any `/` in the model string

### Requirement: Categorised failure display and records
The system SHALL display the accurate failure category in the run footer and the collapsed retry line, including reset or wait detail when known (e.g. `usage limit, resets in 123h50m`, `rate limit, waiting 2m`, `invalid model`). Persisted try records and `summary.jsonl` SHALL store the stable category separately from the human-readable display reason, so machine consumers do not parse the display string.

#### Scenario: Usage limit shows reset detail
- **WHEN** a `usage_limit` failure with a parsed reset is displayed
- **THEN** the footer/retry line SHALL include the reset detail (e.g. `usage limit, resets in 123h50m`)

#### Scenario: Records carry stable category
- **WHEN** a failed try is persisted
- **THEN** the record SHALL include both the stable `FailureCategory` and the human-readable display reason

### Requirement: Resume finalization guidance
When retrying or resuming after an `incomplete_finalization` failure (including operator pause/resume of a laps-backed run), the resumed prompt SHALL include explicit finalization instructions directing the agent to finish the lap and call `laps done` (or `laps handoff` if blocked).

#### Scenario: Incomplete retry carries finalization guidance
- **WHEN** a run is retried after an `incomplete_finalization` failure
- **THEN** the resumed prompt SHALL include explicit guidance to finish and call `laps done`/`laps handoff`
