## Draft: Add Architecture Guardrails

Status: drafted 2026-06-29; updated 2026-06-29 to baseline against the post-#1/#2
tree and to enforce the first real internal edge — `internal/relay/runner →
internal/relay` — created by `decompose-relay-runner`.

This change adds tooling and CI checks. It should not change Rally runtime
behaviour.

## Why

Rally has production files well beyond healthy review size. Sizes below are a
2026-06-29 snapshot; this change lands *after* `decompose-relay-runner` (#1) and
`slim-cli-composition-root` (#2), which split `runner.go` into the
`internal/relay/runner` package and shrink `main.go`. The grandfather caps
(section B) MUST be regenerated against the then-current tree at implementation
time, not hard-coded to these figures:

- `internal/relay/runner.go`: 3,782 lines (gone after #1 — replaced by the
  carved `internal/relay/runner/*.go` files, each already under budget),
- `internal/config/config_v2.go`: 993 lines (split within `package config` by #2),
- `cmd/rally/main.go`: 863 lines (slimmed by #2),
- `internal/agent/opencode.go`: 801 lines (addressed by #4).

The largest tests are even larger:

- `internal/relay/runner_test.go`: 6,915 lines,
- `internal/agent/agent_test.go`: 2,812 lines,
- `internal/relay/runner_failure_telemetry_test.go`: 2,331 lines,
- `internal/config/config_v2_test.go`: 1,801 lines.

This is exactly the sort of architectural drift CI should catch early. The goal
is not to make line count a proxy for quality. The goal is to force a conscious
decision before files become too large for future maintainers and agents to
understand.

The codebase also needs import-boundary checks before new harnesses, new roles,
and a TUI add more packages. A UI helper should not become importable by harness
adapters just because both are under `internal/`. Modules should expose simple
public interfaces and keep internal complexity private.

## Intent

Add a lightweight architecture guard that checks:

- file-size warning and error budgets,
- disallowed imports between internal modules,
- external dependency confinement, such as New Relic only under telemetry,
- production code not importing test helpers.

Roll it out incrementally so existing large files can be split intentionally
without blocking unrelated work immediately.

## Candidate Work

### A. Prefer a small Go checker over regex-only linting

Possible implementation shapes:

- `go list -json` plus `jq`: good for package import graph checks, but weak for
  file-level diagnostics and line budgets.
- Shell plus `rg`/`wc`: very fast for simple checks, but brittle for Go imports,
  aliases, build tags, and generated files.
- Custom Go checker: best long-term fit because it can use `go/parser`,
  `go/token`, and ordinary filepath logic without adding third-party
  dependencies.

Recommended first implementation: a small custom Go command, for example
`tools/archguard`, plus unit tests for the policy engine.

The checker should:

- parse imports with `parser.ImportsOnly`,
- count physical lines,
- skip generated files that begin with `// Code generated`,
- skip `testdata`, `.git`, `.rally`, `.laps`, archive folders, and build output,
- apply separate production and test-file policies,
- emit clear file-specific diagnostics.

### B. Start with file-size budgets and grandfather current outliers

Recommended initial budgets:

| File kind | Warning | Hard error for new files | Existing outliers |
|---|---:|---:|---|
| production `.go` | 500 lines | 800 lines | grandfather with per-file caps |
| `_test.go` | 900 lines | 1,800 lines | grandfather with per-file caps |
| generated `.go` | exempt | exempt | require `// Code generated` |

Grandfathering should be explicit, and the map should be *generated from the
then-current tree* — by the time this change lands, #1 will have removed
`runner.go` entirely. Example policy shape (illustrative caps, regenerate at
implementation time):

```go
var fileBudgets = map[string]int{
    // runner.go is gone post-#1; cap whatever remains over budget instead, e.g.
    // "internal/relay/runner/run_one.go": <actual>,
    "internal/agent/opencode.go": 801, // until #4 splits it
    // ...regenerate from `archguard --report` against HEAD
}
```

The check should fail if a grandfathered file grows above its cap. As refactors
land, reduce or remove the caps. Because #1 lands first, the relay caps should
already be low — this change ratchets, it does not have to absorb the 3,782-line
outlier.

Warnings over 500 lines should appear in local output. CI should hard-fail only
on new files over the error budget, growth beyond a grandfathered cap, and import
boundary violations.

### C. Add production import-boundary rules

Start with rules that match the current architecture rather than an ideal future
architecture. Tests can be looser initially.

Initial production rules:

- `cmd/rally` may import internal packages as the process composition root.
- **`internal/relay/runner` (the orchestrator) may import** orchestration
  dependencies such as `relay`, `agent`, `agent_prompt`, `gitx`, `keyboard`,
  `laps`, `monitor`, `progress`, `reliability`, `routing`, `store`, `style`,
  `telemetry`, `textutil`, and `user_prompt/roleloader`.
- **`internal/relay` (the primitives) must not import `internal/relay/runner`.**
  This is the flagship edge: `decompose-relay-runner` (#1) established the one-way
  `runner → relay` dependency, and this rule keeps it one-way. The relay
  primitives may import only what `relay.go`/`resilience.go`/`mix.go` need (e.g.
  `store`, `reliability`, `routing`, `agent` for `Resolver`).
- `internal/relay` and `internal/relay/runner` must not import `internal/config`
  or `internal/cli`.
- `internal/agent` may import `agent_prompt`, `reliability`, and `textutil`.
- `internal/agent` must not import `config`, `store`, `laps`, `relay`,
  `telemetry`, `cli`, or future UI packages.
- `internal/config` may import `agent`, `routing`, and `store` for current
  model and path resolution.
- `internal/config` must not import `relay`, `cli`, `laps`, `progress`,
  `telemetry`, or prompt packages.
- `internal/routing` may import `agent` only among higher-level Rally packages.
- `internal/store` may import `reliability` and `textutil`, but not `agent`,
  `config`, `laps`, `progress`, `relay`, `telemetry`, or `cli`.
- `internal/reliability` may import `monitor`, but not `agent`, `store`,
  `relay`, `telemetry`, `config`, `laps`, or `routing`.
- `internal/laps` may import `release`, but not `relay`, `config`, `cli`,
  `progress`, `agent`, `store`, or `telemetry`.
- `internal/telemetry` may import `buildinfo`; New Relic imports should stay
  confined to `internal/telemetry`.
- Non-test files must not import `internal/testutil`.

The `runner`/`relay` split above already reflects `decompose-relay-runner` (#1),
which lands first. The harness-adapter rules should still be revisited after
`modularize-harness-adapters` (#4), since those package boundaries sharpen there.

### D. Add external dependency confinement rules

Initial rules:

- `github.com/newrelic/go-agent` only under `internal/telemetry`.
- `github.com/pelletier/go-toml` only under `internal/config`.
- `github.com/spf13/cobra` only under `cmd/rally`, `internal/cli`, and any
  intentionally command-shaped package.
- `github.com/charmbracelet/huh` only under interactive prompt/config packages.
- `github.com/charmbracelet/lipgloss` and terminal styling packages only under
  `internal/style`, `cmd/rally`, and future presentation packages.

Do not overfit dependency rules too early. The first pass should catch obvious
leaks, not encode every current incidental import forever.

### E. Wire into local and CI flows

Add a `just arch-check` recipe.

Add it to `just check` after formatting and `go vet`, or keep it as a separate
recipe for one release cycle before making it part of `check`.

Add a CI step to `.github/workflows/test.yml` in the `lint` job.

Suggested rollout:

1. Advisory local command with warnings and hard import failures.
2. CI hard-fails on disallowed imports and new oversize files.
3. CI hard-fails on grandfathered file growth.
4. Ratchet grandfather caps downward after decomposition changes land.

### F. Keep policy messages human-readable

Each failure should explain the architectural reason, not just the rule name.

Example:

```text
internal/agent/foo.go imports internal/telemetry: harness adapters should return
typed results/evidence; relay/runtime owns telemetry emission.
```

This mirrors the useful part of the referenced Prayer app import-boundary setup:
specific source scopes, denied imports, and helpful explanations.

## Testing Strategy

For the checker itself:

- Unit-test line counting, generated-file exemption, hidden directory skipping,
  import parsing, production/test distinction, and grandfather caps.
- Add fixture directories under the checker package's `testdata`.
- Test diagnostics so CI failures remain actionable.

For repository integration:

- Run the checker locally and commit the initial baseline.
- Run `just check`.
- Run `go test -count=1 ./...`.
- Confirm CI lint output is readable when a deliberate fixture failure is tested
  locally. Do not leave deliberate failures in the repo.

## Sequencing

1. Implement checker in advisory mode with line-count warnings.
2. Add import-boundary hard failures for the safest current rules.
3. Add CI wiring.
4. Ratchet file-size caps as large-file refactors land.
5. Add future module-boundary rules after harness and presentation packages are
   split.

## Open Questions

- Should the checker live under `tools/archguard`, `internal/archguard`, or as a
  repository-scanning test package?
- Should warnings be printed in normal CI logs, or should CI only emit hard
  failures while local `just arch-check` shows warnings?
- Should test files use the same hard budget as production files after the first
  refactor wave, or keep a larger threshold?
- Should architecture budgets apply to Markdown specs and scripts, or only Go
  files for the first version?

## Out of Scope

- General static analysis such as `golangci-lint`; see
  `adopt-lint-and-fuzz-gates`.
- Fuzz testing.
- Refactoring large files. This change enforces and ratchets the policy; the
  actual splits belong in targeted architecture changes.
