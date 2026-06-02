## Draft: Improve Harness Consistency

## Why

Rally supports multiple harnesses (`claude`, `codex`, `gemini`, `opencode`,
`antigravity`, and generic/custom adapters). Each harness has a different CLI
output schema, but Rally should normalize those differences into a small,
consistent adapter contract. Divergent one-off behavior makes relays harder to
reason about and can make one harness appear less reliable than another when the
real issue is integration shape.

This is especially visible around headless execution:

- opencode is reliable in normal interactive use, but Rally's headless
  `opencode run --format json` integration has exposed parser and process
  lifecycle issues.
- Claude Code has some special rate-limit handling; equivalent infra-class
  handling for other harnesses should flow through common reliability
  classification rather than bespoke runner branches.
- Summary extraction, error reporting, tool counting, session IDs, and retry
  classification should look uniform to the relay runner.

## Intent

- Define a shared `Executor` adapter normalization contract that every harness
  implements.
- Keep harness-specific parsing at the adapter boundary, but normalize into the
  same concepts:
  - final assistant text / structured `TryResult.Summary`
  - short bounded fallback summary or error indicator
  - tool call count where available
  - session/conversation ID where available
  - infra-class vs agent-class failure signals
  - rate-limit / retry-after evidence where available
  - process lifecycle state, including whether the adapter can detect clean
    completion separately from process exit
- Audit existing harness-specific special cases and move them into common
  classification helpers where practical.
- Avoid treating any harness as generally unstable because one headless adapter
  path has a bug.

## Initial Questions

- Should `TryResult` grow explicit fields for adapter-level error evidence,
  retry-after hints, or clean-completion markers, instead of encoding them only
  in summary text/log pattern matching?
- Which reliability classifications should be adapter-provided vs inferred from
  logs by shared pattern helpers?
- Can opencode early process reaping be implemented safely once parser-level
  `step_finish` / error handling is correct, or should it remain a separate
  lifecycle probe capability?
- What is the minimum conformance test suite every harness adapter should pass?

## Candidate Work

- Add adapter conformance tests for structured success, unstructured final text,
  no final text, infra/rate-limit error, tool use, and session ID behavior.
- Refactor harness-specific rate-limit and infra detection into shared
  reliability classification utilities where possible.
- Document which behavior may be harness-specific and which behavior must be
  uniform at the runner boundary.
- Consider a harness capability matrix for liveness probe, resume, model
  rotation, clean completion detection, tool counting, and structured output.
