---
name: add-new-harness
description: Add a new built-in Rally harness end to end. Use when Rally needs first-class support for a new agent CLI, including CLI flag discovery, aliases, default model/config support, executor integration, real-backend tests, README documentation, and updates to test-driving-rally and phone-a-friend guidance.
license: MIT
metadata:
  author: rally
  version: "1.0"
---

# Add New Harness

Use this when adding a built-in harness like the Antigravity `agy` integration. The target outcome is not just "can run a command"; it is a routed, documented, tested Rally harness with aliases, model config, reliable logs, and real CLI verification.

## Workflow

1. **Orient and preserve state**
   - Capture `git status --short --branch` before editing.
   - Read the current built-in harness patterns in `internal/agent/`, `internal/config/`, `internal/relay/`, `cmd/rally/`, README, and existing real-backend tests.
   - If the user asks for delegation, use subagents for bounded sidecar work such as CLI flag discovery or touchpoint mapping. Keep integration and final verification local.

2. **Discover the CLI contract**
   - Run `<binary> --help`, `<binary> --version`, and help for relevant subcommands.
   - Identify the required non-interactive prompt mode, permission/approval bypass flag, model-selection mechanism, output format, session/resume flags, timeout flags, and log flags.
   - Manually smoke-test the smallest safe prompt. Record exact commands and failure modes.
   - Do not assume familiar flags exist. Verify spelling, argument placement, and whether flags accept `--flag value` or only `--flag=value`.

3. **Implement first-class harness support**
   - Add a built-in executor with the same `Executor` contract as existing harnesses.
   - Add canonical harness name and aliases in config resolution, route validation, route runtime, mix parsing, and tests.
   - Add default model support in config structs, init templates, `rally config`, `rally init roles`, named model lookup, and save/load paths.
   - If the CLI lacks a model flag, use the CLI's documented or observed settings mechanism, restore prior settings after each run, and serialize access with a process-level lock.
   - Capture enough log output to debug failures without flooding Rally summaries.
   - Implement resume/session support only when the CLI exposes a reliable session id or conversation flag.

4. **Update durable guidance**
   - README must list the supported built-in harnesses and aliases, including the new harness binary and any special model/config behavior.
   - Update `openspec/specs/executor/spec.md` if the executor surface changes.
   - Update `.claude/skills/test-driving-rally/SKILL.md`:
     - compatibility line,
     - real-backend coverage list,
     - `which` command,
     - canonical model slug table,
     - known environment failures,
     - default version bump guidance.
   - Update `.claude/skills/phone-a-friend/SKILL.md` and its references:
     - built-in alias table,
     - Rally examples when useful,
     - direct headless CLI command shape when known,
     - canonical model notes and failure signatures.

5. **Test the harness**
   - Add unit tests for output parsing, CLI args, model resolution, aliases, config load/save, route checks, init output, and failure classification where applicable.
   - Add a targeted real-backend test in `internal/relay/runner_real_backend_test.go` guarded by `RALLY_TEST_REAL_AGENTS=1`.
   - Run focused tests first, then `go test ./...`.
   - Build from source and verify `rally version`.
   - Run one manual relay or direct CLI smoke test that proves the real CLI works in this environment.
   - If the full real-backend suite has unrelated provider failures, record exact failures and still run the new harness's targeted real test.

6. **Version and wrap up**
   - When committing implementation work, default to a **minor** version bump in `internal/buildinfo/VERSION` unless the user explicitly asks for patch or major.
   - Do not create or push release tags by hand. Rally release tags are created from the VERSION bump on `main`.
   - Update `tmp/session-handoff.md` with CLI findings, implemented files, tests run, and known residual risks.
   - Keep skill/documentation updates separate from code patches when practical, especially if `test-driving-rally` changed.
   - Push only when the user asked for it.

## Acceptance Checklist

- New aliases resolve in `rally routes check` and `rally relay --agent`.
- Default model and named model shortcuts work from `.rally/config.toml`.
- `rally init` and `rally config` expose the new model field.
- README's supported harness list includes the new harness.
- `test-driving-rally` and `phone-a-friend` know the new CLI and model.
- Unit tests, `go test ./...`, build/version check, and targeted real-backend test pass or have clearly recorded environment-only failures.
