## Why

Rally still ships production and test files well beyond healthy review size, and
nothing stops the next one from appearing. A 2026-07-01 scan of the current tree
(after **#1 decompose-relay-runner** and **#2 slim-cli-composition-root**
landed) shows the largest production files are:

- `internal/relay/runner/run_one.go` — 1,510 lines,
- `internal/agent/opencode.go` — 801 lines (addressed later by #4),
- `internal/relay/runner/route_runtime.go` — 752 lines,
- `internal/monitor/monitor.go` — 663 lines,
- `internal/config/providers.go` — 621 lines,
- `internal/cli/routes_check.go` — 619 lines.

and the largest tests are larger still (`internal/agent/agent_test.go` 2,812,
`internal/relay/runner/run_one_test.go` 2,355,
`internal/relay/runner/runner_failure_telemetry_test.go` 2,331,
`internal/relay/runner/relay_steps_test.go` 2,238,
`internal/config/config_v2_test.go` 1,801). The 3,782-line `runner.go` that
motivated this sequence is **already gone** — #1 carved it into the
`internal/relay/runner` package — so this change ratchets a tree that two
refactors have already improved; it does not have to absorb the original
outlier.

Just as important, the codebase now has real internal edges worth protecting.
**#1** established the one-way `internal/relay/runner → internal/relay`
dependency (the `relay-module-structure` spec). **#2** established the layered
composition root `cmd/rally → internal/cli → internal/app` and the
presentation-neutral `app.StartRelay` seam, with the explicit rules that
`internal/app` must not import `internal/user_prompt`, `internal/laps`, or
`internal/cli`, and that `internal/release` must not import `internal/app` (the
`composition-root-structure` spec). Today those rules live only as prose and as
one-off `go list` assertions inside two changes' task lists. Before #4 adds
per-harness packages and #5/#7 add presentation surfaces, Rally needs a standing
CI gate that keeps these edges one-way and keeps third-party dependencies
(New Relic, TOML, Cobra, huh, lipgloss) confined to the packages that own them.

This is change **#3** in the architecture sequence (`openspec/next-up.md`). It is
tooling-and-CI only: it adds an architecture checker and wires it into local and
CI flows. It changes **no Rally runtime behaviour**, bumps **no version**, and
cuts **no release**.

## What Changes

The change rolls out in mechanical-first phases so the gate is green by
construction before it is allowed to fail a build.

**Phase 1 — the checker (`tools/archguard`), advisory:**

- Add a small dependency-free Go command, `tools/archguard` (`package main` in
  the existing module — stdlib `go/parser`, `go/token`, `path/filepath` only, so
  `go mod tidy` and `go vet ./...` are unaffected and no third-party dep is
  added).
- It walks the repo, parses each `.go` file with `parser.ImportsOnly`, counts
  physical lines, and applies a policy engine. It skips generated files (those
  beginning with `// Code generated`), `testdata`, `vendor`, build output, and
  hidden bookkeeping dirs (`.git`, `.rally`, `.laps`).
- A `--report` mode prints the current over-budget files so the grandfather map
  can be regenerated against HEAD, and a `--ci` mode exits non-zero only on hard
  violations. Default (local) mode also prints advisory warnings.

**Phase 2 — file-size budgets with a grandfathered baseline:**

- Warning thresholds: 500 lines (production `.go`), 700 lines (`_test.go`).
- Hard-error thresholds for files **not** grandfathered: 800 lines (production),
  1,000 lines (`_test.go`). Generated files are exempt but must carry the
  `// Code generated` marker.
- A grandfather map records every file currently over its hard-error budget at
  its **actual** current line count; the check fails if a grandfathered file
  grows above its recorded cap. The map is **regenerated from HEAD at
  implementation time** via `archguard --report` — the baseline figures in
  `design.md`/`tasks.md` are the 2026-07-01 snapshot, not a hard-code.

**Phase 3 — import-boundary and dependency-confinement rules:**

- Encode the **current** internal import graph as allow/deny rules, flagship
  being `internal/relay` MUST NOT import `internal/relay/runner` (keeps #1's edge
  one-way), plus the composition-root edges from #2: `internal/release` ↛
  `internal/app`; `internal/app` ↛ {`internal/cli`, `internal/user_prompt`,
  `internal/laps`}; and no `internal/*` package imports `internal/cli` (only
  `cmd/rally` does).
- Confine third-party dependencies to their owning packages (verified against the
  current tree): New Relic → `internal/telemetry`; `go-toml` → `internal/config`;
  Cobra → command-shaped packages (`cmd/rally`, `internal/cli`,
  `internal/progress`); huh → interactive-prompt packages (`internal/cli`,
  `internal/user_prompt`); lipgloss/terminal styling → `internal/style`,
  `internal/cli`.
- Confine the test helper: non-test files MUST NOT import `internal/testutil`.

**Phase 4 — local + CI wiring:**

- Add a `just arch-check` recipe (advisory warnings + hard import/size failures)
  and make `just check` depend on it so violations surface locally before a push.
- Add an `archguard` step to the `lint` job in `.github/workflows/test.yml`,
  running `archguard --ci` so CI hard-fails on disallowed imports, new oversize
  files, and grandfathered-file growth — but never on advisory warnings.

Because every rule is generated from the current tree, the baseline is green the
moment it lands: the gate goes straight to enforcing (hard errors) with no
advisory-only grace period needed, while the 500/900 warnings stay informational
forever. As later refactors (#4 splits `opencode.go`, etc.) land, the grandfather
caps ratchet down and eventually disappear.

Each failure message explains the architectural reason, not just the rule name —
e.g. `internal/relay imports internal/relay/runner: the relay primitives must not
depend on the orchestrator; keep the runner → relay edge one-way`.

## Capabilities

### New Capabilities

- `architecture-guardrails`: the `tools/archguard` checker (file-size budgets with
  grandfathered caps, internal import-boundary rules, third-party dependency
  confinement, test-helper confinement, human-readable diagnostics) and its local
  (`just arch-check` / `just check`) and CI (`lint` job) wiring, including the
  tooling-only / no-runtime-change / no-version-bump contract.

### Modified Capabilities

<!-- None. This change adds a CI gate that *enforces* edges already defined by the
     relay-module-structure (#1) and composition-root-structure (#2) specs; it
     does not redefine those requirements, so neither spec is modified. -->

## Impact

- **Code**: new `tools/archguard/` package (policy engine + `main`) plus its unit
  tests and `testdata` fixtures. No change to any `cmd/rally` or `internal/*`
  runtime file.
- **Build/dev flow**: new `just arch-check` recipe; `just check` gains an
  `arch-check` dependency. No change to `go build`, `.goreleaser.yaml`, or the
  released binary — `tools/archguard` is a dev tool, never shipped.
- **CI**: one new step in the existing `lint` job; no new job, no new required
  status check beyond what `lint` already provides. Trigger surface
  (push to `dev`/`main`, PRs) is unchanged.
- **Dependencies**: none added — `archguard` is stdlib-only, so `go.mod`/`go.sum`
  are untouched and the `tidy` gate stays green.
- **Sequencing**: consumes #1's `runner → relay` edge and #2's composition-root
  edges as its flagship rules; hands #4 `modularize-harness-adapters` a place to
  tighten harness-package boundaries (and a cap on `opencode.go` to ratchet away);
  hands #5/#7 a gate that keeps presentation packages from being imported by
  adapters or primitives.
- **Out of scope**: general static analysis (`golangci-lint`) and fuzzing — see
  `adopt-lint-and-fuzz-gates`; refactoring/splitting the large files themselves
  (this change enforces and ratchets policy, the splits are their own changes);
  applying budgets to Markdown/scripts (Go files only for v1).
