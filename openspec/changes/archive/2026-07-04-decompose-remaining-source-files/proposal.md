## Why

After the runner (#1/#6), composition root (#2), harness adapters (#4), and
presentation boundary (#5) changes, four production files remain that no other
change owns, each one file doing three or four jobs. A 2026-07-02 scan (commit
`f55712c`, after **#4 modularize-harness-adapters**) confirms the draft's
numbers are current:

- `internal/monitor/monitor.go` — 663 lines (rendering + process/network
  inspection + monitor lifecycle),
- `internal/config/providers.go` — 621 lines (parsing + resolution + wildcard
  matching),
- `internal/cli/routes_check.go` — 619 lines (Cobra wiring + check core +
  rendering + validation),
- `internal/store/store.go` — 541 lines (open/init + writes + reads +
  agent-status).

All four sit in `add-architecture-guardrails` (#3)'s 500-line *advisory
warning* band, under the 800-line hard budget — none is grandfathered. This is
proactive findability polish, worth doing before the warnings become background
noise and before `rename-rally-roles` / `build-new-tui` add more surface to
these same files.

This is change **#7** in the architecture sequence (`openspec/next-up.md`). It
is a **behaviour-preserving refactor**: same-package file splits only, no
runtime, config-schema, telemetry, store, or CLI behaviour change, no version
bump, no release.

## What Changes

- Split each of the four files into responsibility-named files in the **same**
  package behind its existing entry point, mirroring #2's `config_v2.go` split:
  - `monitor.go` → status rendering/formatting vs process & network inspection
    vs the monitor lifecycle types,
  - `providers.go` → provider parsing vs resolution vs wildcard matching,
  - `routes_check.go` → Cobra command wiring vs check core vs rendering vs
    reasoning/alias validation,
  - `store.go` → `Store` type + open/init vs append/write vs read/query vs
    agent-status.
- No exported API change in any of the four packages; functions move verbatim.
- Land one package's split per commit so each review stays small.
- **Explicitly out of scope**: the three warning-band harness adapter files #4
  created (`internal/harness/claude/claude.go` 571,
  `internal/harness/opencode/opencode_evidence.go` 570,
  `internal/harness/antigravity/antigravity.go` 531). Those layouts were
  deliberately shaped by #4 weeks ago as deep modules owning their parsing
  complexity; re-splitting them immediately is churn without findability gain.
  They stay advisory-band, owned by the harness layer, revisited only if they
  grow toward the hard budget.

## Capabilities

### New Capabilities

- `support-module-structure`: the file-structure contract for the runtime
  support packages this change decomposes — `internal/monitor` and
  `internal/store` split into responsibility-named same-package files behind
  their primary types, with verbatim moves, unchanged exported surfaces, and
  the behaviour-preservation / no-version-bump contract.

### Modified Capabilities

- `composition-root-structure`: the "Config module structure" requirement
  currently pins `providers.go` as *unchanged*; it is updated to cover the
  providers split (parse / resolve / wildcard files). A new requirement covers
  the routes-check command decomposition inside `internal/cli` (command wiring
  vs check core vs rendering vs validation).

## Impact

- **Code**: `internal/monitor`, `internal/config`, `internal/cli`,
  `internal/store` — file splits within each package; every symbol keeps one
  home; no catch-all files. No import edges change, so no `tools/archguard`
  policy-table edit is needed; the advisory warnings for these four files
  disappear.
- **Behaviour**: none. Exported surfaces, error strings, CLI output, config
  schema/semantics, telemetry, and store shape unchanged;
  `internal/buildinfo/VERSION` untouched; no release.
- **Guardrails**: no grandfather entries involved (all four files are under the
  hard budget). Third-party confinement is preserved by construction: Cobra
  stays under `internal/cli`, `go-toml` under `internal/config`.
- **Tests**: `go test -count=1 ./internal/monitor ./internal/config
  ./internal/store ./internal/cli` stays green with no test edits; test-file
  decomposition remains owned by #8 (`config_v2_test.go` and `store_test.go`
  are #8's, and the new production file names give #8 its mirror).
- **Sequencing**: after #5 (`separate-runtime-presentation-boundary`). #5
  leaves `internal/monitor` internals untouched and keeps the live status line
  runner-driven (its documented residual); this change re-grounds and splits
  the `monitor.go` file after #5 lands. Independent of #6 (different
  packages, can run in parallel). Before `rename-rally-roles` (#9).
- **Out of scope**: the runner core (#6), harness adapter files (routed above),
  test files (#8), any behaviour/schema/output change, and promoting any of the
  four splits to child packages (file split first, per the carried-over
  deep-module principle).
