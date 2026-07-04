## Draft: Decompose the oversized test files

Status: drafted 2026-07-01. Pure test reorganization: no production code, no
runtime/telemetry/store/CLI behaviour, and no version change. Splits large
`_test.go` files across the tree so tests are as navigable as the code they
cover.

## Why

Test files are the largest files in the repo — larger than any production file —
and they are exactly what an agent reads to learn how a subsystem is *supposed*
to behave. A 2026-07-01 snapshot of `_test.go` over the 1,000-line hard budget in
`add-architecture-guardrails` (#3) (regenerate at implementation time):

- `internal/agent/agent_test.go` — 2,812,
- `internal/relay/runner/run_one_test.go` — 2,355,
- `internal/relay/runner/runner_failure_telemetry_test.go` — 2,331,
- `internal/relay/runner/relay_steps_test.go` — 2,238,
- `internal/config/config_v2_test.go` — 1,801,
- `internal/relay/runner/route_runtime_test.go` — 1,392,
- `internal/store/store_test.go` — 1,112,
- `internal/relay/resilience_test.go` — 1,063,
- `internal/relay/runner/runner_outcome_test.go` — 1,038.

These nine are the test grandfather set #3 records. A 2,800-line test file forces
an agent to scroll past dozens of unrelated cases to find the one behaviour it
needs to understand or extend. There is no technical reason for the length: Go
lets a package hold arbitrarily many `_test.go` files, so a long suite is a
missed split, not a constraint.

## Philosophy: deep modules, clean entry points, progressive disclosure

Tests should follow the same progressive-disclosure discipline as production
code:

- **File names as an index.** Split each suite by the behaviour or production
  unit it exercises, so the file name answers "where is X tested" before the file
  is opened (e.g. `run_one_retry_test.go`, `run_one_classify_test.go`).
- **Test structure mirrors production structure.** After #4/#6/#7 give each
  package responsibility-named source files, the test files should line up with
  them one-for-one where practical — the cleanest map for an exploring agent.
- **Shared setup behind a named helper file.** Fixtures, builders, and
  table-driven scaffolding move to a per-package `*_helpers_test.go` /
  `testsupport_test.go` so individual case files stay short and readable.

## Intent

- Split each oversized `_test.go` into responsibility-named `_test.go` files in
  the **same** package, preserving every test/benchmark function exactly once (a
  move-and-verify inventory, like #1's test relocation).
- Extract shared per-package test helpers/fixtures into a dedicated helper file.
- No assertion changes beyond mechanical relocation; the point is arrangement,
  not coverage.

## Sequencing & coordination

This is deliberately the *last* of the decomposition changes so it can mirror the
final production layout:

- **Coordinate with, don't duplicate, #4 and #6.** `modularize-harness-adapters`
  (#4) may move `agent_test.go`'s per-harness cases next to the adapter
  subpackages, and `decompose-run-one` (#6) restructures the runner source. Where
  those changes split their own tests alongside the code, this change's scope
  shrinks to the remainder.
- **Directly owns** the test files whose production code is otherwise stable:
  `config_v2_test.go` (config), `store_test.go` (store), `resilience_test.go`
  (relay primitives) — and any runner/agent test files not absorbed by #4/#6.
- Land after #4/#6/#7; before `rename-rally-roles` (#9).

## Testing / behaviour preservation

- `go test -count=1 ./...` and `go test -race -shuffle=on -count=1 ./...` stay
  green with only relocated tests; a name-level inventory confirms every
  pre-change test function appears exactly once afterward.
- After landing, remove or ratchet down the corresponding test grandfather caps
  in `add-architecture-guardrails` (#3).

## Open questions

- One change for all packages, or per-package? Kept as one draft (the user's
  intent); may split at proposal time if the inventory is large.
- Do any packages want a `testdata`-driven golden approach to shrink table
  literals, or is file-splitting alone enough? Prefer file-splitting first; treat
  golden extraction as a separate, optional follow-up.

## Out of scope

- Any production-code change (including the source splits owned by #4/#6/#7).
- Changing what is tested (coverage, assertions, fixtures' meaning) — this is
  arrangement only.
- New test frameworks or helpers beyond extracting existing shared setup.
