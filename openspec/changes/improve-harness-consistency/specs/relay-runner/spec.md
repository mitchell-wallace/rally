## MODIFIED Requirements

### Requirement: Failure taxonomy and evidence
The system SHALL classify each failure into one stable `FailureCategory`: `usage_limit`, `short_rate_limit`, `provider_overloaded`, `transient_infra`, `invalid_model`, `auth_or_proxy`, `harness_launch`, `incomplete_finalization`, or `agent_error`. (`transient_infra` covers API timeout, network/connection/TLS failure, non-overload 5xx, and runner-side try-budget exhaustion with no executor signal; the liveness stall kill is not a log-text category and continues to set an infra class directly via the stall path.) Each category SHALL have a short, human-readable display label distinct from the machine-readable category, and SHALL map to exactly one resilience `FailureClass` per the Category→FailureClass mapping (`usage_limit`, `invalid_model`, and `auth_or_proxy` map to neither infra nor the freeze counter). Classification SHALL prefer, in order: (1) typed `FailureEvidence` supplied by the executor; (2) provider/configuration/quota evidence from structured data or bounded error snippets; (3) meaningful task-file change (`incomplete_finalization`); (4) harness-scoped text patterns; (5) `agent_error` as the default. The system SHALL tolerate absent evidence and fall back to log-tail classification. The freeze-cascade counter SHALL be driven solely by the mapped infra `FailureClass`, making the Category→FailureClass mapping the single source of truth for freeze accounting; no separate per-`Category` check SHALL govern the freeze increment at its call site.

Every classification path SHALL attach a populated `FailureEvidence` to the returned `StrategyDecision` so the structured `failure_evidence` context block on the emitted `RallyFailure` event is populated for 100% of categorised failures, not only the executor-evidence path. The decision's `FailureEvidence.Source` SHALL identify which path produced it: `executor_evidence` (Priority 1, when the executor supplied Evidence without its own sub-source label), `codex_session_log` / `codex_no_session_log` / `opencode_disk_log` (Priority 1, when the executor's Evidence was derived from a session or disk-log fallback — these sub-source labels ride on the Evidence's `Source` field and take precedence over the generic `executor_evidence` label), `dirty_tree` (Priority 3), `text_pattern` (Priority 4), or `unmatched` (Priority 5). The `RawSignal` SHALL be bounded (256 runes) and `Message` SHALL be a short human-readable label appropriate to the source.

#### Scenario: Structured evidence wins over text patterns
- **WHEN** the executor supplies `FailureEvidence` with a category
- **THEN** the system SHALL use that category rather than re-deriving one from log text
- **AND** the decision's `Evidence.Source` SHALL be `executor_evidence`

#### Scenario: Unknown failure defaults to agent_error
- **WHEN** no evidence, provider/config/quota signal, meaningful change, or harness-scoped pattern matches
- **THEN** the system SHALL classify the failure as `agent_error`
- **AND** the decision's `Evidence.Source` SHALL be `unmatched`
- **AND** `Evidence.RawSignal` SHALL carry a bounded tail of the log lines that were examined

#### Scenario: Each category maps to one resilience class
- **WHEN** a category is assigned
- **THEN** the system SHALL derive its resilience `FailureClass` from a single mapping in which `usage_limit`/`invalid_model`/`auth_or_proxy` are not infra-class

#### Scenario: Dirty-tree classification carries changed-paths signal
- **WHEN** a failure is classified `incomplete_finalization` because the working tree has meaningful uncommitted changes and the agent did not finalize
- **THEN** the decision's `Evidence.Source` SHALL be `dirty_tree`
- **AND** `Evidence.RawSignal` SHALL carry the bounded changed-paths list so the failure event is self-contained without re-reading git state

#### Scenario: Text-pattern classification carries the matching line
- **WHEN** a failure is classified via a harness-scoped text pattern
- **THEN** the decision's `Evidence.Source` SHALL be `text_pattern`
- **AND** `Evidence.Message` SHALL be the pattern name
- **AND** `Evidence.RawSignal` SHALL carry the matching log line(s) that triggered the pattern

### Requirement: Harness-scoped classification
The system SHALL scope failure classification to the harness that produced the failure, so that a pattern naming or implying one harness cannot match a failure from a different harness. Display labels for generic failures SHALL NOT name a harness unless the failing harness matches that harness and the label is intentionally harness-specific. With the gemini harness removed, the harness-scoped patterns targeting gemini (`gemini-cli exit 1`, `gemini auth or unsupported client`) SHALL NOT exist; the antigravity-scoped eligibility pattern SHALL continue to apply only to antigravity.

#### Scenario: Codex prose does not match a Claude pattern
- **WHEN** a Codex try's log tail incidentally contains the text "rate-limit" or "Claude"
- **THEN** the system SHALL NOT classify or label the failure as a Claude rate limit

#### Scenario: Harness name omitted from generic label
- **WHEN** a generic failure (e.g. a usage limit) is displayed for a non-Claude harness
- **THEN** the display label SHALL NOT name a different harness

#### Scenario: Removed gemini-only patterns do not match
- **WHEN** a failure's log tail is consulted against the harness-scoped pattern table
- **THEN** no pattern scoped to the removed `gemini` harness SHALL exist (the `gemini auth or unsupported client` pattern with `Harness: "gemini"` is removed)
- **AND** the antigravity-scoped `antigravity gemini-cli exit 1` pattern (which matches the literal string `gemini-cli` because antigravity shells out to that binary) SHALL still exist and SHALL continue to classify antigravity's exit-1-with-no-other-signal cases as `agent_error`

## ADDED Requirements

### Requirement: Runner-side try-budget exhaustion classification
The system SHALL classify a runner-killed try distinctly from a real harness crash when the per-try-cap deadline fires without producing any executor or post-hoc evidence. The runner already distinguishes the two budget kinds via `loopOut.timedOut && !loopOut.runBudgetExhausted` (per-try-cap-only) versus `loopOut.timedOut && loopOut.runBudgetExhausted` (run-budget), and the existing run-budget path already clears `failure_category`, sets `fail_reason = "run timeout"`, and forces `FailureClass = agent`. This requirement adds a distinct label for the per-try-cap-only case so the two kinds stay separately reportable. When the runner-side action loop terminates a try because the per-try-cap deadline fired (`loopOut.timedOut && !loopOut.runBudgetExhausted`) and no `FailureEvidence` was produced — neither executor Evidence nor the codex session-log / opencode disk-log fallbacks — the runner SHALL record the resolved category as `transient_infra` and the fail reason as `try budget exhausted; no output`, while overriding the `FailureClass` to agent-class so the freeze counter does not increment (the harness did not fail; the runner killed it). The run-budget path SHALL remain unchanged (no relabelling, no category) so existing dashboards and the operator-worthy capture gate continue to treat run-budget kills as they do today. The classification SHALL be visible in NRQL by filtering on the `fail_reason` text or on the absence of `failure_evidence.source` for a `transient_infra` category, and the two budget kinds SHALL be distinguishable via the existing `timeout_kind` tag (`try_cap` vs `run_budget`).

#### Scenario: Per-try-cap exhaustion is labelled distinctly
- **WHEN** a try is killed by the per-try-cap deadline only (the run budget is not exhausted) without producing any executor or post-hoc evidence
- **THEN** the recorded fail reason SHALL be `try budget exhausted; no output`
- **AND** the recorded category SHALL be `transient_infra`
- **AND** the try log's `timeout_kind` tag SHALL be `try_cap`

#### Scenario: Run-budget exhaustion keeps its existing labels
- **WHEN** a try is killed by the run-budget deadline (with or without the per-try-cap also firing) without producing any executor or post-hoc evidence
- **THEN** the recorded fail reason SHALL be `run timeout` (unchanged from prior releases)
- **AND** the recorded `failure_category` SHALL be empty (unchanged from prior releases)
- **AND** the try log's `timeout_kind` tag SHALL be `run_budget`
- **AND** this behaviour SHALL NOT regress as a side-effect of the per-try-cap classification

#### Scenario: Per-try-cap exhaustion does not increment the freeze counter
- **WHEN** a try is killed by the per-try-cap deadline without producing any evidence
- **THEN** the resolved `FailureClass` SHALL be agent-class (does-not-freeze) even though the category is `transient_infra`
- **AND** the freeze counter SHALL NOT increment

#### Scenario: Real infra failure with timeout is not relabelled
- **WHEN** a try times out and the executor or post-hoc fallback produced `FailureEvidence` (e.g. an API timeout signal, or a codex session log mapped to `harness_launch`)
- **THEN** the runner SHALL NOT override the category or fail reason with the budget-exhaustion labels
- **AND** the existing classification path SHALL apply

#### Scenario: Slow-vs-underqualified diagnostic is answerable via try-log tags
- **WHEN** operators need to distinguish "slow model getting the job done → revise time budget" from "model underqualified → review routing" for a budget-killed try
- **THEN** the try log SHALL continue to carry the diagnostic signals that answer this question: `runtime_ms`, `tool_calls`, `files_changed`, and `last_output_age_ms`, in addition to `timeout_kind`, `role`, and `runner`
- **AND** the `failure_evidence` block for budget-exhaustion cases SHALL NOT attempt to duplicate those tags (it stays a category/reason marker; the try log is the authoritative source for the slow-vs-underqualified question)

### Requirement: Runner tag carries the resolved model
The system SHALL populate the `runner` telemetry tag with the model actually used by the executor, so per-model NRQL queries do not collapse on bare-alias routes. The runner SHALL prefer `result.ResolvedModel` when non-empty, falling back to the route-supplied model otherwise, at every site that constructs the `runner` tag. The runner SHALL NOT emit a `RallyTry`, `RallyFailure`, or `RallyDiagnostic` event whose `runner` tag lacks a model component when the executor resolved one.

#### Scenario: Bare-alias route reports the executor's default model
- **WHEN** a route entry is configured with a bare alias (e.g. `cx` with no `:model`) and the executor resolved a default model
- **THEN** the `runner` tag on every telemetry event for that try SHALL be `<harness>:<resolved-model>`
- **AND** it SHALL NOT be the bare harness name

#### Scenario: Explicit route model stays authoritative
- **WHEN** a route entry supplies an explicit model and the executor reports the same model via `ResolvedModel`
- **THEN** the `runner` tag SHALL be `<harness>:<route-model>` (no behaviour change vs. prior releases)

#### Scenario: Empty resolved model falls back gracefully
- **WHEN** the executor did not populate `ResolvedModel` (e.g. an empty-model fixture executor in tests)
- **THEN** the runner SHALL fall back to the route-supplied model
- **AND** when both are empty, the `runner` tag SHALL be the bare harness name (no regression)
