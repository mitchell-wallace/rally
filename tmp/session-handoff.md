# Session Handoff — Antigravity Harness Support

**Date:** 2026-05-21
**Branch:** `main`
**Rally version:** `0.8.0`

## User request

Add first-class Rally support for Google Antigravity CLI (`agy`) after the
Antigravity/Gemini 3.5 Flash release, including default model/config support,
end-to-end testing, version bump, and push.

## What changed

- Added built-in `antigravity` harness with `ag` and `agy` aliases.
- Added `AntigravityExecutor` using:
  - `agy --print`
  - `--dangerously-skip-permissions`
  - `--print-timeout=<duration>`
  - optional `--conversation=<id>` resume
- Added settings-backed model selection because `agy 1.0.0` has no model flag.
  Rally temporarily writes the resolved model label to
  `~/.gemini/antigravity-cli/settings.json` and restores the previous file
  after the run.
- Added `antigravity_model` under `[defaults]`, root-level compatibility
  loading, `rally config` support, `rally init` template support, and
  `rally init roles` seeding to `Gemini 3.5 Flash (High)`.
- Added named-model support through `[harness.ag.models]`,
  `[harness.agy.models]`, or `[harness.antigravity.models]`.
- Updated fallback alias maps in config, relay mix parsing, route runtime,
  route suggestions, and tests.
- Updated README, OpenSpec executor spec, and the `test-driving-rally` skill.
- Bumped `internal/buildinfo/VERSION` from `0.7.13` to `0.8.0`.

## CLI findings

`agy --help` on 2026-05-21 showed:

- `-p` / `--print` for non-interactive prompt mode.
- `--print-timeout` must be supplied as `--print-timeout=20s` before/alongside
  `--print`; `--print --print-timeout 20s ...` is interpreted as prompt text.
- `--dangerously-skip-permissions` works with print mode.
- `--conversation=<uuid>` resumes a specific conversation, but print mode
  reprints previous visible assistant output before the new response.
- No `--model` flag exists; `agy --model ...` exits with
  `flags provided but not defined: -model`.
- Conversation IDs are visible in the `--log-file` output as
  `Print mode: conversation=<uuid>`.

The user settings file was restored after real Antigravity testing; it still
contains `"model": "Claude Opus 4.6 (Thinking)"` after the Rally run.

## Verification

Passed:

- `go test ./...`
- `go build -o /tmp/rally ./cmd/rally && /tmp/rally version`
  - output: `rally v0.8.0-dev`
- `agy --dangerously-skip-permissions --print-timeout=20s --print ...`
  - returned the requested exact text.
- `RALLY_TEST_REAL_AGENTS=1 go test ./internal/relay -run TestRealBackend_AntigravityRelay -v -timeout 240s`
  - passed in 114.76s.
  - verified file creation, completed try record, `agent_type=antigravity`, and
    conversation ID capture in appended `agy` log.
- Targeted real-backend Codex smoke started after Antigravity:
  - `TestRealBackend_CodexRelay` passed in 29.44s.

Not clean in this environment:

- Full `RALLY_TEST_REAL_AGENTS=1 go test ./internal/relay/... -run TestRealBackend -v -timeout 600s`
  was stopped because `TestRealBackend_ClaudeBasicRelay` returned a Claude
  harness error and entered the existing frozen-agent wait path.
- A targeted subset run was stopped after `TestRealBackend_OpenCodeRelay`
  produced no changes and entered the existing frozen-agent wait path.

These failures were in external real-agent backends, not in the new
Antigravity adapter. Unit coverage and the new Antigravity real backend test
passed.

## Files touched

Key code paths:

- `internal/agent/antigravity.go`
- `internal/config/config_v2.go`
- `cmd/rally/main.go`
- `cmd/rally/init_roles.go`
- `internal/cli/config.go`
- `internal/relay/mix.go`
- `internal/relay/route_runtime.go`
- `internal/cli/routes_check.go`

Key tests:

- `internal/agent/agent_test.go`
- `internal/config/config_v2_test.go`
- `internal/relay/runner_real_backend_test.go`

Docs/state:

- `README.md`
- `openspec/specs/executor/spec.md`
- `.claude/skills/test-driving-rally/SKILL.md`
- `internal/buildinfo/VERSION`

## Carryover

- Investigate why Claude real-backend tests are currently returning harness
  errors in this environment.
- Investigate current Opencode behavior: `TestRealBackend_OpenCodeRelay`
  returned no changes and hit frozen-agent wait.
- Consider adding cross-process locking for the Antigravity settings override
  if concurrent Rally processes using `ag` become a real workflow.
