## Future Proposals

Ideas that are out of scope for v0.2.0 but worth capturing for later.

### Mock Agent CLI Binaries

**Current state (v0.2.0)**: `FixtureExecutor` is an in-process test double — it applies precomputed diffs and returns canned JSON outputs without invoking any real agent CLI. This is sufficient for e2e testing of rally's relay runner, store, and CLI.

**Future idea**: Build standalone mock CLI binaries that impersonate each agent CLI (`claude`, `codex`, `gemini`, `opencode`). These would live in fixture directories and behave like real CLIs:

- `claude -p "hello world in python"` → applies a precomputed diff from the fixture set, exits 0
- `claude -p "random prompt not part of fixture"` → fails with a realistic error
- `claude --unrecognised-flag` → fails with a usage error

This would enable testing the full executor → subprocess pipeline end-to-end, including CLI flag construction, output format parsing, and error handling — things that FixtureExecutor bypasses by operating above the Executor interface.

Each supported agent would get its own mock binary. The mock would match prompts against a fixture manifest and either apply the corresponding diff or fail.

**Why not now**: FixtureExecutor covers the critical testing needs for v0.2.0. Mock CLI binaries add build complexity (separate binaries, PATH manipulation in tests) and are most valuable once the per-agent output parsing is stable and needs regression testing.
