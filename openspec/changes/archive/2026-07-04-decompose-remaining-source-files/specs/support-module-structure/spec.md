## ADDED Requirements

### Requirement: Monitor module structure

`internal/monitor/monitor.go` SHALL be split into responsibility-named files in
the same `package monitor`, separating the monitor lifecycle (the
`Monitor` type, construction, start/stop/tick, PID and indicator updates —
retained in `monitor.go`), status rendering/formatting, process and network
inspection, and the `NetworkMonitor` type. The split SHALL be a file-only
move: no exported identifier SHALL be added, removed, renamed, or have its
signature changed, and rendered status output SHALL be byte-identical.

#### Scenario: Monitor exported surface unchanged across the split

- **WHEN** the exported identifiers of `internal/monitor` are compared before
  and after the change
- **THEN** the sets are identical and differ only in declaring source file

#### Scenario: Rendering and inspection live apart

- **WHEN** `internal/monitor` is read after the change
- **THEN** status rendering/formatting and process/network inspection live in
  separate responsibility-named files, `monitor.go` retains the lifecycle, and
  no `misc`/`helpers` catch-all file exists

### Requirement: Store module structure

`internal/store/store.go` SHALL be split into responsibility-named files in the
same `package store`, separating the `Store` type with open/init/layout
migration (retained in `store.go`), the relay/try write and ID-allocation
paths, the relay/try query paths, the message subsystem, and agent-status
access. The split SHALL be a file-only
move: no exported identifier SHALL be added, removed, renamed, or have its
signature changed, and the persisted store shape SHALL be unchanged.

#### Scenario: Store exported surface and shape unchanged

- **WHEN** the exported identifiers of `internal/store` and the on-disk store
  layout are compared before and after the change
- **THEN** both are identical; only declaring source files differ

#### Scenario: Store behaviour preserved

- **WHEN** `go test -count=1 ./internal/store` runs after the split
- **THEN** it passes with no assertion changes

### Requirement: Support-module splits are behaviour-preserving and release-free

The support-module decomposition SHALL change no runtime behaviour, CLI output,
config schema or semantics, telemetry event/field, or persisted shape, SHALL
NOT bump `internal/buildinfo/VERSION`, and SHALL NOT require a release. It
SHALL NOT change any package import edge, so no `tools/archguard` policy-table
or grandfather edit is required by it.

#### Scenario: No behaviour-surface or policy edits

- **WHEN** the diff of the change is reviewed
- **THEN** it contains only same-package file moves (plus test relocations if
  any), no archguard policy-table or grandfather-map edit, and leaves
  `internal/buildinfo/VERSION` untouched
