## 1. Tier 1 - Docs and Prompts

- [ ] 1.1 Update `AGENTS.md` terminology to `relay > outing > try`, define driver as harness+model, and explicitly keep `internal/relay/runner.Runner` as the orchestrator.
- [ ] 1.2 Update `README.md` and `.rally/README.md` operator/config prose from entity `run` to `outing` and harness+model `runner` to `driver`.
- [ ] 1.3 Update `internal/agent_prompt/general/*`, `internal/agent_prompt/roles/*`, and generated `.rally/agents/*` templates for outing/driver vocabulary.
- [ ] 1.4 Verify docs/prompts keep roles out of scope and do not preempt `rename-rally-roles`.

## 2. Tier 2 - Go Identifiers and Operator Prose

- [ ] 2.1 Store+persistence chunk: rename entity-bearing store/progress identifiers (`RunID`, `RunEntry`, active run metadata, message consumption run fields) to outing forms, keeping behavior unchanged.
- [ ] 2.2 Runtimeevent+presentation chunk: rename `RunHeaderReady` family and payload fields (`RunIndex`, `TotalRuns`) to outing forms; update terminal and surviving TUI adapters.
- [ ] 2.3 Runner internals+telemetry-label chunk: rename harness+model internal variables/helpers from runner to driver where they are not the orchestrator package/type or external telemetry keys.
- [ ] 2.4 CLI prose chunk: update headers (`run: X/Y`), summaries (`N runs`), TUI fallback titles (`run %d`), and exact string tests to outing wording.
- [ ] 2.5 Re-inventory dirty TUI packages before editing and apply durable renames only to surviving presentation packages (`tuitabs`/`tuicore` per lap context).

## 3. Tier 3 - Tolerant Persisted-State Readers

- [ ] 3.1 Add custom JSON handling for `.rally/tries.jsonl`: write `outing_id`; read old `run_id`, new `outing_id`, equal-both, and reject conflicting both.
- [ ] 3.2 Add custom JSON handling for `.rally/summary.jsonl`: write `outing_id`; read old `run_id`, new `outing_id`, equal-both, and reject conflicting both.
- [ ] 3.3 Add custom JSON handling for `.rally/state/run-state.json`: write `outing_id` and `active_outing_id`; read old `run_id` and `active_run_id`; reject conflicts.
- [ ] 3.4 Add custom JSON handling for message records: write `consumed_by_outing_id`; read old `consumed_by_run_id`; reject conflicts.
- [ ] 3.5 Add regression fixtures/tests proving old checked-in Rally state loads and new state writes only new keys.

## 4. Tier 4 - Alias and External Compatibility Surface

- [ ] 4.1 Keep New Relic custom event names unchanged: `RallyTry`, `RallyRoute`, `RallyDiagnostic`, `RallyFailure`.
- [ ] 4.2 Keep telemetry attribute names unchanged: `run_id`, `runner`, `from_runner`, `to_runner`, and related dashboard keys.
- [ ] 4.3 Add/adjust tests asserting telemetry does not dual-emit outing/driver keys or renamed custom events.
- [ ] 4.4 Keep any changed CLI flags/commands backwards compatible through aliases; document alias/deprecation behavior where applicable.
- [ ] 4.5 Confirm `.laps/` files and laps CLI vocabulary are untouched.

## 5. Verification

- [ ] 5.1 Run `gofmt` on changed Go files.
- [ ] 5.2 Run `go test ./...`.
- [ ] 5.3 Run `go test -race -shuffle=on -count=1 ./internal/relay/runner`.
- [ ] 5.4 Run `go vet ./...`.
- [ ] 5.5 Run `go run ./tools/archguard --ci`.
- [ ] 5.6 Run `openspec validate adopt-racing-terminology`.
- [ ] 5.7 Run a live smoke with a throwaway workspace/fixture driver: confirm operator output says `outing`, new persisted files write outing keys, old-key fixtures still read, and telemetry tests preserve New Relic names.
