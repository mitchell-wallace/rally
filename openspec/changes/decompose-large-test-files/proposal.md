## Why

Test files are the largest files in the repo — larger than any production file —
and they are exactly what an agent reads to learn how a subsystem is *supposed*
to behave. A 2026-07-02 scan (commit `f55712c`) shows **eight** `_test.go`
files over the 1,000-line hard budget in `add-architecture-guardrails` (#3),
matching #3's current test grandfather map exactly (the draft's ninth file,
`internal/agent/agent_test.go` 2,812, was already carved up by
**#4 modularize-harness-adapters** — no harness test file is over 1,000 now):

- `internal/relay/runner/run_one_test.go` — 2,355,
- `internal/relay/runner/runner_failure_telemetry_test.go` — 2,331,
- `internal/relay/runner/relay_steps_test.go` — 2,226,
- `internal/config/config_v2_test.go` — 1,801,
- `internal/relay/runner/route_runtime_test.go` — 1,392,
- `internal/store/store_test.go` — 1,112,
- `internal/relay/resilience_test.go` — 1,063,
- `internal/relay/runner/runner_outcome_test.go` — 1,038.

A 2,300-line test file forces an agent to scroll past dozens of unrelated cases
to find the one behaviour it needs. Go lets a package hold arbitrarily many
`_test.go` files, so a long suite is a missed split, not a constraint.

This is change **#8**, deliberately last of the decompositions
(`openspec/next-up.md`) so the test layout can mirror the final post-#5/#6/#7
production layout. It is **pure test reorganization**: no production code, no
behaviour, no version bump, no release.

## What Changes

- Split each of the eight grandfathered `_test.go` files into
  responsibility-named `_test.go` files in the **same** package, mirroring the
  production file layout #6/#7 produce (e.g. runner tests line up with the
  per-phase `run_attempt_*.go` files), preserving every test/benchmark function
  exactly once via a move-and-verify inventory.
- Extract shared per-package fixtures/builders into per-package helper test
  files, extending existing ones (e.g. `internal/relay/runner/helpers_test.go`)
  rather than creating parallel ones.
- No assertion changes — arrangement only, not coverage.
- Ratchet #3's baseline: regenerate the grandfather map so all eight test
  entries are gone; every new `_test.go` file lands under the 1,000-line cap.
- **Scope boundary**: only the eight hard-budget files. Warning-band test files
  (700–1,000 lines, e.g. `runner_timeout_runone_test.go` 945,
  `task_test.go` 764, `reliability/patterns_test.go` 745) stay advisory and
  untouched, except where a mirror split of an owned file naturally absorbs
  cases.

## Capabilities

### New Capabilities

- `test-module-structure`: the test-file structure contract — oversized suites
  split into responsibility-named `_test.go` files that mirror their package's
  production layout, shared setup in per-package helper test files, every
  test/benchmark preserved exactly once, and the no-behaviour-change /
  no-version-bump contract; ratchets the #3 test grandfather map to empty.

### Modified Capabilities

_None — the `architecture-guardrails` spec is unchanged (grandfather-map
regeneration is a policy-baseline update, per the #4/#6 precedent)._

## Impact

- **Code**: `_test.go` files only, across `internal/relay/runner`,
  `internal/relay`, `internal/config`, `internal/store`. Zero production-file
  edits.
- **Behaviour**: none — `go test -count=1 ./...` and
  `go test -race -shuffle=on -count=1 ./...` green with identical assertions;
  a name-level inventory proves every pre-change test/benchmark function
  appears exactly once afterward.
- **Guardrails**: the eight test grandfather entries are removed; no policy-
  table edit.
- **Sequencing**: after #6 (runner production layout) and #7 (config/store
  layout) so the mirror is stable; coordinates with (does not duplicate) any
  test relocation those changes already performed. Before `rename-rally-roles`
  (#9).
- **Out of scope**: production code of any kind; changing what is tested
  (coverage, assertions, fixture meaning); new test frameworks; golden/testdata
  extraction (explicitly deferred as an optional follow-up); warning-band test
  files beyond the scope boundary above.
