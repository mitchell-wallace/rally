## Draft: Improve Error Categorisation And Handling

## Why

Recent real Rally relays exposed that the current failure labels are too coarse
and sometimes actively misleading. The visible CLI output is improving, but the
underlying categorisation still conflates different operational causes:

- Long subscription usage limits are shown as generic `agent error`, generic
  `rate limit`, or `incomplete: file changes without finalization`.
- A Codex run can be labelled `claude rate-limit interrupt` because the shared
  classifier matches unscoped text in a log tail.
- Dirty harness-local settings can make startup/provider errors look like agent
  file edits without finalization.
- Invalid model names are currently a workaround for per-relay runner disabling,
  but the resulting failures are not categorized as configuration errors.

These labels drive operator decisions, retry timing, runner freezing, Sentry
issues, and the on-screen retry story. When they are wrong, Rally wastes tries,
waits for the wrong duration, routes poorly, and makes post-relay debugging
harder.

## Evidence From Recent Runs

### Antigravity Claude usage exhaustion

Container `30cb9624d27c`, Rally 0.8.3, tries 57-61:

- CLI displayed five short failures as `agent error`.
- Raw Antigravity logs repeatedly contained:
  `RESOURCE_EXHAUSTED (code 429): Individual quota reached ... Resets in
  123h50m...`.
- There were zero changed files, so incomplete detection did not mask the error.
- This is a subscription usage/quota limit with a reset measured in days, not an
  ordinary per-minute API rate limit.

Container `ca5990ebcc41`, Rally 0.8.4, tries 242-246:

- CLI displayed `incomplete: file changes without finalization`.
- Raw Antigravity logs again showed `RESOURCE_EXHAUSTED ... Resets in
  118h44m...`.
- The only changed path was `.claude/settings.local.json`; there were no
  agent-conducted task edits.
- Dirty-path based incomplete classification masked the stronger provider
  failure.

### Claude intentionally disabled by invalid model

Container `ca5990ebcc41`, tries 247-251:

- The configured model was intentionally renamed to
  `claude-opus-4-8-disabled` to preserve Claude usage for a parallel session.
- Early tries produced `model_not_found` / HTTP 404 with text saying the selected
  model may not exist or may not be accessible.
- Later tries produced `403 CONNECT blocked: rate limit exceeded for
  api.anthropic.com`.
- Rally displayed `incomplete: file changes without finalization` because the
  same harness-local `.claude/settings.local.json` path was dirty.
- The clean category should be invalid model/configuration, not incomplete. A
  cleaner per-relay disablement mechanism should avoid this pattern entirely.

### Codex verifier runs mislabelled as Claude rate limits

Container `30cb9624d27c`, Rally 0.8.3, tries 70-74:

- Harness was Codex (`gpt-5.4`) on a VERIFY lap.
- The attempts ran for around 16-18 minutes and produced useful verification
  summaries, recorded laps, and Rally state changes.
- Rally labelled several failures `claude rate-limit interrupt` or
  `rate limit generic`.
- The last 50 log lines contained incidental strings such as `rate-limit`,
  `rate limit`, and `Claude`; the classifier currently uses broad global
  substring patterns. This is classifier bleed-through, not a Codex quota event.
- Waiting one minute repeatedly is mismatched to the underlying evidence and did
  not resolve the run.

### Incomplete retry-to-success

Container `30cb9624d27c`, Rally 0.8.3, try 62 then retry success:

- A Claude try changed files and failed without finalization.
- The retry completed and committed successfully.
- This validates retry-to-success and resume-on-retry as useful, but the resumed
  prompt should include finalization guidance, not only previous summary/session
  context.

## Problem Shape

The current taxonomy is not wrong in spirit, but it is missing important
distinctions and priority rules:

- `rate limit` is overloaded. Short API throttling, provider overload,
  subscription usage limits, proxy blocks, and invalid-model fallback failures
  need different actions.
- `incomplete` currently wins too early when any new dirty path exists outside
  `.rally/` and `.laps/`. Some dirty paths are harness-local or operator-local
  state, not task progress.
- Classifier patterns are not harness-scoped. A label can name the wrong
  harness.
- Classification mostly reads arbitrary text tails. Some harnesses emit
  structured error events that should be parsed first.
- Retry strategy and display category are coupled too tightly. The CLI label
  should tell the operator what happened; the retry strategy should use parsed
  retry/reset metadata where available.

## Goals

- Produce truthful, operator-actionable failure labels in the CLI footer,
  retry line, try records, telemetry, and Sentry issue data.
- Separate short rate limits from long usage/quota limits.
- Parse reset/retry timing when providers expose it, and avoid arbitrary
  one-minute waits for multi-hour or multi-day usage exhaustion.
- Prevent dirty harness-local files from masking provider/configuration errors
  as `incomplete`.
- Make classification harness-aware so Codex cannot be labelled as a Claude
  rate-limit interrupt.
- Preserve the useful incomplete finalization flow for real task-file changes
  without finalization.
- Feed better categories into route fallback, runner freeze/disable decisions,
  and retry/resume policy.
- Add focused regression coverage using the observed signatures from this
  thread.

## Non-Goals

- Do not redesign the full harness adapter contract. That remains available for
  the later `improve-harness-consistency` work once error handling has moved.
- Do not build the new TUI here. This change may add data/actions that a future
  start-of-run config or inflight steering UI can use.
- Do not require OpenSpec for Rally core behavior.
- Do not change the basic retry-to-success behavior for genuine incomplete
  runs, except to ensure resumed attempts receive the right guidance.

## Proposed Taxonomy

Use stable internal categories and short display labels. Suggested starting set:

- `usage_limit`: Subscription or account quota exhausted; reset is measured in
  hours/days or explicitly described as a usage limit.
  - Examples: Antigravity/Gemini `RESOURCE_EXHAUSTED ... Individual quota
    reached ... Resets in 118h44m37s`; Codex "You've hit your usage limit";
    Claude `rate_limit_event.rateLimitType` of `five_hour` or `seven_day` when
    overage is rejected.
  - Strategy: pause/freeze that runner until reset if parsed; route away. Do
    not retry every minute.
  - Display: `usage limit`, optionally `usage limit, resets in 118h44m`.

- `short_rate_limit`: Short throttling or too-many-requests condition where a
  short wait or `Retry-After` makes sense.
  - Examples: `429 Too Many Requests` with a small `retry-after`; provider text
    that clearly describes request throttling.
  - Strategy: wait parsed retry-after or a conservative short default, then
    resume.
  - Display: `rate limit`, optionally with wait duration.

- `provider_overloaded`: Provider 5xx/overload with no account quota signal.
  - Examples: Claude `529 Overloaded`, `503 Service Unavailable`.
  - Strategy: resume retry with exponential or existing retry budget behavior;
    do not treat as usage exhaustion.
  - Display: `provider overloaded`.

- `invalid_model`: Configured model does not exist or is inaccessible.
  - Examples: Claude `model_not_found`, "selected model may not exist or you may
    not have access".
  - Strategy: fail/rotate/disable that runner entry until config changes; do not
    spend all retry budget on identical attempts.
  - Display: `invalid model`.

- `auth_or_proxy`: Authentication, OAuth, or proxy/connect block.
  - Examples: `Failed to authenticate`, `CONNECT blocked`, OAuth token failures.
  - Strategy: route away or mark runner unavailable until operator action.
  - Display: `auth/proxy error`.

- `harness_launch`: Local executable/config launch failure.
  - Examples: `fork/exec`, `argument list too long`, executable missing.
  - Strategy: fresh restart or rotate depending on subtype.
  - Display: `harness launch error`.

- `incomplete_finalization`: Meaningful task-file changes were produced by this
  try, but the lap did not finalize with `laps done` or `laps handoff`.
  - Strategy: resume retry with explicit finalization guidance.
  - Display: `incomplete: file changes without finalization`.

- `agent_error`: Agent exited or returned incomplete without stronger infra,
  configuration, quota, or incomplete-finalization evidence.
  - Strategy: existing retry/fresh behavior.
  - Display: `agent error`.

## Classification Priority

1. Parse structured harness events and executor-returned error metadata first.
2. Detect provider/configuration/quota errors from structured data or bounded
   error snippets.
3. Compute meaningful task-file change evidence for incomplete finalization.
4. Apply harness-scoped text patterns as a fallback.
5. Default to `agent_error`.

This priority is the key behavior change. A dirty path should not mask
`RESOURCE_EXHAUSTED`, `model_not_found`, or `Failed to authenticate`, especially
when the changed file is known harness-local state.

## Harness-Local Dirty Path Handling

Add a small exclusion or classification helper for paths that are not task work:

- Rally state remains excluded: `.rally/`, `.laps/`.
- Add known harness-local transient paths observed in relays, starting with
  `.claude/settings.local.json`.
- Consider whether repo-local tool settings created by harness startup should be
  excluded entirely or recorded under a separate `harness_state_changed` flag.

The goal is not to hide real project configuration changes. The helper should be
conservative and covered by tests:

- A try that only dirties `.claude/settings.local.json` and logs
  `RESOURCE_EXHAUSTED` is `usage_limit`.
- A try that changes `src/foo.go` without finalization remains
  `incomplete_finalization`.
- A try that changes both a meaningful task file and a provider error should
  prefer the provider error if the subprocess never reached useful work; this may
  need structured evidence or tool-call/session evidence.

## Structured Error Evidence

Prefer extracting a compact `FailureEvidence` from each executor or log parser:

```go
type FailureEvidence struct {
    Category     FailureCategory
    Harness      string
    Provider     string
    Message      string
    StatusCode   int
    ResetAfter   time.Duration
    ResetAt      *time.Time
    RetryAfter   time.Duration
    RawSignal    string
}
```

This does not need to be the final shape, but the runner needs a typed signal so
it is not inferring everything from arbitrary text. The first pass can still live
in reliability classification helpers if changing `TryResult` is too broad, but
the design should move toward typed evidence.

Observed parser targets:

- Antigravity/Gemini: `RESOURCE_EXHAUSTED`, `Individual quota reached`, `Resets
  in <duration>`.
- Claude: stream JSON `rate_limit_event`, `api_error_status`, `error` values
  such as `model_not_found` and `authentication_failed`, result text for 529
  overload.
- Codex: `turn.failed` usage-limit messages and any structured error objects
  emitted by the CLI.
- Opencode: JSON error events and provider API errors, with harness-specific
  parsing kept out of generic substring labels.

## Retry And Routing Behavior

- `usage_limit`: mark the runner unavailable until parsed reset where possible.
  If no reset is available, apply a long conservative pause and route away. The
  one-minute wait is inappropriate for five-hour or seven-day limits.
- `short_rate_limit`: wait parsed retry-after; if absent, use a short default.
- `invalid_model`: do not retry the same runner/model repeatedly. Mark the entry
  exhausted for the relay or require operator/config change.
- `auth_or_proxy`: route away and surface operator action; repeated retry is
  unlikely to help.
- `provider_overloaded`: retry/resume within budget; do not freeze for days.
- `incomplete_finalization`: resume retry and include finalization instructions.

## Resume Prompt Follow-Up

When retrying or resuming after an incomplete finalization, include the normal
finalization instructions in the resumed prompt. Add a small resume-specific
snippet along these lines:

```text
Continue the current lap. If you need to double-check the scope, run `laps get`.
Finish with `laps done` when complete, or `laps handoff` if blocked.
```

This should apply to retry resumes as well as operator pause/resume when the run
is laps-backed.

## CLI Display And Records

- Show the accurate category in the footer and collapsed retry line.
- Include reset/wait information when known:
  - `usage limit, resets in 123h50m`
  - `rate limit, waiting 2m`
  - `invalid model`
- Store the stable category separately from the display label in try records and
  telemetry. `fail_reason` can remain human-readable, but machine-readable data
  should not require string parsing.
- Avoid harness-specific labels that can be wrong for another harness. Prefer
  `usage limit` over `claude rate-limit interrupt`, unless the harness is Claude
  and the label is intentionally Claude-specific.

## Configuration And Steering Tie-In

The invalid-model workaround points to an adjacent need: per-relay or start-time
runner disablement. This draft does not need to implement the UI, but the error
handling pass should leave room for:

- disabling a harness or model for the current relay without editing config;
- marking a runner unavailable because the classifier found usage exhaustion or
  invalid model config;
- showing why a runner is unavailable and when it may recover.

This overlaps with the directional rethink in `build-new-tui`: a lighter
start-of-run config flow and inflight steering may be more useful than a full
alternate runtime TUI.

## Candidate Work

- Add `FailureCategory` names and update `StrategyDecision` to carry stable
  category plus display label.
- Add parser helpers/tests for:
  - Antigravity/Gemini `RESOURCE_EXHAUSTED ... Resets in ...`;
  - Claude `rate_limit_event` five-hour/seven-day usage limit;
  - Claude `model_not_found`;
  - Claude auth/proxy failures;
  - Codex usage-limit messages;
  - provider overload such as 529.
- Make rate-limit patterns harness-scoped or remove harness names from generic
  reasons.
- Reorder classification so strong provider/configuration/quota evidence beats
  incomplete detection.
- Exclude conservative harness-local dirty paths from meaningful task-change
  detection.
- Add retry strategy tests proving usage limits do not sleep for one minute and
  loop through the retry budget.
- Add run-level regression tests using the concrete observed signatures:
  - Antigravity quota with zero changed files -> `usage_limit`;
  - Antigravity quota plus only `.claude/settings.local.json` dirty ->
    `usage_limit`;
  - Codex log tail mentioning `rate-limit` as prose -> not Claude rate limit;
  - Claude invalid model plus settings dirty -> `invalid_model`;
  - real task-file dirty with no finalization -> `incomplete_finalization`.
- Add resumed-attempt finalization prompt coverage for incomplete retries.

## Open Questions

- Should the first implementation add typed evidence to `TryResult`, or keep it
  in reliability/log parsing until the later harness-consistency pass?
- How should Rally represent long reset windows in scheduler state: paused until
  reset, frozen until reset, or a new unavailable/quota state?
- Should `usage_limit` be per harness, per harness+model, or per provider
  account? Antigravity Claude exhaustion may not imply Gemini models are also
  exhausted.
- What is the exact set of harness-local paths safe to exclude from meaningful
  task changes?
- Should invalid model/config errors fail the relay immediately when every route
  entry for that lane is invalid, or produce an operator prompt where possible?

## Acceptance Direction

- The observed examples from this thread classify accurately.
- Retry waits align with parsed reset/retry evidence.
- No Codex run can display `claude rate-limit interrupt`.
- Harness-local dirty files do not mask provider/config errors as incomplete.
- Genuine incomplete finalization still retries/resumes and includes finalization
  guidance.
- Try records and telemetry expose both stable category and human display reason.
