# Design — decompose-large-test-files

Pure test reorganization. No production code, no behaviour change, no version
bump, no release. Baselined against commit `f55712c` (2026-07-02); **re-ground
the inventory at implementation time** — this change lands after #5/#6/#7, all
of which may relocate or split tests alongside their production moves.

## Context

Eight `_test.go` files exceed #3's 1,000-line hard budget and constitute the
entire test grandfather map (see proposal). Five are `internal/relay/runner`
suites whose production counterparts #6 splits into per-phase files; the other
three (`config_v2_test.go`, `store_test.go`, `resilience_test.go`) cover stable
packages this change owns directly. #4 already demonstrated the pattern by
carving `agent_test.go` (2,812) into per-package suites, all under the cap.

## Goals / Non-Goals

**Goals:**

- Test file names answer "where is X tested" and line up with the post-#6/#7
  production files one-for-one where practical.
- Shared fixtures/builders live in per-package helper test files; case files
  stay short.
- Every pre-change test/benchmark function survives exactly once
  (move-and-verify inventory); the #3 test grandfather map empties.

**Non-Goals:**

- No assertion, fixture-meaning, or coverage change; no new frameworks.
- No production edits — not even renames (production layout is #6/#7's).
- No golden/testdata extraction (optional follow-up, out of scope).
- No warning-band test splits (700–1,000 lines) beyond natural absorption.

## Decisions

### Decision 1 — one change for all four packages

Resolves the draft's first open question: keep one change (the user's stated
intent), because the contract is identical everywhere and per-package changes
would multiply artifact overhead. Reviews stay small via one-commit-per-suite
(eight commits or fewer, see Migration Plan).

### Decision 2 — mirror the production layout, phase-first for the runner

Split axes (candidate, verify against the post-#6 layout at implementation):

- `run_one_test.go` (2,355) → per-phase files mirroring #6's
  `run_attempt_*.go` split (e.g. `run_attempt_prepare_test.go`,
  `run_attempt_classify_test.go`, `run_retry_decide_test.go`, …), with
  `run_one_test.go` retaining only the top-level `runOne` flow cases.
- `runner_failure_telemetry_test.go` (2,331) → by failure family (harness
  failure evidence, benching/reset, diagnostics/events), aligned with the
  telemetry cases' subjects; names like `runner_failure_telemetry_*_test.go`.
- `relay_steps_test.go` (2,226) → mirroring #6's `relay_route_wait.go` /
  `relay_run_progress.go` / `relay_summary.go` split.
- `route_runtime_test.go` (1,392) → mirroring `route_runtime_construct.go` /
  `route_runtime_select.go` / `route_runtime_recovery.go` /
  `route_runtime_bench.go`.
- `runner_outcome_test.go` (1,038) → by outcome family (classify vs record),
  mirroring `run_attempt_classify.go` / `run_attempt_record.go`.
- `config_v2_test.go` (1,801) → mirroring #2's production split (`load`,
  `decode`, `validate`, `resolve`, `save`, plus #7's providers files where the
  cases belong there).
- `store_test.go` (1,112) → mirroring #7's `store_write.go` /
  `store_read.go` / `store_messages.go` / `store_agent_status.go`.
- `resilience_test.go` (1,063) → by resilience concern using the established
  vocabulary: state machine/persistence vs **stall**/**frozen** transitions vs
  **benched**/probation/decay.

A test that spans multiple production files goes with the behaviour it
primarily pins; do not split a single test function.

### Decision 3 — helper extraction extends existing helper files

`internal/relay/runner/helpers_test.go` (142 lines) already exists; shared
fixtures/builders extracted during the split move there (or into a
`testsupport_test.go` sibling if a package has none), never into new parallel
helper files per case file. Helper moves are mechanical relocations of existing
setup code — no new abstractions, no fixture-meaning changes. Helper files are
themselves subject to the 1,000-line cap.

### Decision 4 — move-and-verify inventory is the acceptance gate

Before: record every `func Test*`/`func Benchmark*`/`func Fuzz*` name per
package (e.g. `grep -hoE '^func (Test|Benchmark|Fuzz)[A-Za-z0-9_]*' | sort`).
After: identical multiset, each name declared exactly once, `go test -count=1
./...` and `go test -race -shuffle=on -count=1 ./...` green. The `-shuffle=on`
run is load-bearing: it catches accidental cross-file ordering dependencies
that a straight move can expose.

### Decision 5 — coordination with #5/#6/#7, not duplication

Where #5/#6/#7 already relocated or split a suite alongside their production
moves, this change's scope shrinks to the remainder — the acceptance criterion
is the empty test grandfather map, not a fixed file list. The runner splits
wait for #6's final production file list (recorded by #6's task 5.5); config/
store splits wait for #7's (task 6.5). `resilience_test.go` has no upstream
dependency and can land first.

### Decision 6 — spec home is a new `test-module-structure` capability

Test-layout discipline is a durable contract future changes should honour, not
a one-off: it gets its own capability rather than a delta to
`relay-module-structure`/`composition-root-structure` (which own production
layout) or `architecture-guardrails` (whose grandfather map is a regenerated
baseline, not a spec surface — #4/#6 precedent).

## Risks / Trade-offs

- **[Risk] A "move" silently drops or duplicates a test** → Decision 4's
  name-level inventory is mandatory per commit, not just at the end.
- **[Risk] Hidden ordering/shared-state coupling between cases breaks under
  relocation** (package-level vars, `TestMain`, file-scoped fixtures) →
  `-shuffle=on -race` per commit; if a case fails under shuffle before the
  move, STOP and report — that is a pre-existing defect, not this change's to
  fix silently.
- **[Risk] #6/#7 layouts drift after this change is planned** → Decision 5
  keys the split to their recorded final file lists at implementation time;
  the mirror axes hold even if names shift.
- **[Risk] Helper extraction changes fixture semantics** (e.g. a builder that
  a case mutated locally) → extract only verbatim-shared setup; leave
  case-local variants inline.
- **Trade-off**: many more test files per package. Accepted — the file name
  index is the point, and it matches how #4 left the harness tests.

## Migration Plan

One commit per source suite (eight or fewer), each: split → helper extraction →
inventory check → full package test run. Order: `resilience_test.go` first (no
upstream), then config/store after #7, then the five runner suites after #6.
Finish by regenerating the archguard baseline (`--report`) — all eight test
entries gone — and running the full suite. Rollback is per-commit `git revert`.

## Open Questions

_None — the draft's two open questions are resolved by Decisions 1 and 2/4
(one change; file-splitting only, golden extraction deferred)._
