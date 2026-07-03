## 1. Baseline and inventory

- [ ] 1.1 Confirm the tree is green: `go build ./...`, `go test -count=1 ./...`, `go test -race -shuffle=on -count=1 ./internal/relay/... ./internal/config ./internal/store`, `go run ./tools/archguard --ci` (exit 0). If a test fails under `-shuffle=on` **before** any move, STOP and report — pre-existing ordering defects are not this change's to fix silently.
- [ ] 1.2 Re-ground the target list: regenerate the test grandfather map (`go run ./tools/archguard --report`) — the scope is every `_test.go` entry in it (eight at baseline), nothing else. Note which suites #5/#6/#7 already split; scope shrinks to the remainder.
- [ ] 1.3 Record the pre-change name-level inventory per target package: `grep -hoE '^func (Test|Benchmark|Fuzz)[A-Za-z0-9_]*' <files> | sort` saved for post-change comparison. Collect #6's recorded runner file list (its task 5.5) and #7's per-package lists (its task 6.5) as the mirror targets.

## 2. Stable-package suites

- [x] 2.1 (No upstream dependency — can land first.) Split `internal/relay/resilience_test.go` (1,063) by resilience concern — state machine/persistence vs stall/frozen transitions vs benched/probation/decay — extracting shared fixtures into a single helper test file for the package. Inventory check + `go test -race -shuffle=on -count=1 ./internal/relay`; commit.
- [x] 2.2 (After #7's config layout is final.) Split `internal/config/config_v2_test.go` (1,801) mirroring the production layout (`load`/`decode`/`validate`/`resolve`/`save` from #2, providers files from #7); shared fixtures into one helper test file. Inventory check + `go test -race -shuffle=on -count=1 ./internal/config`; commit.
- [x] 2.3 (After #7's store layout is final.) Split `internal/store/store_test.go` (1,112) mirroring #7's `store_write.go`/`store_read.go`/`store_messages.go`/`store_agent_status.go`. Inventory check + `go test -race -shuffle=on -count=1 ./internal/store`; commit.

## 3. Runner suites (after #6's layout is final)

- [x] 3.1 Split `internal/relay/runner/run_one_test.go` (2,355) per-phase against #6's `run_attempt_*.go` files, keeping top-level `runOne` flow cases in `run_one_test.go`; move shared fixtures into the existing `helpers_test.go`. Inventory check + `go test -race -shuffle=on -count=1 ./internal/relay/runner`; commit.
- [x] 3.2 Split `internal/relay/runner/runner_outcome_test.go` (1,038) by outcome family mirroring `run_attempt_classify.go`/`run_attempt_record.go`. Inventory check + package tests; commit.
- [x] 3.3 Split `internal/relay/runner/route_runtime_test.go` (1,392) mirroring `route_runtime_construct.go`/`route_runtime_select.go`/`route_runtime_recovery.go`/`route_runtime_bench.go`. Inventory check + package tests; commit.
- [ ] 3.4 Split `internal/relay/runner/relay_steps_test.go` (2,226) mirroring `relay_route_wait.go`/`relay_run_progress.go`/`relay_summary.go` (post-#5 membership). Inventory check + package tests; commit.
- [ ] 3.5 Split `internal/relay/runner/runner_failure_telemetry_test.go` (2,331) by failure family (harness failure evidence vs benching/reset vs diagnostics/events). Inventory check + package tests; commit.

## 4. Ratchet and verification

- [ ] 4.1 Regenerate the archguard baseline (`go run ./tools/archguard --report`): zero `_test.go` grandfather entries remain; every new `_test.go` file (helpers included) is under 1,000 lines; `go run ./tools/archguard --ci` exit 0. No policy-table edit.
- [ ] 4.2 Final inventory comparison per package against task 1.3: identical multisets, each test/benchmark/fuzz function declared exactly once.
- [ ] 4.3 Full verification: `go test -count=1 ./...`, `go test -race -shuffle=on -count=1 ./...`, `go vet ./...`, `gofmt -l .` empty, `just check` green.
- [ ] 4.4 Confirm the diff touches only `_test.go` files plus the regenerated baseline: zero production-file edits, no assertion changes, `internal/buildinfo/VERSION` untouched.
