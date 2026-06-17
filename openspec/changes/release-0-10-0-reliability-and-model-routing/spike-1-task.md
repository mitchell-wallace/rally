# Spike 1 Task: Empirical Harness and Tail Behavior Checks

## Purpose

Validate the assumptions behind the Rally 0.10.0 reliability and model routing work before implementation.

The current draft/proposal/design include intended behavior for reasoning level aliases, warning-level lap mismatch handling, and `rally tail` improvements. This spike must verify the real behavior of the relevant harnesses and the current `rally tail` command with live executions.

## Questions to Answer

1. How is reasoning effort/level actually set for each supported harness that exposes it?
2. Which harnesses do not support reasoning effort, and how should Rally represent that cleanly?
3. What does `rally tail` do today during a real active run?
4. Does `rally tail` currently follow new log output, select the active try, and present enough context to distinguish run/try/role?
5. What exact telemetry evidence supports changing `wrong_lap_consumed` and `multi_lap_consumed` from failure-level events to warning-level events?

## Required Checks

### 1. Reasoning effort by harness

Try the smallest real invocation available for each harness, using a harmless prompt such as asking the model to print one short sentence.

Check at least:

- `codex`
- `claude`
- `opencode` with a reasoning-capable model, such as DeepSeek if configured
- `gemini`
- `antigravity`

For each harness, record:

- Whether reasoning effort/level is supported.
- The exact CLI flag, config field, environment variable, or model string needed.
- Accepted values, if discoverable by real command help or live execution.
- What happens when an unsupported reasoning value is supplied.
- Whether unsupported reasoning should be a config validation error, warning, or silently ignored.

Expected prior assumption to verify:

- `codex` supports reasoning effort.
- `claude` supports reasoning effort/level.
- Some `opencode` models support reasoning effort/level.
- `gemini` does not expose Rally-usable reasoning effort.
- `antigravity` does not expose Rally-usable reasoning effort.

Do not rely only on documentation. Use real CLI behavior where possible and cite the commands/results in the report.

### 2. Live `rally tail` behavior

Create or use a disposable tiny repo/task, such as building a simple Python API and testing it, then run Rally on it.

During an active Rally run:

- Run `rally tail`.
- Run `rally tail --try 0` if supported.
- Observe whether output updates as new log lines are written.
- Observe whether it follows the active try or only the latest completed try.
- Observe whether run/try/role context is visible enough for a user to understand what they are watching.
- Capture any awkward formatting, missing color, or stale-output behavior.

Record the exact commands used and the observed behavior.

### 3. Telemetry evidence

Use the New Relic APM dashboard or CLI.

Check Rally events related to:

- `wrong_lap_consumed`
- `multi_lap_consumed`
- short rate limit or provider limit events around 2026-06-16 11:11 AEST, especially the Prayer-app repo / dune-vm incident if present

Record:

- Issue IDs.
- Event levels.
- Failure category tags.
- Any run/try/lap metadata present.
- Whether current categorization matches intended behavior.

Known issue IDs from the first pass to re-check:

- `RALLY-4`
- `RALLY-6`
- `RALLY-C`
- `RALLY-8`
- `RALLY-9`
- `RALLY-2`

## Deliverable

Write a concise report in this change folder named `spike-1-report.md`.

The report should include:

- A table of harness reasoning support and exact settings.
- A short transcript or summary of the live `rally tail` test.
- Telemetry findings with issue IDs and implications for error handling.
- Recommended changes to `draft.md`, `proposal.md`, `design.md`, and `tasks.md` if the spike disproves any current assumption.

## Constraints

- Do not implement product changes during this spike.
- Do not modify unrelated files.
- Prefer a disposable test repo for live Rally behavior.
- If a harness is unavailable or blocked by quota, record the exact blocker and the strongest evidence still available.
