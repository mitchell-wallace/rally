## 1. Baseline and re-grounding

- [x] 1.1 Confirm the tree is green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go test -count=1 ./internal/monitor ./internal/config ./internal/cli ./internal/store`, `go run ./tools/archguard --ci` (exit 0). If red, STOP.
- [x] 1.2 Re-ground the four files (this change runs after #5, which may touch monitor consumption): regenerate line counts and `grep -n "^func"` inventories for `internal/monitor/monitor.go`, `internal/config/providers.go`, `internal/cli/routes_check.go`, `internal/store/store.go`. Record each pre-change function inventory (name → file) for post-split verification.

## 2. Split internal/monitor (one commit)

- [x] 2.1 Split `monitor.go` per design Decision 2's full 34-function inventory: lifecycle + state stay in `monitor.go` (`NewMonitor`, `Start`, `Stop`, `Tick`, `run`, `computeIndicators`, `UpdatePIDs`, `SetProcessGroupID`, `SetStallThreshold`, `SetRetry`, `SetStalled`, `SetStopping`, `SetArmed`, `SetActing`, `SetRecovered`, `SetCursorUpLines`); rendering/formatting → `monitor_render.go` (`RenderStatus`, `RenderStatusExt`, `formatDuration`, `formatLastActivity`, `plural`, `Monitor.render`, `Monitor.clear`); process/network inspection → `proc_stats.go` (`GitDirtyCount`, `LogLastActivity`, `GetPIDsInGroup`, `readPGID`, `CountTCPConnections`, `socketInodesForPIDs`, `ReadIOBytes`, `ReadSyscallBytes`); `network_monitor.go` (`NewNetworkMonitor`, `NetworkMonitor.evaluate`, `NetworkMonitor.Check`). Moves verbatim; no `helpers.go`.
- [x] 2.2 `go test -count=1 ./internal/monitor ./internal/relay/...` green (runner consumes monitor); commit `decompose-remaining-source-files: split internal/monitor`.

## 3. Split internal/config providers (one commit)

- [x] 3.1 Split `providers.go` per design Decision 2's full 26-function inventory: parsing/raw conversion → `providers_parse.go` (`parseProviders`, `parseProviderValue`, `toModelList`, `providersToRaw`, `toAnySlice`); resolution + index/count surface → `providers_resolve.go` (`resolveProviders`, `resolveProviderMembers`, `resolveProviderSpec`, `resolveProviderConcreteSpec`, `lookupBareModelAlias`, `BuildProviderIndex`, `ProviderMemberCounts`, `sortedHarnessKeys`, `sortedMapKeys`, `sortResolvedAgents`, `builtInHarnessNames`, `runnerLabel`); wildcard matching/expansion → `providers_wildcard.go` (`resolveProviderWildcardSpec`, `resolveProviderWildcardHarness`, `providerPrefixWildcard`, `providerSuffixWildcard`, `matchAll`, `matchPrefix`, `matchSuffix`, `expandProviderHarnessModels`, `expandProviderModels`, `modelFilter`). Delete `providers.go` if nothing remains (preferred) — every symbol exactly one home.
- [x] 3.2 `go test -count=1 ./internal/config ./cmd/rally` green; deprecation/validation messages byte-identical; commit.

## 4. Split internal/cli routes-check (one commit)

- [ ] 4.1 Split `routes_check.go` per design Decision 2's full 23-function inventory: Cobra wiring → `routes_cmd.go` (`NewRoutesCmd`, `runRoutesCheck`, `defaultResolveWorkspaceDir`); check core stays in `routes_check.go` (`CheckRoutes`, `checkRoles`, `removedAliasRouteError.Error`, `collectActiveAssignees`, `collectJSONAssignees`, `collectNestedAssignees`, `addAssignee`, `mergeAssignees`, `hasDefaultRoute`, `sortedRouteNames`); rendering → `routes_render.go` (`renderRouteCheckResult`, `pluralize`); reasoning/alias validation → `routes_validate.go` (`validateReasoning`, `reasoningTokenRecognised`, `validateRouteEntry`, `decorateResolveError`, `topAliasSuggestions`, `aliasCandidates`, `levenshtein`, `min`). Cobra imports remain confined to `internal/cli`.
- [ ] 4.2 `go test -count=1 ./internal/cli ./cmd/rally` green; `rally routes check` output byte-identical on a representative config; commit.

## 5. Split internal/store (one commit)

- [ ] 5.1 Split `store.go` per design Decision 2's full 26-function inventory: `Store` type + `NewStore` stay in `store.go`; relay/try writes + ID allocation → `store_write.go` (`AppendTry`, `AppendRelay`, `UpdateRelay`, `NextRelayID`, `NextTryID`); relay/try queries → `store_read.go` (`GetTry`, `GetRelay`, `RecentTries`, `RecentRelays`, `AllRelays`, `AllTries`); message subsystem → `store_messages.go` (`AddMessage`, `UpdateMessage`, `maybeTruncateMessages`, `NextMessageID`, `GetMessages`, `PendingMessages`, `RelayScopedMessages`, `EligibleRelayScopedMessages`, `ConsumedRunScopedMessageForRun`); agent-status → `store_agent_status.go` (`AppendAgentStatus`, `ResetAgentStatus`, `truncateAgentStatus`, `GetAgentStatus`, `AllAgentStatus`). Persisted shape untouched.
- [ ] 5.2 `go test -count=1 ./internal/store ./internal/relay/...` green; commit.

## 6. Verification

- [ ] 6.1 Function-inventory check: every pre-change function from task 1.2 appears exactly once across each package's new files; no new functions introduced.
- [ ] 6.2 Exported-surface check per package (e.g. `go doc ./internal/monitor`, `go doc ./internal/store` diffed pre/post, or the archguard-independent inventory from 1.2): identical sets.
- [ ] 6.3 Full verification: `go test -count=1 ./...`, `go vet ./...`, `gofmt -l .` empty, `go run ./tools/archguard --ci` exit 0 with the four 500-line advisory warnings gone and **no** grandfather-map or policy-table diff, `just check` green.
- [ ] 6.4 Confirm behaviour preservation: no CLI-output, config-schema/semantic, telemetry, store-shape, or error-string diff; `internal/buildinfo/VERSION` untouched; the three harness warning-band files (`claude.go`, `opencode_evidence.go`, `antigravity.go`) untouched per design Decision 3.
- [ ] 6.5 Record the final per-package production file lists so #8 `decompose-large-test-files` can mirror `config_v2_test.go`/`store_test.go` splits against them.
