# Design

## Context

The scheme is locked to scheme B from `draft.md`:

- Hierarchy: `relay > outing > try`
- Harness+model route entry: `driver`
- Keep: `relay`
- Keep: `try`
- Keep: `internal/relay/runner` and `runner.Runner` as the orchestrator names

This is vocabulary-only. The migration must not change scheduling, retry,
fallback, persistence semantics, telemetry emission decisions, or CLI behavior.

The inventory below was grounded in the current working tree with tracked-file
grep. Some TUI files are in flux from parallel work; tracked packages currently
include `tuicore`, `tuipanels`, `tuisafe`, and `tuitabs`, while the lap context
states that `tuitabs` and `tuicore` are the surviving TUI packages. Treat
prototype presentation packages as volatile and re-inventory immediately before
implementation.

## Rename Surface Inventory

Counts were computed over existing tracked files only, excluding missing
tracked paths in the dirty worktree:

| Surface | Count |
| --- | ---: |
| files containing `run_id` | 41 |
| files containing `RunID` | 54 |
| files containing `RunEntry` | 26 |
| files containing `TryRecord` | 55 |
| files containing `RunHeaderReady` | 24 |
| files containing footer event names (`AttemptFinished`, `AttemptCancelled`, `HandoffAttemptFinished`, `RetryFooterUpdated`) | 27 |
| files containing operator run labels / summaries (`run:`, `run %d`, `%d run`, `RenderSummary`) | 63 |
| files containing lowercase word `runner` | 321 |
| files containing identifier `Runner` | 85 |
| files containing New Relic event names (`RallyTry`, `RallyRoute`, `RallyDiagnostic`, `RallyFailure`) | 37 |
| files containing telemetry runner tag helpers/keys | 24 |
| agent-prompt/template files mentioning run/runner/try/attempt | 13 |
| top-level docs files mentioning run/runner/try/attempt | 3 |

Important current examples:

- `internal/store/records.go`: `TryRecord.RunID json:"run_id"`,
  `MessageRecord.ConsumedByRunID json:"consumed_by_run_id"`, and relay records.
- `internal/progress/runstate.go`: `RunState.RunID json:"run_id"`,
  `ActiveRunID json:"active_run_id"`, `ActiveTryMetadata.RunID`.
- `internal/progress/store.go`: `RunEntry.RunID json:"run_id"`.
- `internal/relay/runner/runtimeevent/events.go`: `RunHeaderReady`,
  `RunIndex`, `TotalRuns`, `RelayCompleted.TotalRuns`,
  `RelaySummaryReady.TotalRuns`, and footer comments using run/attempt language.
- `internal/style/style.go`: `HeaderOptions.RunIndex`, `RenderHeader` label
  `run: X/Y`, `RenderSummary` text `N runs`, and footer try/attempt wording.
- `internal/cli/tui_view.go`: synthetic replay groups tries by `RunID` and
  emits `RunHeaderReady`.
- `internal/cli/tui2.go`: TUI feed seed maps `progress.RunEntry` by `RunID`,
  falls back to title `run %d`, and exposes `RunIndex` to `tuicore`.
- `internal/telemetry/tags.go` and `internal/telemetry/attributes.go`:
  `EventInfo.RunID`, `run_id`, `RunnerLabel`, `runner` tag, and priority keys.
- `internal/relay/runner/telemetry.go`: helper names and comments such as
  `runnerLimitCategory`, `resolvedRunnerModel`, and "failing runner".
- Docs/prompts/templates: `AGENTS.md`, `README.md`, `.rally/README.md`,
  `.rally/agents/*`, and `internal/agent_prompt/**`.

## Disambiguation Rules

| Occurrence | Sense | Action |
| --- | --- | --- |
| `internal/relay/runner` package | Orchestrator implementation package | Keep |
| `runner.Runner` type and methods | Orchestrator engine | Keep |
| Method names like `Run`, `RunDemo`, cobra `RunE` | Go/API verb convention | Keep |
| English verb "run" | Verb prose | Keep unless it names the entity |
| `RunID`, `RunEntry`, `RunState`, `RunHeaderReady`, `RunIndex`, `TotalRuns` | Middle execution entity | Rename to outing forms |
| `run_id`, `active_run_id`, `consumed_by_run_id` in Rally persisted state | Middle execution entity | New writes use outing keys; readers accept old keys |
| `runner` meaning harness+model selected from routes | Driver | Rename to driver in docs, prompts, config prose, helper names, and internal identifiers |
| Telemetry `runner` tag | External New Relic attribute for harness+model | Keep external key; internal helper names may say driver |
| Telemetry `run_id` tag | External New Relic correlation attribute | Keep |
| `.laps/` files and laps CLI vocabulary | Laps-owned backend/tool surface | Out of scope |

Ambiguous code sites must be classified before editing. In particular,
`internal/relay/runner` files contain both orchestrator `Runner` references that
stay and execution-entity `run*` references that move to outing.

## Migration Design

### Tier 1: Docs and Prompts

Update human-facing vocabulary first:

- `AGENTS.md` terminology becomes `relay > outing > try`, and `Runner` becomes
  `Driver` for the harness+model sense.
- README and `.rally/README.md` use "driver" for route entries and "outing" for
  one driver assigned to one lap.
- `internal/agent_prompt/general/*`, `internal/agent_prompt/roles/*`, and
  `.rally/agents/*` are updated where they refer to the execution entity or
  harness+model route entry.
- Keep roles (`junior`, `senior`, `ui`, `verify`, and any pending role names)
  unchanged. `rename-rally-roles` owns role taxonomy and should not be mixed
  with this vocabulary migration.

### Tier 2: Internal Go Identifiers

Rename entity-bearing identifiers in reviewable chunks:

1. Store/progress/persistence structs:
   - `TryRecord.RunID` -> `OutingID`
   - `MessageRecord.ConsumedByRunID` -> `ConsumedByOutingID`
   - `RunState` -> `OutingState` only if the file/package usage remains clear;
     otherwise keep file names and rename fields first.
   - `RunEntry` -> `OutingEntry`
   - `ActiveRunID` / `ActiveTryMetadata.RunID` -> outing names.
2. Runtime event and presentation payloads:
   - `RunHeaderReady` -> `OutingHeaderReady`
   - `KindRunHeaderReady` -> `KindOutingHeaderReady`
   - `RunIndex` -> `OutingIndex`, `TotalRuns` -> `TotalOutings`
   - relay summary payload totals/outcome counts become outing counts.
3. Runner internals and telemetry labels:
   - Rename harness+model helper names from runner to driver where not external.
   - Keep `Runner` receiver names and package path.
   - Prefer local variables such as `driver`, `pickedDriver`,
     `resolvedDriverModel` when they represent `harnessapi.ResolvedAgent`.
4. CLI prose:
   - `run: X/Y` -> `outing: X/Y`
   - `N runs` -> `N outings`
   - TUI feed fallback `run %d` -> `outing %d`.

Tests should move with each chunk and assert exact strings for compatibility
where operator output changes intentionally.

### Tier 3: Persisted State

Rally state is committed into user repos, so there must never be a silent format
break. New writes use new keys; readers accept old and new keys.

Exact JSON handling:

- For integer outing identifiers, custom `UnmarshalJSON` should accept both
  `outing_id` and `run_id`. If both are present and equal, accept. If both are
  present and differ, return a parse error that names both keys. If only the old
  key is present, populate the new field.
- For string outing identifiers in summary/run-state records, apply the same
  rule for `outing_id` and `run_id`.
- For active metadata, accept `active_outing_id` and `active_run_id` with the
  same conflict rule.
- For message consumption, accept `consumed_by_outing_id` and
  `consumed_by_run_id` with the same conflict rule.
- `MarshalJSON` or struct tags for new structs should write only the new key,
  except telemetry payloads described below.
- Add focused tests for old-only, new-only, equal-both, and conflicting-both
  JSON for every persisted record type.

Affected local files and records:

- `.rally/tries.jsonl`: `TryRecord.run_id` becomes `outing_id` on write; old
  `run_id` reads remain supported.
- `.rally/summary.jsonl`: `RunEntry.run_id` becomes `outing_id` on write; old
  `run_id` reads remain supported.
- `.rally/state/run-state.json`: `run_id` and `active_run_id` become
  `outing_id` and `active_outing_id` on write; old keys read.
- Message records: `consumed_by_run_id` becomes `consumed_by_outing_id` on
  write; old keys read.

`.laps/` files and laps tool vocabulary are out of scope.

### Tier 4: External Names and Aliases

Telemetry custom event names and attribute keys keep their current names:

- Custom events stay `RallyTry`, `RallyRoute`, `RallyDiagnostic`, and
  `RallyFailure`.
- Attributes stay `relay_id`, `run_id`, `try_id`, `runner`, `from_runner`, and
  `to_runner`.
- The `runner` telemetry value continues to mean harness+model and continues to
  include resolved model when available.

Rejected alternative: dual-emit new telemetry keys (`outing_id`, `driver`,
`from_driver`, `to_driver`) alongside old keys. Dual-emitting increases
attribute budget pressure, fragments dashboards during the migration window,
and does not improve local comprehension because telemetry is an external
continuity surface. Keep stable external names and document the historical
telemetry vocabulary instead.

CLI commands and flags keep aliases. The locked scheme keeps `relay`, so no
relay command alias is needed for this change. Any future flag/prose change
from `runner` to `driver` must keep the old flag as an alias where scripts may
use it.

## Sequencing and Coordination

- Sequence after the current TUI package churn lands or rebase the presentation
  chunk immediately before implementation.
- Coordinate with `rename-rally-roles`: that change owns role taxonomy. This
  change must not rename roles or role assignees.
- Keep the `racecontrol` idea deferred. The win here is freeing `runner` for the
  orchestrator by renaming the harness+model sense to driver.

## Verification

- `go test ./...`
- `go test -race -shuffle=on -count=1 ./internal/relay/runner`
- `go vet ./...`
- `go run ./tools/archguard --ci`
- `openspec validate adopt-racing-terminology`
- Live smoke: initialize or use a throwaway workspace, run a tiny relay through
  a fixture/generic driver, confirm output says `outing`, persisted files write
  new keys, old-key fixtures still load, and telemetry tests still assert
  stable New Relic names.
