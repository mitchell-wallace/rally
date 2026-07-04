## Why

Rally's core vocabulary currently mixes track-and-field terms with the racing
language used by the product, queue, docs, and operator experience. The practical
problem is not metaphor purity; it is ambiguity:

- `runner` means both a harness+model route entry and the relay orchestrator
  package/type (`internal/relay/runner.Runner`).
- `run` is the middle execution entity, a Go method verb (`Run`, `RunE`), common
  English prose, and a prefix in persisted fields and presentation events.
- `try` is already the stable persisted noun, while presentation sometimes says
  "attempt".

This change formalises the locked scheme B from `draft.md`: keep `relay`, rename
the middle execution entity from `run` to `outing`, rename harness+model
`runner` to `driver`, and keep `try`.

## What Changes

- Update terminology docs, README prose, embedded agent prompts, and installed
  `.rally/agents` templates to define the hierarchy as `relay > outing > try`.
- Rename Go identifiers and internal presentation/runtime-event names for the
  execution entity from `Run*` to `Outing*`, where they describe the entity.
- Rename harness+model route-entry vocabulary from `runner` to `driver`.
- Preserve Go-idiom `Run()` methods, cobra `RunE`, and English verb-sense
  "run" prose.
- Preserve `internal/relay/runner` and its `Runner` type as the orchestrator
  package/type. A future `racecontrol` rename is explicitly deferred.
- Add tolerant readers for persisted Rally state so old `run_id` keys are
  accepted while new writes use `outing_id`.
- Keep telemetry custom event names and attributes stable for New Relic
  dashboard continuity.
- Keep CLI commands/flags backwards compatible through aliases where any
  operator-facing flag or command wording changes.

This is a vocabulary-only change. Zero behavior change is intended.

## Capabilities

### Modified Capabilities

- `relay-runner`: execution hierarchy and route-entry naming.
- `run-summary`: local summary/run-state schema compatibility for the renamed
  outing entity.
- `store`: try history and message-consumption persisted fields.
- `runtime-presentation-boundary`: runtime events and presentation-neutral
  payloads for outing headers, footers, and summaries.
- `cli-display`: operator-facing labels and summaries.
- `agent-prompt`: agent-facing terminology and role templates.
- `telemetry`: stable external telemetry names despite internal vocabulary
  changes.

## Impact

- **Code**: mechanical identifier renames across store/progress, relay runner
  internals, runtime events, presentation, CLI display, docs, prompts, and
  tests.
- **Persistence**: new writes use `outing_id` and related outing names; readers
  accept both old and new keys without silently dropping old state.
- **Telemetry**: New Relic custom events and attributes keep existing names
  (`RallyTry`, `RallyRoute`, `RallyDiagnostic`, `RallyFailure`, `run_id`,
  `runner`) to avoid dashboard churn.
- **Out of scope**: laps files/commands, role taxonomy (`rename-rally-roles`),
  renaming `relay`, renaming `try`, and renaming the orchestrator package/type.
