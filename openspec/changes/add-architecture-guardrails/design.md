# Design — add-architecture-guardrails

Tooling-and-CI change. No Rally runtime behaviour, no version bump, no release.
Baselined against the tree at commit `4c9d307` (2026-07-01), after
`decompose-relay-runner` (#1) and `slim-cli-composition-root` (#2).

## Goals / non-goals

- **Goal**: a standing CI gate that (a) keeps the internal import edges #1 and #2
  established one-way, (b) confines third-party deps to their owning packages,
  and (c) ratchets file size down rather than letting new files reach
  `runner.go` scale.
- **Goal**: a green-by-construction baseline — rules are generated from the
  current tree, so the gate enforces from day one with no failing baseline.
- **Non-goal**: deep static analysis or fuzzing (`adopt-lint-and-fuzz-gates`),
  splitting the large files themselves (their own targeted changes), and
  budgeting non-Go files.

## Decision 1 — checker lives at `tools/archguard`, in the main module

Resolves the draft's open question (`tools/` vs `internal/` vs a test package).

- **`tools/archguard`**, `package main`, in the **existing** module. Rationale:
  it is a repo-scanning *dev tool*, not part of the rally binary, so it does not
  belong under `internal/` (which is the product). Keeping it in the main module
  (not a nested module) means `go vet ./...`, `go test ./...`, and gofmt already
  cover it with no extra CI wiring, and `go mod tidy` stays a no-op because the
  tool imports **stdlib only** (`go/parser`, `go/token`, `go/ast`, `os`,
  `path/filepath`, `strings`, `bufio`). No third-party dependency is introduced.
- GoReleaser builds `cmd/rally` only, so `tools/archguard` is never released.
- The policy engine is a small library (`tools/archguard/policy` or same-package
  exported funcs) so it is unit-testable independently of `main`'s walk/print.
- `archguard` budgets apply to `archguard` too — keep its own files under budget.

Alternative considered: a `go:build tools` test package that fails via
`go test`. Rejected: a `main` with `--report`/`--ci` modes gives a better local
ergonomics story (regenerate baseline, advisory vs CI) and a clearer failure
surface than a test assertion.

## Decision 2 — policy is Go data, regenerated via `--report`

- File budgets and the grandfather map are Go values in the `archguard` package
  (no config file, no parser, no dep). Import rules and dependency-confinement
  rules are likewise Go tables.
- `archguard --report` prints the current over-hard-budget files as a ready-to-
  paste grandfather map and prints any import/dep violations. The implementer
  runs it against HEAD and pastes the result, so the committed caps always match
  the real tree at landing.
- `archguard --ci` exits non-zero only on **hard** violations (disallowed import,
  new file over the hard budget, grandfathered file grown above its cap,
  dependency-confinement breach, production import of `testutil`). Warnings
  (500/900) print but never set the exit code.
- Default (no flag) = local advisory: prints warnings **and** hard violations,
  exits non-zero on hard violations (so `just check` is a faithful local mirror
  of CI).

## Decision 3 — file-size budgets

| File kind        | Warning | Hard error (non-grandfathered) | Grandfathered |
|------------------|--------:|-------------------------------:|---------------|
| production `.go` |     500 |                            800 | per-file cap  |
| `_test.go`       |     900 |                          1,800 | per-file cap  |
| generated `.go`  |  exempt |                         exempt | must carry `// Code generated` |

- "Physical lines" = newline count (the same number `wc -l` reports).
- Test files keep the larger budget for now (resolves the draft's open question):
  the test outliers are mostly table-driven and the production splits come first;
  revisit tightening `_test.go` after the #4 refactor wave.
- A file in the grandfather map is exempt from the standard hard budget but fails
  if it exceeds **its own** recorded cap. A file **not** in the map fails if it
  exceeds the standard hard budget — that is how "new oversize file" is caught.

### Grandfather baseline (2026-07-01 snapshot — regenerate at implementation)

Production `.go` over 800:

| File | Cap |
|------|----:|
| `internal/relay/runner/run_one.go` | 1510 |
| `internal/agent/opencode.go` | 801 |

`_test.go` over 1,800:

| File | Cap |
|------|----:|
| `internal/agent/agent_test.go` | 2812 |
| `internal/relay/runner/run_one_test.go` | 2355 |
| `internal/relay/runner/runner_failure_telemetry_test.go` | 2331 |
| `internal/relay/runner/relay_steps_test.go` | 2238 |
| `internal/config/config_v2_test.go` | 1801 |

Everything else is under its hard budget; the production files in the 500–800
band (e.g. `route_runtime.go` 752, `monitor.go` 663, `providers.go` 621,
`routes_check.go` 619, `claude.go` 560, `store.go` 541, `relay_steps.go` 526,
`antigravity.go` 517) and tests in the 900–1,800 band emit advisory warnings
only and need no grandfather entry.

## Decision 4 — import-boundary rules (verified against the current graph)

Encode the **current** graph, not an idealized future one. The full production
graph at `4c9d307` is the source of truth; the rules below match it exactly, so
the baseline passes.

Flagship / composition-root edges (the reason this change exists):

- `internal/relay` MUST NOT import `internal/relay/runner` — keep #1's
  `runner → relay` edge one-way.
- `internal/relay` and `internal/relay/runner` MUST NOT import `internal/config`
  or `internal/cli`.
- `internal/release` MUST NOT import `internal/app` — the metadata edge #2 broke
  so `runner → laps → release` does not cycle back into `app`.
- `internal/app` MUST NOT import `internal/cli`, `internal/user_prompt`, or
  `internal/laps` — the presentation-neutral seam from #2 (`app` reaches `laps`
  only transitively through `runner`).
- No `internal/*` package imports `internal/cli`; only `cmd/rally` may.

Per-package allow-lists (each package may import only these internal packages;
matches the current tree):

| Package | May import (internal) |
|---|---|
| `internal/agent` | `agent_prompt`, `reliability`, `textutil` |
| `internal/config` | `agent`, `routing`, `store` |
| `internal/routing` | `agent` |
| `internal/store` | `reliability`, `textutil` |
| `internal/reliability` | `monitor` |
| `internal/laps` | `release` |
| `internal/progress` | `laps`, `store` |
| `internal/telemetry` | `buildinfo` |
| `internal/release` | `buildinfo` |
| `internal/user_prompt/roleloader` | `store` |
| `internal/relay` | `agent`, `store` |
| `internal/relay/runner` | `agent`, `agent_prompt`, `gitx`, `keyboard`, `laps`, `monitor`, `progress`, `relay`, `reliability`, `routing`, `store`, `style`, `telemetry`, `textutil`, `user_prompt/roleloader` |
| `internal/app` | `agent`, `config`, `relay`, `relay/runner`, `routing`, `store`, `telemetry` |
| `internal/cli` | (composition/presentation layer — broad; denied only `nothing above it`) |
| `cmd/rally` | any internal package (process composition root) |

Leaf packages with no internal imports today (`agent_prompt`, `buildinfo`,
`gitx`, `keyboard`, `monitor`, `style`, `textutil`, `user_prompt`, `testutil`)
are encoded as "no internal imports".

`internal/cli` and `cmd/rally` are the two composition/presentation layers and
are intentionally allowed broad internal imports; the discipline on them is the
*reverse* direction — nothing imports `cli`, and `cmd/rally` is imported by
nothing.

Implementation note: express rules as **deny-lists keyed by the architectural
intent** where a tight allow-list would be noisy (e.g. `cli`), and as
allow-lists for the lower packages whose surface should stay small. The harness
allow-lists (`agent`, and later per-harness packages) are the ones #4 will
revisit.

## Decision 5 — dependency-confinement rules (verified)

| Dependency | Confined to |
|---|---|
| `github.com/newrelic/go-agent` | `internal/telemetry` |
| `github.com/pelletier/go-toml` | `internal/config` |
| `github.com/spf13/cobra` | `cmd/rally`, `internal/cli`, `internal/progress` (command-shaped) |
| `github.com/charmbracelet/huh` | `internal/cli`, `internal/user_prompt` (interactive prompt) |
| `github.com/charmbracelet/lipgloss` | `internal/style`, `internal/cli` (styling/presentation) |

`cobra` is currently used in `internal/progress/cli.go` as well as `internal/cli`
— `progress` is command-shaped, so it is included rather than treated as a leak.
Keep the first pass to these obvious owners; do not encode every incidental
import. Test files are checked the same as production for dep confinement (a
New Relic import in a non-telemetry `_test.go` is still a leak), since the
confinement intent is package-level.

## Decision 6 — local + CI wiring

- `just arch-check` → `go run ./tools/archguard` (advisory: warnings + hard).
- `just check` gains `arch-check` as a dependency (after `vet`/format), so a
  local `just check` is a faithful mirror of the CI gate. This folds it in
  immediately rather than keeping it separate for a cycle (the draft's hedge):
  safe because the baseline is green by construction. Alternative (keep separate
  one cycle) is noted but not taken.
- CI: add an `archguard` step to the existing `lint` job running
  `go run ./tools/archguard --ci`. No new job and no new required check beyond
  `lint`; the `lint` job is already a blocking gate (`ci-quality-gates`).

## Diagnostics format

Each hard violation prints: the offending file (repo-relative), the rule, and a
one-line architectural reason. Examples:

```text
internal/relay/relay.go:1: import boundary: imports internal/relay/runner —
  the relay primitives must not depend on the orchestrator; keep the
  runner → relay edge one-way.
internal/agent/foo_test.go:1: dependency confinement: imports
  github.com/newrelic/go-agent — New Relic is owned by internal/telemetry;
  adapters return typed evidence and let relay/runtime emit telemetry.
internal/relay/runner/run_one.go: size: 1620 lines exceeds grandfather cap 1510
  — split before growing this file; ratchet the cap down, never up.
internal/agent/new_big.go: size: 920 lines exceeds the 800-line production
  hard budget — split it or justify a grandfather entry.
```

## Testing strategy

- Unit-test the policy engine against `tools/archguard/testdata` fixtures:
  line counting, generated-file exemption (`// Code generated`), hidden-dir and
  `testdata`/`vendor` skipping, import parsing (aliases, dot-imports, grouped
  blocks), production vs test policy, grandfather cap pass/fail, dependency
  confinement, and `testutil` confinement. Assert the **diagnostic text**, not
  just the boolean, so CI output stays actionable.
- Repository integration: run `archguard --report` against HEAD, paste the
  baseline, then confirm `go run ./tools/archguard --ci` exits 0 on the clean
  tree. Run `just check` and `go test -count=1 ./...`.
- Prove a deliberate fixture failure produces readable output, but leave no
  deliberate failure in the repo.

## Open questions — resolved

- **Location** → `tools/archguard` in the main module (Decision 1).
- **Warnings in CI logs** → CI runs `--ci` (hard-only exit); warnings are local
  advisory via `just arch-check` / `just check` (Decision 2/6).
- **Test budget** → keep the larger 1,800 hard budget for now; revisit after the
  #4 refactor wave (Decision 3).
- **Budgets for Markdown/scripts** → out of scope for v1; Go files only.
