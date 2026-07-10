## Context

The selected `internal/presentation/tuitabs` implementation already provides a
full-screen dashboard, transcript, agent-status, and laps queue over
presentation-neutral view models and callbacks. Configuration is currently
editable only through the linear `rally config` form. Runtime relay startup
loads an effective v2 config by layering the machine file under the repo file,
then constructs immutable route, reasoning, and provider indexes for that
relay.

The existing `config.SaveV2File` validates and marshals a complete `V2Config`.
That is appropriate for initialization and the existing form, but it
normalizes the whole document and drops comments. TUI edits need a narrower
persistence path. Archguard also prohibits the presentation packages from
importing config, store, harness, or runner internals.

## Goals / Non-Goals

**Goals:**

- Make ordered routes editable for every built-in role and configured custom
  role, including adding, removing, and reordering configured runner
  shorthands.
- Make per-role reasoning effort and provider disabled state visible and
  editable from the TUI.
- Persist each confirmed edit to the machine config and validate the resulting
  document through the existing v2 decoder/resolver.
- Preserve unrelated TOML byte-for-byte and retain comments around edited
  keys wherever TOML structure permits.
- Keep presentation dependencies within the existing archguard seam.

**Non-Goals:**

- Editing repo-local overrides, harness definitions, provider membership, or
  other `rally config` fields.
- Dynamically rebuilding route/provider indexes inside an already-running
  relay. Existing relay startup semantics remain unchanged.
- Replacing the monitor frame-capture bridge or changing relay start flow.

## Decisions

### 1. The Config tab writes machine scope

Routes, reasoning, and providers are shared runner policy, so this surface
reads and writes `~/.config/rally/config.toml` (honouring
`XDG_CONFIG_HOME`). The tab labels the exact path. Repo `.rally/config.toml`
continues to override machine values at relay load time; it is not mutated.

This chooses the proposal Update Notes' machine-oriented configuration model
over origin-per-key editing. Origin tracking was considered, but mixing scopes
inside one compact editor makes persistence hard to predict and conflicts with
the repository convention that `.rally/config.toml` is normally comments-only.

### 2. Config owns targeted TOML mutations; app owns the use-case service

The config package gains validated, atomic operations for one route, one
reasoning value, or one provider disabled flag. Each operation loads the
current machine file afresh, mutates a typed copy, validates it with existing
v2 rules, patches only the corresponding TOML key/table, decodes the exact
result, and atomically renames it into place.

An app-layer service exposes presentation-neutral snapshots and mutations. It
adds built-in role metadata, configured shorthand choices, and provider member
counts. The CLI maps these app values to `tuicore` DTOs/callbacks. The TUI
therefore remains unaware of config paths, parsers, and runtime routing types.

Passing the full config into presentation or importing config directly was
rejected because it breaches the established dependency boundary. Reusing
`SaveV2File` was rejected because it would silently erase comments and
reformat unrelated settings.

### 3. Mutations are immediate, asynchronous, and confirmation-safe

Every completed add/remove/reorder, effort selection, or provider toggle is
sent as one async mutation. The UI shows saving/error feedback and replaces its
snapshot with the service response only after persistence succeeds.

Destructive route removal and provider toggles use a modal confirmation state
that captures the exact target. While armed, navigation and unrelated command
keys are consumed; they cannot leak to the underlying list or retarget the
second press. Escape cancels. This directly addresses the known raw-key failure
mode in terminal UIs.

### 4. The route picker uses configured shorthands

Choices include built-in harness shorthands, configured custom harness names,
configured `harness:model-alias` pairs, and existing route entries. The editor
does not accept arbitrary route text. Custom role names are entered through a
small text-input mode and then use the same picker.

Reasoning effort uses an explicit default/low/medium/high/xhigh picker. A
pre-existing nonstandard reasoning value remains visible and selectable so the
TUI never destroys a value merely by opening the editor.

### 5. Changes affect subsequent relay construction

`app.StartRelay` already constructs route specs, reasoning resolution, and the
provider index from the config passed at startup. A TUI mutation therefore
takes effect for relays started after config is reloaded, exactly like a file
edit. The current runner has no supported dynamic index replacement seam, so
this increment does not pretend that an in-flight relay is reconfigured.

## Risks / Trade-offs

- [Repo overrides can mask a machine edit in one repository] -> Label the
  scope and path in the tab and document that effective config still honours
  repo overrides.
- [Targeted line editing must handle TOML quoting and multiline values] ->
  locate tables/keys through small TOML probe decodes, validate the final
  document, and cover comments, quoted keys, multiline arrays, and concise
  provider conversion in tests.
- [A concise provider array cannot carry `disabled`] -> Convert only that
  provider to `[providers.<name>]` table form, leaving all other provider and
  config text untouched.
- [Immediate writes can leave several intentional edits as several filesystem
  operations] -> Serialize commands in the Bubble Tea update loop and reload
  the file for every mutation so external edits are not overwritten wholesale.
- [Current relays retain old routing state] -> State this explicitly in the UI
  and report; dynamic routing is a separate runner/app design problem.

## Migration Plan

No data migration is required. Existing concise and table provider forms both
remain valid. Rollback removes the Config tab and app service; files already
edited remain ordinary v2 TOML accepted by existing CLI commands.

## Open Questions

- A later steering change may add a runner-owned, synchronized route/provider
  index replacement API. If it does, the app mutation service can invoke it
  after a successful write without changing the presentation contract.
