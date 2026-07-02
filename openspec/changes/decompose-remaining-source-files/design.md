# Design — decompose-remaining-source-files

Behaviour-preserving refactor of four warning-band production files in four
packages. No observable behaviour, CLI, config, telemetry, or store change; no
version bump; no release. Baselined against commit `f55712c` (2026-07-02);
re-ground `monitor.go` at implementation time because #5
(`separate-runtime-presentation-boundary`) may touch it first.

## Context

Each target file is a small package's whole story crammed into one document:
`monitor.go` (663) mixes status rendering, `/proc`-style process/network
inspection, and the `Monitor`/`NetworkMonitor` lifecycle; `providers.go` (621)
mixes provider parsing, resolution, and wildcard matching; `routes_check.go`
(619) mixes Cobra wiring, the check core, rendering, and reasoning/alias
validation; `store.go` (541) mixes the `Store` type/open/init, appends/writes,
reads/queries, and agent-status. All are under #3's 800-line hard budget —
this is findability polish, not gate-clearing.

## Goals / Non-Goals

**Goals:**

- Each package's directory listing answers "where does X live"; the primary
  type/constructor stays in the headline file an agent opens first.
- Verbatim moves; exported surfaces, error strings, and behaviour identical.
- One package per commit for small reviews.

**Non-Goals:**

- No behaviour/schema/output change; no exported-API change.
- No child packages (file split first; promote only if a clean interface
  emerges later).
- No harness-adapter splits (routed out in the proposal), no runner work (#6),
  no test splits (#8).
- No archguard policy edits (no import edge changes; nothing grandfathered).

## Decisions

### Decision 1 — four packages in one change, one commit per package

Resolves the draft's first open question. Kept as one change: the four splits
share one contract (same-package responsibility-named split, verbatim moves,
behaviour preservation) and none is big enough to justify its own
proposal/design/tasks overhead. The one-commit-per-package rule keeps each
review as small as four separate changes would.

### Decision 2 — file layouts (candidate seams, verify at implementation)

**`internal/monitor`** (headline file keeps the lifecycle). Full name-to-file
inventory of the 34 current functions/methods (2026-07-02; re-verify):

```text
monitor.go          # lifecycle + state: NewMonitor, Start, Stop, Tick, run,
                    # computeIndicators, UpdatePIDs, SetProcessGroupID,
                    # SetStallThreshold, SetRetry, SetStalled, SetStopping,
                    # SetArmed, SetActing, SetRecovered, SetCursorUpLines
monitor_render.go   # RenderStatus, RenderStatusExt, formatDuration,
                    # formatLastActivity, plural, Monitor.render, Monitor.clear
proc_stats.go       # GitDirtyCount, LogLastActivity, GetPIDsInGroup, readPGID,
                    # CountTCPConnections, socketInodesForPIDs, ReadIOBytes,
                    # ReadSyscallBytes
network_monitor.go  # NewNetworkMonitor, NetworkMonitor.evaluate,
                    # NetworkMonitor.Check
```

**`internal/config`** (mirrors #2's `config_v2.go` split convention). Full
name-to-file inventory of the 26 current functions (2026-07-02; re-verify):

```text
providers_parse.go     # raw-TOML shape ↔ ProviderConfig: parseProviders,
                       # parseProviderValue, toModelList, providersToRaw, toAnySlice
providers_resolve.go   # resolution + the index/count surface: resolveProviders,
                       # resolveProviderMembers, resolveProviderSpec,
                       # resolveProviderConcreteSpec, lookupBareModelAlias,
                       # BuildProviderIndex, ProviderMemberCounts, plus the
                       # ordering/label helpers they use (sortedHarnessKeys,
                       # sortedMapKeys, sortResolvedAgents, builtInHarnessNames,
                       # runnerLabel)
providers_wildcard.go  # wildcard matching + expansion: resolveProviderWildcardSpec,
                       # resolveProviderWildcardHarness, providerPrefixWildcard,
                       # providerSuffixWildcard, matchAll, matchPrefix, matchSuffix,
                       # expandProviderHarnessModels, expandProviderModels,
                       # the modelFilter type
```

`providers.go` itself disappears if nothing remains after the three moves
(preferred), or stays only as a thin documented seam — never as a catch-all.

**`internal/cli`**. Full name-to-file inventory of the 23 current
functions/methods (2026-07-02; re-verify):

```text
routes_cmd.go       # Cobra wiring: NewRoutesCmd, runRoutesCheck,
                    # defaultResolveWorkspaceDir
routes_check.go     # check core (keeps the headline name): CheckRoutes,
                    # checkRoles, removedAliasRouteError.Error,
                    # collectActiveAssignees, collectJSONAssignees,
                    # collectNestedAssignees, addAssignee, mergeAssignees,
                    # hasDefaultRoute, sortedRouteNames
routes_render.go    # renderRouteCheckResult, pluralize
routes_validate.go  # validateReasoning, reasoningTokenRecognised,
                    # validateRouteEntry, decorateResolveError,
                    # topAliasSuggestions, aliasCandidates, levenshtein, min
```

**`internal/store`** (headline file keeps the type). Full name-to-file
inventory of the 26 current functions/methods (2026-07-02; re-verify) — note
the relay/try *read* paths get their own file, which the draft's four-way
axis missed:

```text
store.go              # Store type + NewStore (open/init/layout migration)
store_write.go        # relay/try writes + ID allocation: AppendTry,
                      # AppendRelay, UpdateRelay, NextRelayID, NextTryID
store_read.go         # relay/try queries: GetTry, GetRelay, RecentTries,
                      # RecentRelays, AllRelays, AllTries
store_messages.go     # the message subsystem (writes + queries): AddMessage,
                      # UpdateMessage, maybeTruncateMessages, NextMessageID,
                      # GetMessages, PendingMessages, RelayScopedMessages,
                      # EligibleRelayScopedMessages,
                      # ConsumedRunScopedMessageForRun
store_agent_status.go # AppendAgentStatus, ResetAgentStatus,
                      # truncateAgentStatus, GetAgentStatus, AllAgentStatus
```

Exact membership follows the function inventory at implementation time; the
decision that holds is the responsibility axes above. Every symbol keeps
exactly one home; no `misc`/`helpers` catch-all file in any package.

### Decision 3 — harness warning-band files are routed out, with a trigger

`internal/harness/claude/claude.go` (571),
`internal/harness/opencode/opencode_evidence.go` (570), and
`internal/harness/antigravity/antigravity.go` (531) entered the warning band
via #4's deliberate deep-module layout (opencode's evidence file is itself the
product of a responsibility split). Re-splitting them now would churn a
just-reviewed layout. Routed: owned by the harness layer; the trigger to
revisit is any of them approaching the 800-line hard budget or the
`add-new-harness` flow touching them. Alternative considered — fold them into
this change: rejected as churn without findability gain.

### Decision 4 — monitor split defers to #5's boundary

#5 introduces the runtime event/control boundary but deliberately leaves
`internal/monitor` internals untouched: the live status line stays
runner-driven as #5's documented residual (its design Decision 8), with no
monitor interface introduced. This change owns the *file* split inside
`internal/monitor`. Sequencing after #5 means the split carves whatever #5
left, and the `monitor_render.go` file is the natural future home boundary if
rendering later moves to a presentation package (the `build-new-tui` change
owns that replace-don't-adapt decision) — this change does not move it there.

### Decision 5 — spec homes

- `composition-root-structure` already owns config/cli structure: its "Config
  module structure" requirement is MODIFIED (it currently pins `providers.go`
  unchanged), and an ADDED requirement covers the routes-check split.
- `internal/monitor` and `internal/store` have no structural spec; they get a
  new `support-module-structure` capability rather than overloading
  `composition-root-structure` (they are not composition-root layers) or the
  behavioural `store` spec (which owns what the store does, not its file
  layout).

## Risks / Trade-offs

- **[Risk] A "move" quietly edits behaviour** → move functions whole, review
  with `git diff --color-moved`, keep error strings byte-identical, and run
  each package's tests plus `cmd/rally` (CLI output) after each commit.
- **[Risk] `monitor.go` drifted by the time this lands** (#5 touches monitor
  consumption) → task 1.2 re-grounds the inventory; the responsibility axes
  stand.
- **[Risk] Unexported helpers shared across the new files create confusing
  ownership** (e.g. a formatter used by both render and proc paths) → the
  symbol goes in the file matching its primary responsibility; do not create a
  shared `helpers.go`.
- **Trade-off**: `routes_check.go` keeping the check core under its old name
  while wiring moves out means the headline file changes meaning slightly;
  accepted so links/history stay anchored on the core logic.

## Migration Plan

Four independent same-package refactors, one commit each
(monitor → config → cli → store, any order). Each commit leaves the tree
green; rollback is a per-commit `git revert`.

## Open Questions

_None — the draft's two open questions are resolved by Decisions 1 and 3/4
(one change, file splits only, harness files routed out)._

## Re-grounding — post-#5 (tasks 1.1 / 1.2)

Recorded at the implementation-time baseline gate. `#5`
`separate-runtime-presentation-boundary` landed without touching
`internal/monitor` internals (per its design Decision 8), so the only place
drift could hide is monitor *consumption* in the runner — confirmed unchanged
for this change's scope. The live inventories below match Decision 2 exactly;
no head lap is required.

### Baseline gate (task 1.1) — all GREEN

- `go build ./...` — exit 0
- `go vet ./...` — exit 0
- `gofmt -l .` — empty (exit 0)
- `go test -count=1 ./internal/monitor ./internal/config ./internal/cli ./internal/store` — ok (all four packages)
- `go run ./tools/archguard --ci` — exit 0 (the four target files are reported
  as warn-band advisories exactly as expected; nothing is denied or over the
  hard cap)

### Re-grounded line counts (task 1.2)

| file | design Decision 2 funcs | live `grep -c "^func"` | live lines |
| --- | ---: | ---: | ---: |
| `internal/monitor/monitor.go` | 34 | **34** | 663 |
| `internal/config/providers.go` | 26 | **26** | 621 |
| `internal/cli/routes_check.go` | 23 | **23** | 619 |
| `internal/store/store.go` | 26 | **26** | 541 |

Line counts are byte-for-byte the design's values (663 / 621 / 619 / 541); no
file has grown or shrunk since the `f55712c` baseline.

### Drift vs Decision 2 (task 1.2)

**None.** Every responsibility axis in Decision 2 resolves cleanly against the
live `grep -n "^func"` output: each live function maps to exactly one target
file, no function is missing or extra, and no `misc`/`helpers` catch-all is
needed in any package. The per-function mapping is recorded below. No head lap
is required; the split laps (tasks 2–5) can proceed one-for-one against the
inventory.

### Pre-change function inventory (task 1.2) — name → file

Each current function and its planned target home per Decision 2's
responsibility axes. Counts in parens are per target file.

**`internal/monitor`** (34 → 4 files):

- `monitor.go` stays (16): `NewMonitor`, `Monitor.Start`, `Monitor.Stop`,
  `Monitor.Tick`, `Monitor.run`, `Monitor.computeIndicators`, `Monitor.UpdatePIDs`,
  `Monitor.SetProcessGroupID`, `Monitor.SetStallThreshold`, `Monitor.SetRetry`,
  `Monitor.SetStalled`, `Monitor.SetStopping`, `Monitor.SetArmed`,
  `Monitor.SetActing`, `Monitor.SetRecovered`, `Monitor.SetCursorUpLines`.
  Associated decls that stay: `TickInterval`, `Indicators`, `Monitor`.
- `monitor_render.go` (7): `RenderStatus`, `RenderStatusExt`, `formatDuration`,
  `formatLastActivity`, `plural`, `Monitor.render`, `Monitor.clear`.
- `proc_stats.go` (8): `GitDirtyCount`, `LogLastActivity`, `GetPIDsInGroup`,
  `readPGID`, `CountTCPConnections`, `socketInodesForPIDs`, `ReadIOBytes`,
  `ReadSyscallBytes`.
- `network_monitor.go` (3): `NewNetworkMonitor`, `NetworkMonitor.evaluate`,
  `NetworkMonitor.Check`. Associated decl that travels: `NetworkMonitor`.

**`internal/config`** (26 → 3 files; `providers.go` disappears):

- `providers_parse.go` (5): `parseProviders`, `parseProviderValue`,
  `toModelList`, `providersToRaw`, `toAnySlice`. The public
  `ProviderConfig` type is the parsed raw-TOML shape and travels here (same
  package, findability-only; referenced package-wide).
- `providers_resolve.go` (12): `V2Config.resolveProviders`,
  `V2Config.resolveProviderMembers`, `V2Config.resolveProviderSpec`,
  `V2Config.resolveProviderConcreteSpec`, `V2Config.lookupBareModelAlias`,
  `V2Config.BuildProviderIndex`, `V2Config.ProviderMemberCounts`,
  `sortedHarnessKeys`, `sortedMapKeys`, `sortResolvedAgents`,
  `builtInHarnessNames`, `runnerLabel`. Associated decls that travel:
  `providerRunnerKey`, `resolvedProvider`.
- `providers_wildcard.go` (9): `V2Config.resolveProviderWildcardSpec`,
  `V2Config.resolveProviderWildcardHarness`, `providerPrefixWildcard`,
  `providerSuffixWildcard`, `matchAll`, `matchPrefix`, `matchSuffix`,
  `V2Config.expandProviderHarnessModels`, `V2Config.expandProviderModels`.
  Associated decl that travels: `modelFilter`.

**`internal/cli`** (23 → 4 files):

- `routes_cmd.go` (3): `NewRoutesCmd`, `runRoutesCheck`,
  `defaultResolveWorkspaceDir`. Associated decls that travel (Cobra-wiring
  indirection vars): `resolveWorkspaceDir`, `loadConfig`.
- `routes_check.go` stays (10): `CheckRoutes`, `checkRoles`,
  `removedAliasRouteError.Error`, `collectActiveAssignees`,
  `collectJSONAssignees`, `collectNestedAssignees`, `addAssignee`,
  `mergeAssignees`, `hasDefaultRoute`, `sortedRouteNames`. Associated decls
  that stay (result data model + check core): `defaultRouteKey`,
  `removedAliasRouteError`, `RouteCheckResult`, `ProviderSummary`,
  `RoleDiagnostic`, `RoleOverlap`, `RouteSummary`.
- `routes_render.go` (2): `renderRouteCheckResult`, `pluralize`.
- `routes_validate.go` (8): `validateReasoning`, `reasoningTokenRecognised`,
  `validateRouteEntry`, `decorateResolveError`, `topAliasSuggestions`,
  `aliasCandidates`, `levenshtein`, `min`.

**`internal/store`** (26 → 5 files):

- `store.go` stays (1): `NewStore`. Associated decls that stay: the
  `Store` type and the cross-group window-size constants
  (`agentStatusWindowSize`, `messagesWindowSize`) — both are read by the
  messages and agent-status split files, so they stay in the anchor file
  rather than spawning a shared `helpers.go`.
- `store_write.go` (5): `AppendTry`, `AppendRelay`, `UpdateRelay`,
  `NextRelayID`, `NextTryID`.
- `store_read.go` (6): `GetTry`, `GetRelay`, `RecentTries`, `RecentRelays`,
  `AllRelays`, `AllTries`.
- `store_messages.go` (9): `AddMessage`, `UpdateMessage`,
  `maybeTruncateMessages`, `NextMessageID`, `GetMessages`, `PendingMessages`,
  `RelayScopedMessages`, `EligibleRelayScopedMessages`,
  `ConsumedRunScopedMessageForRun`.
- `store_agent_status.go` (5): `AppendAgentStatus`, `ResetAgentStatus`,
  `truncateAgentStatus`, `GetAgentStatus`, `AllAgentStatus`.

Totals reconcile to Decision 2 exactly: monitor 16+7+8+3 = 34, config
5+12+9 = 26, cli 3+10+2+8 = 23, store 1+5+6+9+5 = 26.
