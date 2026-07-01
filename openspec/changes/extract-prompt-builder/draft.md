## Draft: Extract Prompt Builder

Status: drafted 2026-07-01. Concept-only capture, deliberately not tied to the
current file layout — revisit and re-anchor when it is picked up. Anchored
loosely to commit `1505f02`, before `modularize-harness-adapters` reshapes the
harness packages.

## Why

Prompt construction is currently a single builder co-located with the executor
contract (today `BuildPrompt` in `internal/agent/prompt.go`; after
`modularize-harness-adapters` it moves with the contract into
`internal/harnessapi`). That is fine while the logic is a straightforward
concatenation of role/task/general sections. As prompt assembly grows more
distinct concerns — richer section composition, per-role shaping, conditional
blocks — it becomes its own responsibility that shouldn't sit inside the harness
contract package.

## Intent

Give prompt construction its own module with a distinctly owned responsibility,
isolated from the harness executor contract. Harnesses consume the built prompt;
they don't own how it's assembled.

## Timing

Not a priority now — the current builder is simple enough to leave where it is.
Worth doing once prompt-construction logic starts getting more complex. Sequence
after `rename-rally-roles`, and after `build-new-tui` (both are higher priority).

## Out of Scope

- Changing prompt content or the built-prompt contract.
- Any harness-adapter restructuring (owned by `modularize-harness-adapters`).
