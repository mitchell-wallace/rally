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

**`internal/monitor`** (headline file keeps the lifecycle):

```text
monitor.go          # Monitor, NewMonitor, Start/Stop/Tick, UpdatePIDs, indicator setters
monitor_render.go   # RenderStatus, RenderStatusExt, formatDuration, formatLastActivity, render/clear
proc_stats.go       # GitDirtyCount, LogLastActivity, GetPIDsInGroup, CountTCPConnections, ReadIOBytes, ReadSyscallBytes
network_monitor.go  # NetworkMonitor
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

**`internal/cli`**:

```text
routes_cmd.go       # NewRoutesCmd, runRoutesCheck (Cobra wiring only)
routes_check.go     # CheckRoutes, checkRoles (the check core; keeps the headline name)
routes_render.go    # renderRouteCheckResult and output formatting
routes_validate.go  # reasoning/alias validation helpers
```

**`internal/store`** (headline file keeps the type):

```text
store.go              # Store type, Open/init, layout migration entry points
store_write.go        # append/write paths
store_messages.go     # message read/query paths
store_agent_status.go # agent-status read/write
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
