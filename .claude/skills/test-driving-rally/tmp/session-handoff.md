# Session handoff — refactor sanity smoke (2026-07-02)

Built current Rally from source as `/tmp/rally` and smoke-tested behavior after the
recent structural OpenSpec sequence: `decompose-relay-runner`,
`slim-cli-composition-root`, `add-architecture-guardrails`, and
`modularize-harness-adapters`.

## Verdict

Rally still works across the tested real-backend paths after one regression fix.

## Regression found and fixed

- **Antigravity wrote to scratch instead of the repo.** `TestRealBackend_AntigravityRelay`
  initially failed with `all agents unavailable` / `no changes made`. Manual Rally
  reproduction showed `agy` summaries linking to
  `~/.gemini/antigravity-cli/scratch/antigravity-e2e.txt`; the log showed it tried to
  read `.rally/README.md` from scratch. Direct `agy --print` worked when the prompt said
  "In this git repo". Fix: generated prompts now include `## Workspace` with the resolved
  repo path and an explicit instruction not to use a scratch directory. Commit:
  `459eaf4 fix: include workspace in agent prompt`.
- Version bumped `internal/buildinfo/VERSION` from `0.12.0` to `0.13.0` per the
  test-driving workflow because this session produced a code patch.

## Behavior smoke coverage

- Baseline real-backend suite: Claude basic relay, Claude + laps, per-repo log scoping,
  Codex, built-in OpenCode, Antigravity, retry-budget resilience, and custom OpenCode
  harness all pass after the fix.
- CLI/app seam subagent: `rally routes check` schema warning, invalid harness rejection,
  missing default warning, `rally init`, default-route relay without `--agent`,
  `rally instructions show`, and `rally progress` all passed.
- Harness registry subagent: built-in `op`, custom generic `mycode` using
  `opencode run $PROMPT --format json`, and a two-iteration `op mycode:mini` route all
  passed with expected `agent_type`/`agent_mix` state records.
- Laps/resume subagent: two-lap Claude relay drained the queue and installed hooks;
  `--new` completed the old relay and reset agent status. A synthetic `--resume` setup
  resumed the existing relay and preserved status but failed no-change tries because of
  the artificial frozen-state seed; not counted as a Rally start-decision regression.

## Verification commands

- `go test ./internal/harnessapi`
- `go build -o /tmp/rally ./cmd/rally/`
- Manual Rally Antigravity reproduction in `/tmp/rally-ag-cli` after the fix: passed.
- `go test ./...`: passed.
- `go run ./tools/archguard --ci`: exit 0; emitted only existing warn-budget file-size
  warnings.
- `RALLY_TEST_REAL_AGENTS=1 go test ./internal/relay/... -run TestRealBackend -v -count=1 -timeout 900s`: passed.

## Outstanding

- Usage-limit behavior was intentionally not tested; the user noted nothing is close to
  limits right now.
- No interactive TUI keypress probes were driven; coverage focused on real CLI/backend
  behavior and non-interactive state semantics.
