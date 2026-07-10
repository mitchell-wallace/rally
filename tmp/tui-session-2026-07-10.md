# Rally TUI config session — 2026-07-10

## What landed

- `0b8092f` — `feat(tui): add targeted routing config service`
  - targeted, atomic v2 TOML mutations for one route, reasoning key, or provider
    disabled switch;
  - app-layer machine-config snapshot/mutation service;
  - configured shorthand catalog, built-in/custom role union, provider member
    counts, preservation tests, and archguard update;
  - current OpenSpec design/spec/tasks artifacts.
- `9f90dec` — `feat(tui): make routing config operable`
  - fifth Config tab in `internal/presentation/tuitabs`;
  - ordered route add/remove/reorder, custom role creation, reasoning picker,
    provider enable/disable, reload/save feedback;
  - modal exact-target confirmations that consume unrelated keys;
  - CLI mapping of the app service into presentation-neutral `tuicore` values
    for demo, historical, and live TUI modes.
- `e188db3` — `fix(config): preserve table spacing on targeted inserts`
  - inserts a new key before a table's trailing blank separator;
  - adds regression coverage for clean `[reasoning]`/next-table spacing.

All three functional commits were pushed to `origin/dev`. No staging/main,
VERSION, tags, global Rally binary, or global Laps binary was touched.

## Design decisions

### Config scope

The Config tab explicitly reads and writes the machine config path
(`~/.config/rally/config.toml`, honouring `XDG_CONFIG_HOME`). Routes,
reasoning, and providers are shared runner policy. Repo `.rally/config.toml`
remains an override layer and is never written by this surface.

This is labelled in the tab with the resolved path and the note that writes
apply to relays started afterwards. A repo override can therefore still mask a
machine value in that repo; the TUI does not imply otherwise.

### Persistence

`config.SaveV2File` was investigated and rejected for this surface because it
marshals the entire `V2Config`, dropping comments and normalizing unrelated
format. New config-owned targeted operations:

1. reload the current single machine file;
2. mutate one typed value;
3. patch only the corresponding table/key;
4. decode/validate the exact resulting bytes through existing v2 logic;
5. write a same-directory temp file, preserve mode, sync, and atomically rename.

Unrelated TOML remains byte-identical. The edited value itself is rendered by
the existing TOML library (so its quote style can normalize). Disabling a
concise provider necessarily converts only that provider to
`[providers.<name>]` table form; the untouched parent table/comment and other
providers stay in place.

### Architecture and lifecycle

Presentation still imports only `tuicore`, runtime events, keyboard, and style.
The app service owns config/role/store coordination; CLI maps app DTOs to
presentation DTOs/callbacks.

`app.StartRelay` constructs immutable route/reasoning/provider state at relay
startup. Persisted edits therefore affect subsequently loaded relays. There is
no supported dynamic route/provider index replacement seam, so an in-flight
relay is not claimed to change.

## Live tmux verification evidence

Binary and isolation:

```text
go build -o ./bin/rally ./cmd/rally
./bin/rally version
rally v1.0.0-dev

tmux new-session -d -s tuitest -x 140 -y 38 \
  -c /tmp/rally-tui-e2e/nonworkspace \
  'XDG_CONFIG_HOME=/tmp/rally-tui-e2e/xdg /workspace/rally/bin/rally tui --demo'
```

The initial Config pane showed the actual machine path plus all expected
surfaces:

```text
[5] Config
Machine config · /tmp/rally-tui-e2e/xdg/rally/config.toml
> default  built-in route: op:zai → cx:g55  effort: default
  junior   built-in route: op:zai → cx:g55  effort: medium
  planner  custom   route: cl:sonnet  effort: default
  primary            ENABLED  2 runners
  secondary          DISABLED 1 runners
```

Route reorder (`Enter`, `]`) updated both pane and disk:

```text
Role default · reasoning default
  1. cx:g55
> 2. op:zai

default = ['cx:g55', 'op:zai'] # reorder this
```

Route add (`a`, `Enter`) showed the configured-only picker and persisted the
selected shorthand:

```text
Add runner to default
> ag
  cl
  cl:sonnet
  cx
  cx:g55
  op
  op:deep
  op:zai

default = ['cx:g55', 'op:zai', 'ag'] # reorder this
```

Route removal blind-spot path (`x`, `Down`, `x`) retained the armed target;
the capture before and after `Down` was identical:

```text
Confirm: remove ag from default
Press x again to confirm · Esc cancels
Other keys are ignored while this exact target is armed.

default = ['cx:g55', 'op:zai'] # reorder this
```

Reasoning set/clear/re-add was driven through the picker. Final bytes and pane:

```text
Reasoning effort for default
  (default / clear)
  low
  medium
> high
  xhigh

[reasoning]
junior = "medium"
default = 'high'

[providers]
```

Provider retarget blind-spot path (`Enter`, `Up`, `Enter`) likewise left the
exact provider armed. The enabled concise provider converted and was sidelined:

```text
Confirm: toggle provider primary to disabled
Press Enter again to confirm · Esc cancels
Other keys are ignored while this exact target is armed.

[providers.primary]
models = ['op:zai', 'op:deep'] # concise provider converts on disable
disabled = true
```

The reverse direction was also verified on the existing table provider:

```text
[providers.secondary]
models = ["cx:g55"]
disabled = false # table provider remains table form
```

Custom role creation (`n`, type `observer`, `Enter`, `a`, `Enter`) produced:

```text
Role observer · reasoning default
> 1. ag

observer = ['ag']
```

Scope verification ran from a directory containing this repo override:

```text
# REPO SCOPE SENTINEL: the TUI must not edit this file.
[routes]
qa = ["op:zai"]
```

After another machine provider toggle, that repo file was byte-identical while
the machine file changed. The pane continued to label the machine path.

`XDG_CONFIG_HOME=/tmp/rally-tui-e2e/xdg ./bin/rally routes check` passed. Its
provider summary showed `primary` disabled, and the diagnostic said its runners
were sidelined until re-enabled. Running from the repo-sentinel directory also
passed with the repo `qa` route layered in.

Finally, the TUI was quit and relaunched. The captured Config pane showed:

```text
> default   built-in route: cx:g55 → op:zai  effort: high
  observer  custom   route: ag  effort: default
  planner   custom   route: cl:sonnet  effort: default
  primary             DISABLED 2 runners
  secondary           ENABLED  1 runners
machine config reloaded
```

The follow-up scope run then toggled secondary back to disabled, also
persisting correctly.

## Gates

Every functional commit was gated with:

```text
go build ./...                 PASS
go vet ./...                   PASS
go test ./...                  PASS
go run ./tools/archguard       PASS (pre-existing size warnings only)
```

Focused config/app/TUI/CLI tests were run repeatedly during implementation.
The final binary was built only at `./bin/rally`.

## Honest gaps / TODOs

- Current relays retain the route/reasoning/provider indexes loaded at startup;
  dynamic in-flight replacement needs a future runner/app synchronization seam.
- Repo overrides are intentionally read only and can mask machine values. A
  future scope-aware editor could display origin/override diagnostics, but
  should not silently mix scopes.
- Provider membership and harness/model alias management remain in
  `rally config`; this tab only toggles configured providers and picks existing
  route shorthands.
- A custom role can be left with an empty route by removing all entries. There
  is no whole-role delete command in this increment because the requirement was
  entry management, not role lifecycle.
- Converting a concise provider retains an empty `[providers]` parent table and
  appends the new provider subtable later in the document. This is valid TOML
  and preserves unrelated bytes/comments more faithfully than regrouping the
  provider section.

## Rally / environment friction observed

- The first tmux command failed before Rally launch because
  `/tmp/tmux-1000` did not exist. Creating it mode `0700` fixed the environment.
- Running multiple full `go test ./...` processes concurrently caused unrelated
  runner tests that use temporary git state to interfere (`fatal: not in a git
  directory` and one timing/state assertion). After clearing overlapping gate
  processes, isolated full-suite reruns passed. Gates were kept isolated after
  that.
- No `rally start`/laps dogfood relay was needed for this bounded UI change;
  the real built binary, tmux TTY, config writer, `routes check`, and relaunch
  path were exercised directly.
- `.laps/tmp/` was already untracked at session start. Rally's tracked ignore
  rules explicitly re-include direct `.laps/*` children, so a checkout-local
  exclude could not make status clean. The directory was preserved intact at
  `/tmp/rally-preexisting-laps-tmp-2026-07-10` rather than deleted or committed.
