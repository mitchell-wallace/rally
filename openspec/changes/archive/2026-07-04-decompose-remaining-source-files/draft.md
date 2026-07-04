## Draft: Decompose the remaining oversized source files

Status: drafted 2026-07-01. Behaviour-preserving refactor of production files that
sit in the file-size *warning* band and mix several responsibilities. No runtime,
config-schema, telemetry, store, or CLI behaviour change; no version bump.

## Why

After the runner (#1/#6), composition root (#2), harness adapters (#4), and
presentation boundary (#5) are handled, a handful of production files remain that
are individually navigable-but-lumpy — each is one file doing three or four jobs.
A 2026-07-01 snapshot (regenerate at implementation time):

- `internal/monitor/monitor.go` — 663 lines,
- `internal/config/providers.go` — 621 lines,
- `internal/cli/routes_check.go` — 619 lines,
- `internal/store/store.go` — 541 lines.

These are all **under** the 800-line production hard budget in
`add-architecture-guardrails` (#3), so none is grandfathered — they trip the
500-line *advisory warning*, not the CI gate. This change is therefore
proactive-polish, not gate-clearing: the win is agent findability, not a red
build. It is worth doing before #3's warnings become background noise, and before
`rename-rally-roles`/`build-new-tui` add more surface to these same files.

## Philosophy: deep modules, clean entry points, progressive disclosure

Each target file is a small package's whole story crammed into one document.
Split each into responsibility-named files in the **same** package so the
directory listing answers "where does X live" and each file exposes a shallow,
scannable surface over its own deeper implementation:

- The package's primary type/constructor stays in the headline file (the entry
  point an agent opens first).
- Distinct concerns — rendering vs. system inspection, parsing vs. resolution,
  command wiring vs. core logic vs. output — move to their own named files.
- No exported API changes; this mirrors #2's `config_v2.go` split exactly.

## Intent (candidate seams, grounded 2026-07-01; verify at implementation)

- `monitor.go` → rendering/formatting (`RenderStatus*`, duration/activity
  formatting) vs. process & network inspection (`GitDirtyCount`, PID/TCP/IO/
  syscall readers) vs. the `NetworkMonitor` type. e.g. `monitor_render.go`,
  `proc_stats.go`, `network_monitor.go`.
- `providers.go` → provider parsing (`parseProviders`, `parseProviderValue`,
  `toModelList`) vs. resolution (`resolveProviders`/members/spec) vs. wildcard
  matching (`provider*Wildcard`, `modelFilter`). e.g. `providers_parse.go`,
  `providers_resolve.go`, `providers_wildcard.go`.
- `routes_check.go` → Cobra command wiring (`NewRoutesCmd`, `runRoutesCheck`) vs.
  the check core (`CheckRoutes`, `checkRoles`) vs. rendering
  (`renderRouteCheckResult`) vs. reasoning/alias validation. e.g. `routes_cmd.go`,
  `routes_check.go` (core), `routes_render.go`, `routes_validate.go`.
- `store.go` → the `Store` type + open/init vs. append/write vs. message
  read/query vs. agent-status. e.g. `store.go` (type+open), `store_write.go`,
  `store_messages.go`, `store_agent_status.go`.

## Testing / behaviour preservation

- File-move only within each package; exported surfaces and error strings
  unchanged; `add-architecture-guardrails` (#3) import-boundary and
  dependency-confinement rules preserved (e.g. Cobra stays under `internal/cli`,
  `go-toml` under `internal/config`).
- `go test -count=1 ./internal/monitor ./internal/config ./internal/store
  ./internal/cli` stays green with only test relocations.
- Prefer to land each package's split as its own commit so review stays small.

## Sequencing

- After #3 (so the warnings that motivate this are visible) and after #4/#5/#6
  (so those packages' own outliers are already handled and this change is a clean
  mop-up of the remainder). Before `rename-rally-roles` (#9).
- Independent of the runner work; can proceed in parallel with #6 since the files
  are in different packages.

## Open questions

- Should this be one change covering all four files, or one change per package?
  Kept as one draft for planning; may split at proposal time if the diffs are
  large. (Tests for these files are owned by `decompose-large-test-files` #8.)
- Is any of these better served by a child package rather than a file split
  (e.g. `internal/monitor/proc`)? Prefer file split first; promote only if a
  clean interface emerges.

## Out of scope

- The runner orchestration core (`decompose-run-one` #6) and harness adapters
  (`modularize-harness-adapters` #4).
- Test-file decomposition (`decompose-large-test-files` #8).
- Any behaviour, schema, or output change.
