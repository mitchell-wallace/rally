# Rally

Rally is a small CLI orchestrator that runs a repeatable coding loop across
multiple agent CLIs in the same repo. Hand it a task (or a queue of laps),
pick a mix of agents, and Rally cycles through them — iteration after
iteration — capturing transcripts, auto-committing progress, and rotating
away from agents that fail or stall.

It pairs well with Claude Code, Codex CLI, Antigravity, and Opencode. You
can also add your own CLI agent in config without recompiling.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/mitchell-wallace/rally/main/install.sh | sh
```

This drops `rally` into `~/.local/bin/rally`. Add that directory to your
`PATH` if it isn't already.

Update later with the built-in self-updater (which also updates `laps` if it is installed next to `rally`):

```sh
rally update
```

Rally also performs a quiet background update check on startup. Disable it
with `RALLY_NO_UPDATE_CHECK=1`.

## Prerequisites

- A git repository (Rally always operates inside one).
- At least one of `agy`, `claude`, `codex`, or `opencode`
  installed and already authenticated in your shell.

## Quick start

From the root of any git repo:

```sh
rally init                  # one-time: writes .rally/config.toml + scaffolding
rally start                 # run a single iteration with the default mix
rally start --iterations 4  # run four iterations
rally start --iterations 4 --agent "ag:1 cc:1 cx:2 op:1"  # custom mix
```

While a relay is running, watch the current try's transcript live in another
shell:

```sh
rally tail              # latest try
rally tail --try 3      # specific try by id
```

All shortcuts require a **double-press** within a 4-second confirm window;
a single press intentionally does nothing. The shortcut legend adapts to
terminal width.

| Shortcut | Action | Behaviour |
|----------|--------|-----------|
| Ctrl+X | Graceful stop | Sets a stop flag. The current try runs to completion, then the relay halts without launching further runs. |
| Ctrl+C | Quit now | Cancels the active try immediately (SIGINT to the process group, 5-second drain, then SIGKILL). A second Ctrl+C during the drain window escalates to an immediate SIGKILL. |
| Ctrl+P | Pause | Cancels the active try and prints "Paused — press Enter to resume". Pressing Enter resumes with session reuse. |
| Ctrl+S | Skip | Skips the current agent and advances to the next route entry. |

The next `rally start` from the same workspace asks whether to resume the
unfinished relay or start fresh; `--resume` and `--new` skip the prompt.

## How a Rally loop works

Each iteration of `rally start` does this:

1. **Pick a route.** `--agent` override wins, otherwise the lap's `assignee`
   matches a `[routes]` entry, otherwise `default`.
2. **Pick an agent from that route.** Rally walks the route entries by
   quota (e.g. `cc:2` runs twice before advancing). Failures and freezes
   skip ahead.
3. **Build a prompt** from your project instructions, inbox messages, recent
   try context, and any matching `.rally/agents/{ASSIGNEE}.md` file.
4. **Run the agent CLI** in your repo. Rally captures every byte of stdout
   and stderr to a try log.
5. **Auto-commit** any dirty workspace changes once the try finishes
   (uses `--no-verify` by default — see `run_hooks_on_autocommit`).
6. **Record the try** in `.rally/state/tries.jsonl` and append filtered
   output to the relay log in `~/.local/share/rally/relays/`.

If the agent stalls, Rally graceful-kills it, classifies the failure, and
either retries via session resume or advances to the next route entry. See
the `[reliability]` section for tunables.

### Graceful stop vs quit now

The two stop shortcuts differ in how aggressively they terminate the relay:

- **Ctrl+X (graceful stop):** arms on first double-press, sets a stop flag
  but does **not** cancel the running attempt. The current try finishes
  naturally (bounded by the stall detector), then the relay exits without
  starting further runs. Use this when you want the agent to wrap up its
  work cleanly.

- **Ctrl+C (quit now):** arms on first double-press, immediately cancels
  the running attempt's context. The harness sends SIGINT to the agent's
  process group, waits up to 5 seconds for a clean exit, then SIGKILL any
  survivors. A second Ctrl+C during the drain window skips the grace period
  entirely and force-kills the group. Use this when the agent is stuck or
  you need to stop immediately.

### Pause and resume

Ctrl+P cancels the current try and pauses the relay. Rally prints
"Paused — press Enter to resume" and blocks until you press Enter. On
resume, Rally reuses the agent's session when the harness supports it,
so the agent continues in context rather than starting from scratch.

### Double-press confirm window

Each shortcut key requires two presses of the **same** key within a
4-second window. The first press arms the action silently; the second
press fires it. Pressing a different shortcut key starts a new arm cycle
for that key instead. This prevents accidental triggers from a single
keypress.

### Git and commit conventions

Rally manages its own commits so git history stays clean and
machine-readable. All Rally-authored commits use `--no-verify` (unless
`run_hooks_on_autocommit` is set) and fall back to `Rally <rally@localhost>`
when the repo lacks `user.name` / `user.email`.

| Message | When | Scope |
|---|---|---|
| `rally: initialize workspace` | `rally init` bootstraps `.rally/` | Setup files only, path-scoped |
| `rally: install laps hooks` | Hooks installed or updated | Hook files only, path-scoped |
| `rally: run N attempt M (harness)` | Agent leaves uncommitted file changes | `git add -A` |
| `<lap-description>: done` | Agent guidance after `laps done` | Agent's own commit |
| `<lap-description>: in progress (handoff)` | Agent guidance after `laps handoff` | Agent's own commit |

**State folding.** Rally's bookkeeping (`.rally/`, `.laps/`) is folded into
the existing commit history rather than creating extra commits:

- In the common path (code run), `autoCommit` stages everything including
  state — no separate commit is needed.
- For no-code runs where only state changed, Rally amends a rally-authored
  HEAD (`rally:` prefix) and appends ` [+state]` to the message (never
  stacks the suffix).
- If HEAD is not rally-authored, a single `rally: update state` commit is
  created.
- Nothing happens if nothing is staged or the directory is not a git repo.

**Leftover-work guidance.** When a run starts with a dirty working tree
(excluding `.rally/` and `.laps/`), Rally injects a prompt section reminding
the agent to review and commit those changes before starting new work.

## Driving Rally

### Supported built-in harnesses and aliases

| Alias      | Full name     | Binary    |
|------------|---------------|-----------|
| `ag`/`agy` | `antigravity` | `agy`     |
| `cc`       | `claude`      | `claude`  |
| `cx`       | `codex`       | `codex`   |
| `op`       | `opencode`    | `opencode`|

For Opencode runs Rally automatically sets:

```sh
OPENCODE_PERMISSION='{"*":"allow"}'
```

For Antigravity runs Rally uses `agy --print` with
`--dangerously-skip-permissions`. `agy` 1.0.0 does not expose a model flag, so
when `antigravity_model` resolves to a value Rally temporarily writes that
model label to `~/.gemini/antigravity-cli/settings.json` for the run and then
restores the prior setting.

### Choosing agents with `--agent`

`--agent` accepts a quota-bearing mix. Repeat the flag, or pass one quoted
string:

```sh
rally start --agent cc:1 --agent cx:2 --agent op:1
rally start --agent "cc:1 cx:2 op:1"
```

**Bare aliases in `--agent` round-robin one at a time.** `--agent "cc ag op"`
is equivalent to `cc:1 ag:1 op:1`: claude → antigravity → opencode → claude → …
(This is a deliberate asymmetry with `[routes]` config — see below.)

Mix bare aliases, quota-bearing aliases, named models, and even role
references in one string:

```sh
rally start --agent "cc:opus cx:2 op:z"
rally start --agent "SENIOR"
rally start --agent "op:opencode-go/kimi-k2.6 DEFAULT:1"
```

Role references inline a configured `[routes]` entry into the override.
They are valid in `--agent` only, never inside `[routes]` itself.

If you do not pass `--agent`, Rally falls back to `[defaults].mix` from
config, or `claude:1 codex:2` if that is unset.

### Tailing a try log

```sh
rally tail              # follow the latest try
rally tail --try N      # follow try N (1-based)
```

Try logs are scoped per-repo even when many workspaces share one data dir:
the path includes `<basenamePrefix>-<hash>` so two checkouts of the same
project never write to the same file.

## Configuration

Rally config is layered. The **user-level** file at
`~/.config/rally/config.toml` (honouring `$XDG_CONFIG_HOME`) is the base — the
main source of truth shared across every repo. The **repo-level**
`.rally/config.toml` holds per-repo **overrides** only: Rally loads the user
file first, then applies anything set in the repo file on top of it (per key, a
repo value wins; a sub-table such as `[harness.cc.models]` merges per entry, and
a `[routes]` entry replaces just that role's list). `rally init` seeds the user
file with sensible defaults (only if it doesn't exist) and writes a
**comments-only** repo file that documents the knobs and points at the user
file. Edit the user base with `rally config`; edit repo overrides with
`rally config --repo`.

`rally init roles` adds role routes for `junior`, `senior`, `ui`, `verify`, and
`recovery` to the **user** config, plus role instruction files under
`.rally/agents/` (see [Role instruction files](#role-instruction-files)). The
example below shows the active config shape (as written to the user file);
generated defaults may be more compact when unset values can fall back to
harness defaults.

```toml
schema_version = 2
laps_instructions = ""
run_hooks_on_autocommit = false
data_dir = ""

[defaults]
iterations = 1
mix = "cc cx"
claude_model = "claude-opus-4.7"
codex_model = "gpt-5.5"
opencode_model = "zai-coding-plan/glm-5.1"
antigravity_model = "Gemini 3.5 Flash (High)"

[laps]
instructions_file = ".rally/laps_instructions.md"

[free_run]
prompt_file = ".rally/free_run_prompt.md"

[harness.cc.models]
opus = "claude-opus-4-7"
sonnet = "claude-sonnet-4-6"

[harness.op.models]
z  = "zai-coding-plan/glm-5.1"
gk = "opencode-go/kimi-k2.6"

[harness.ag.models]
flash = "Gemini 3.5 Flash (High)"

[routes]
default = ["ag:flash:1", "cc:opus:1", "cx:1", "op:gk:2-4"]
SENIOR  = ["cx:1", "cc:opus:1"]
JUNIOR  = ["op:z:4", "op:gk:2", "ag:1"]
recovery = ["claude"]

[providers]
# Runners that share one usage-limit budget. A usage limit on any member
# benches the whole group until the reset.
codex = ["g55", "g54", "opencode:openai/gpt-5.5"]

[providers.opencode-go]
models   = ["op:gk", "opencode:opencode-go/glm-5.1"]
disabled = true

[reliability]
stall_threshold_secs  = 900
liveness_probe        = false
retry_budget          = 5
run_timeout_secs      = 4500
try_timeout_secs      = 3600
handoff_timeout_secs  = 300
```

### `[defaults]`

| Field                | Type   | Purpose                                              |
|----------------------|--------|------------------------------------------------------|
| `iterations`         | int    | Default iterations when `--iterations` is absent     |
| `mix`                | string | Default agent mix when `--agent` is absent           |
| `antigravity_model`  | string | Model label for the `ag`/`agy`/`antigravity` alias   |
| `claude_model`       | string | Model for the `cc`/`claude` alias                    |
| `codex_model`        | string | Model for the `cx`/`codex` alias                    |
| `opencode_model`     | string | Model for the `op`/`opencode` alias                  |

A bare alias like `cc` in a mix resolves through `[defaults].claude_model`
first, then falls back to the harness's hard-coded internal default.

### `[laps]` and `[free_run]`

| Section      | Field               | Purpose                                                          |
|--------------|---------------------|------------------------------------------------------------------|
| `[laps]`     | `instructions_file` | Prompt body injected on every laps-backed iteration              |
| `[free_run]` | `prompt_file`       | Prompt body in no-backend mode when no ready lap exists          |

Both fall back to a built-in default if the file is missing or unreadable.
Injection in laps-backed mode is unconditional (per v0.4.0 — no toggle).
The free-run file is ignored in laps-backed mode.

**Deprecation:** The legacy `[fallback]` section (with `instructions_file`) is
still accepted as an alias for `[free_run]` / `prompt_file`. If `[fallback]`
is present without `[free_run]`, Rally loads the value and logs a one-time
deprecation warning. `[free_run]` takes precedence when both are set.
`[fallback]` will be removed in a future release.

### `[routes]` — role-aware routing

`[routes]` enables role-aware routing. Route keys are matched
case-insensitively against the active lap's `assignee` value, with
`default` reserved for the no-role / no-match case.

Each entry is one of:

- a bare harness alias such as `cx` or `ag`
- a named model such as `cc:opus` or `op:z`
- a raw `harness:model` string such as `op:opencode-go/kimi-k2.6`
- any of the above with an optional trailing quota: `:N` or `:N-M`

#### Quota rules — read this carefully

| Quota form | Behaviour                                                            |
|------------|----------------------------------------------------------------------|
| _none_     | **Stay on this entry until it fails.** Same harness reruns each pass. |
| `:N`       | Run exactly `N` consecutive iterations, then advance.                 |
| `:N-M`     | Prefer rotating after `N`. Allow up to `M` if every other entry is exhausted or frozen. |

**Important asymmetry between `[routes]` and `--agent`:**

- In `--agent`, bare aliases mean "run once each, then rotate"
  (`--agent "cc ag"` ⇒ `cc:1 ag:1`).
- In `[routes]`, bare aliases mean "stay until failure". If you want
  one-iteration round-robin from `[routes]`, you must add `:1`:

```toml
# Round-robin: one try per agent, then advance
default = ["cc:1", "ag:1", "op:1"]

# Stick on claude until it fails, then antigravity until it fails, …
default = ["cc", "ag", "op"]
```

This split is intentional: command-line mixes are usually short and
exploratory (you want to try a roster), while routes are usually long-lived
preferences ("use claude for this role, fall back to antigravity if it dies").

#### Selection priority

Routing on each iteration is:

1. `--agent` override route, if supplied.
2. Lap `assignee` matches a route name (case-insensitive).
3. `default`.

In no-backend mode there is no lap and no `assignee`, so Rally always uses
`default`. Non-default routes still load and validate, but are never
selected.

#### Single-runner lanes

Rally warns at relay start when a lane has exactly one runner entry — a
single dead harness stalls that lane with no fallback to rotate to. A
single-runner lane is valid, just fragile. Prefer at least two entries per
lane so the scheduler can rotate past a failing harness.

#### Role instruction files

When a lap has an `assignee`, Rally looks for a `{ASSIGNEE}.md` role file under
`.rally/agents/`, resolving (case-insensitively), highest priority first:

1. `.rally/agents/user/{ASSIGNEE}.md` — your overrides
2. `.rally/agents/builtin/{ASSIGNEE}.md` — Rally-managed defaults
3. the role default embedded in the binary

`builtin/` files are **managed by Rally**: they are regenerated from the binary
on each run, so they auto-update when you update Rally — don't hand-edit them.
Put customizations in `user/`, which always win over `builtin/` and are never
touched. When Rally first runs after the upgrade that introduced this layout, it
migrates any legacy flat `.rally/agents/{ROLE}.md` files: files matching content
Rally has shipped move to `builtin/` (and auto-update), and anything you
customized moves to `user/` (preserved), with a one-line notice. If a file is
found, its contents are inserted between Rally's base instructions and the lap
body. Missing files are silent. Rally treats the file contents as opaque text —
no front-matter parsing, no template.

### `[harness.<name>.models]` — named models

Each harness can declare named model shortcuts.

```toml
[harness.cc.models]
opus   = "claude-opus-4-7"
sonnet = "claude-sonnet-4-6"

[harness.op.models]
z  = "zai-coding-plan/glm-5.1"
gk = "opencode-go/kimi-k2.6"

[harness.ag.models]
flash = "Gemini 3.5 Flash (High)"
```

With the above, `--agent "cc:opus op:z ag:flash"` resolves to Claude with
`claude-opus-4-7`, Opencode with `zai-coding-plan/glm-5.1`, and
Antigravity with `Gemini 3.5 Flash (High)`.

Model names must be non-numeric identifiers — `4` is rejected so quota
parsing stays unambiguous.

Built-in harnesses (`ag`/`cc`/`cx`/`op`) can declare named models but
**cannot** declare `command`, `model_flag`, `output_strategy`, or
`tail_stream`.

### `[providers]` — shared-quota groups

`[providers]` groups runners that draw from the same usage-limit budget. By
default Rally infers a quota bucket per harness (and per opencode provider /
antigravity model family). When several runners actually share one account —
e.g. multiple codex models behind one ChatGPT plan, or a codex model exposed
both directly and through opencode — list them under a provider so a usage
limit on **any** member benches **every** member until the reset. This avoids
burning retries on siblings that are already exhausted.

Each provider key is a user-defined name; its value is a list of model specs.
A spec is a named model shortcut (`g55`), a harness-qualified alias (`op:ds`),
a full `harness:model` (`opencode:openai/gpt-5.5`), a whole-harness wildcard
(`codex:*`), a configured model-prefix wildcard (`opencode-go/*` or
`op:opencode-go/*`), or a model-suffix wildcard (`codex:*spark` or `*spark`).
Wildcards expand from your local config only: they include configured model
aliases and matching default models, not an external catalog of every model a
provider could offer. A bare shortcut must be defined under exactly one
harness's `[harness.<h>.models]` table, otherwise qualify it (`cx:g55`). A given
runner may belong to at most one provider.

```toml
[providers]
# Concise array form — enabled, models only.
codex = ["g55", "g54", "opencode:openai/gpt-5.5"]
```

```toml
[providers]
# Cleaner when you want every configured codex model.
codex_all = ["codex:*"]

# Cleaner when your opencode aliases all point at one provider prefix.
opencode_go = ["opencode-go/*"]

# A suffix wildcard — every codex model whose slug ends in "-spark".
codex_spark = ["codex:*spark"]
```

To **disable** a whole provider — sidelining every member for the relay — use
the table form with `disabled = true` (TOML cannot attach a flag to a bare
array). Disable to conserve a known long usage limit (e.g. a monthly cap), or
to keep a harness free while another session runs a large task:

```toml
[providers.claude]
models   = ["cc:opus", "cc:sonnet"]
disabled = true
```

A disabled provider's runners are skipped during selection; if a lane has no
other runner it fails fast with a clear message rather than waiting. `rally
routes check` lists every provider, its member count, and whether it is
disabled.

To **carve a model out of a wildcard group** — for example when one model shares
a harness but draws from a separate quota — pair `exclude` with the wildcard. An
exclude uses the same spec forms as `models`; matching members are removed from
the provider *before* the one-runner-one-provider rule is checked, so the carved
out model can form its own provider without a conflict. An exclude that matches
nothing is a no-op (handy for forward-looking filters), but a `models` wildcard
that matches nothing is still a hard error.

```toml
# codex shares one quota; codex-spark is metered separately, so pull it out.
[providers.codex]
models  = ["codex:*"]
exclude = ["codex:*spark"]

[providers.codex-spark]
models = ["codex:*spark"]
```

### `[reasoning]` — role-level variant preferences

`[reasoning]` layers a per-role default on top of `[routes]` and
`[harness.<name>.models]`. Keys are role names (the same names matched in
`[routes]`, case-insensitively). Each preference is resolved **only after**
the route entry's harness is selected, and **only when that entry has no
explicit model token** — so an explicit model like `cx:g55` always wins.

```toml
[reasoning]
verify = "g55-xh"   # high-reasoning variant on the codex-led verify lane
junior = "g55-l"    # lightweight variant on the opencode-led junior lane
```

A value may be one of three forms:

- A **bare model alias** resolved in the selected harness (a variant you named
  under `[harness.<name>.models]`, e.g. `g55-xh`). This changes the model and
  sets no effort flag.
- A **harness-scoped alias** like `op:g55-xh`, which resolves only when the
  route selects that harness and is ignored for others. A scoped alias that
  names no known model is a hard error in `rally routes check` (likely a typo).
- A **bare effort token** like `xhigh`, `high`, or `low`, applied as a
  reasoning-effort flag to the selected harness where supported. Each harness
  injects effort through its own flag:

  | Harness     | Effort flag                          | Documented values                              |
  |-------------|--------------------------------------|------------------------------------------------|
  | codex       | `-c model_reasoning_effort=<value>`  | `none` `minimal` `low` `medium` `high` `xhigh` |
  | claude      | `--effort <value>`                   | `low` `medium` `high` `xhigh` `max`            |
  | opencode    | `--variant <value>`                  | provider-specific, no fixed set                |
  | antigravity | unsupported as a flag                | reasoning encoded in the model alias/name      |

Unknown effort tokens warn and pass through rather than failing the run (the
spike confirmed claude/opencode ignore many unsupported values and codex
rejects invalid values at the API), so a forward-compatible default never
pre-emptively kills a run on a token Rally doesn't recognise.

### User-defined harnesses

Declaring `command` on a harness registers a brand-new CLI agent. Example:

```toml
[harness.droid]
command          = ["droid", "run", "$PROMPT"]
model_flag       = "--model"
output_strategy  = "tail"
tail_stream      = "combined"
output_lines     = 40

[harness.droid.models]
default = "droid-v1"
fast    = "droid-v1-turbo"
```

- **`command`** is an array of strings passed to `exec`. The literal
  `$PROMPT` is replaced positionally with the prompt body. If `$PROMPT`
  does not appear anywhere in `command`, the prompt is piped on stdin
  instead. Substitution is **positional, not shell** — no interpolation,
  so shell metacharacters in the prompt are safe.
- **`model_flag`** controls how the resolved model joins the command:

  | `model_flag` value | Behaviour                                                    |
  |--------------------|--------------------------------------------------------------|
  | `"--model"` (set)  | Appends `[model_flag, model]` when a model is resolved      |
  | `""` (empty)       | Appends `[model]` positionally when a model is resolved     |
  | omitted            | Never appends a model; harness uses its own default         |

  When `model_flag` is omitted and a non-empty model is resolved, rally
  logs a one-line note that the model could not be passed.

- **`tail_stream`** selects which output to capture (`stdout`, `stderr`,
  or `combined`; default `combined`). **`output_lines`** sets the trailing
  line count (default 40). The only supported `output_strategy` is `"tail"`.

`$MODEL` is **not** a recognised placeholder in `command`. If present, the
config loader rejects it with an explicit error directing you to
`model_flag`.

### `[reliability]`

Tunes retry, stall detection, the liveness probe, and per-run time budgets.

| Field                    | Type | Default | Purpose                                                            |
|--------------------------|------|---------|--------------------------------------------------------------------|
| `stall_threshold_secs`   | int  | `900`   | Seconds of log inactivity before a try is considered stalled       |
| `liveness_probe`         | bool | `false` | Experimental side-channel probe for ambiguous stall signals        |
| `retry_budget`           | int  | `5`     | Maximum retries per try before advancing to the next route entry   |
| `run_timeout_secs`       | int  | `4500`  | Per-run wall-clock budget (75 m) measured **across all retries**   |
| `try_timeout_secs`       | int  | `3600`  | Secondary per-attempt cap (60 m) guarding a single runaway try     |
| `handoff_timeout_secs`   | int  | `300`   | Bounded handoff-only resume window (5 m), not counted in the run budget |

`0`/unset yields the default. Positive timeout values below 300 seconds are
rounded up to 300 seconds with a warning. `handoff_timeout_secs` is clamped
below the effective `try_timeout_secs`/`run_timeout_secs` when possible while
preserving that 5-minute minimum. When `try_timeout_secs >= run_timeout_secs`
the run budget subsumes the per-try cap and the config is accepted rather
than rejected. The two timeouts are orthogonal to the silence-based stall
detector — whichever fires first wins. See
[Recovery and per-run timeouts](#recovery-and-per-run-timeouts) for how they
combine with the `recovery` route.

```toml
[reliability]
stall_threshold_secs  = 900
liveness_probe        = false
retry_budget          = 5
run_timeout_secs      = 4500
try_timeout_secs      = 3600
handoff_timeout_secs  = 300
```

Stall detection is conservative. A try is flagged stalled only when the
log file has not been modified for `stall_threshold_secs` **and** the
agent has zero active TCP connections **and** its IO byte counters have
not advanced. On Linux all three conditions must hold; on macOS the
connection clause is treated as satisfied (no procfs equivalent); on
Windows stall detection is disabled. Confirmed stalls are graceful-killed
(SIGTERM → 5-second drain → SIGKILL) and retried through the resume-aware
retry path.

The liveness probe is opt-in and skipped for harnesses whose adapter does
not support it. It sends a lightweight "respond with OK" prompt when the
stall signal is ambiguous (mtime advancing but IO idle for 60 s). A
successful probe clears the stall flag.

### Recovery and per-run timeouts

Rally bounds how long a struggling run can grind, and routes genuinely
stuck, half-finished work to a dedicated recovery session instead of
letting one attempt loop for hours. This is pure Rally routing and prompt
behavior — laps remains the queue backend, the lap's `assignee` is never
rewritten, and recovery state is derived from the try records Rally already
persists (so it survives relay restarts).

**Per-run and per-try time budgets.** Each run has a hard wall-clock budget
measured *across all of its retry attempts* (`run_timeout_secs`, default
75 m). A secondary per-attempt cap (`try_timeout_secs`, default 60 m) guards
a single runaway try; the run budget sits slightly above it so a quick
non-blocking retry after a transient blip still has buffer. Whichever of the
run budget, the per-try cap, or the silence stall detector fires first
wins. A per-try cap firing with run budget left just ends that attempt and
may retry within the remaining budget; when the **run budget** is exhausted,
the run stops retrying and proceeds to a bounded handoff.

**Bounded handoff-only resume.** On run-budget exhaustion, if the harness
supports session resume and a session was captured, Rally resumes that
session *once* under a separate hard limit (`handoff_timeout_secs`, default
5 m, not counted in the run budget) with a handoff-only prompt that forbids
further implementation and instructs the agent to summarize the blocker and
call `laps handoff` + `laps wrapup`. A successful handoff there is a normal
(success-side) handoff, not a failure. If the harness cannot resume or no
session exists, no synthetic handoff is fabricated and the run resolves
without one. Worst-case wall clock per run is roughly `run_timeout_secs +
handoff_timeout_secs`.

**The `recovery` role and route.** RECOVERY is a reasoning-heavy role like
VERIFY but with the authority and coding ability to modify code and
reconcile dirty state, like SENIOR. It defaults to a stronger runner
(`rally init roles` seeds a `recovery` route) and does not reuse SENIOR's
prompt. Its prompt requires it to classify the leftover state into exactly
one of `continue`, `discard`, `course_correct`, `repair_plan`, or
`needs_user`, then *act* on that classification (never stopping at diagnosis
unless `needs_user`). The classification is recorded on the run via
`laps wrapup --classification <value>` and surfaces as telemetry, so
recovery outcomes stay filterable.

**Two recovery triggers.** The next run for a lap is forced onto the
`recovery` route only for the two states that leave a suspect, half-finished
tree needing reconciliation:

1. **Dirty handoff** — a handoff completed yet meaningful own-uncommitted
   changes remain (auto-commit is suppressed so RECOVERY inherits the real
   dirty tree to reconcile).
2. **Handoff timeout** — the bounded handoff-only resume above failed to
   finalize.

An ordinary `failed` try (usage limit, provider instability, agent error)
is **not** a recovery trigger — it routes, benches, and rotates through the
existing resilience paths. A plain `incomplete` outcome (changes, no
handoff) keeps its existing resume-with-finalization retry, and a clean
handoff (no leftover dirt) keeps its existing follow-up flow.

**Anti-loop cap.** A recovery run can itself time out or leave another dirty
handoff, which would re-arm recovery forever. Rally therefore allows at most
**two** consecutive recovery runs per lap; once the cap is reached it stops
routing to recovery, raises a `needs_user` operator-attention issue, and
falls back to the lap's normal route rather than looping. (This cap-hit
decision happens at routing time with no recovery agent running; a missing
`recovery` route likewise falls back to the lap's normal route with a
warning, never deadlocking the relay.)

### Error classification

Rally maps known harness failures to retry strategies via a static table
in `internal/reliability/patterns.go`.

| Pattern                                  | Strategy            | Meaning                                      |
|------------------------------------------|---------------------|----------------------------------------------|
| opencode "API bad request" from provider | `rotate`            | Advance to the next route entry immediately  |
| antigravity gemini-cli exit 1            | `resume + retry`    | Resume the session and retry once            |
| claude rate-limit interrupt              | `wait + resume`     | Wait for the cooldown, then resume and retry |
| codex completion despite limit warning   | `no-op`             | Treat as a successful completion             |
| unknown failure                          | `fresh restart`     | Start a new try from scratch (safe default)  |

New patterns are added to `ErrorPatterns`; misses fall through to
`fresh restart`.

### Other settings

By default Rally uses `--no-verify` for its post-run autocommit checkpoint
so repo hooks cannot block progress/logging commits. Set
`run_hooks_on_autocommit = true` if you want those fallback commits to run
your normal Git hooks.

### `schema_version` and backwards compatibility

The config file carries a top-level `schema_version` integer (currently
`2`). Absent versions are treated as `1` and accepted silently. On mismatch
Rally logs a one-line warning and proceeds.

v0.2.x configs with root-level `claude_model`, `codex_model`, etc. still
load. If both root-level and `[defaults]` values exist, `[defaults]`
wins and a deprecation note is logged. Config writes always emit the new
shape (models under `[defaults]`).

## Validating routes

Run before a relay or in CI to validate `[routes]`, resolve named models,
and catch quota syntax errors early:

```sh
rally routes check
```

Non-zero exit on parse, resolution, or quota errors. Soft problems
(missing `default`, unreachable role routes) print warnings only.

Drop into Make:

```make
routes-check:
	rally routes check
```

Or CI:

```sh
rally routes check
go test ./...
```

## Project instructions

Project instructions are repo-specific guidance Rally injects into every
prompt:

```sh
rally instructions edit   # open in $EDITOR
rally instructions show   # print to stdout
```

Stored at `.rally/instructions.md` and included in each session prompt.

## Where Rally stores state

Default data directory (override with `data_dir` in config):

```text
~/.local/share/rally
```

| Path                                                  | Contents                                |
|-------------------------------------------------------|-----------------------------------------|
| `~/.local/share/rally/relays/<repo>/relay-N.log`      | Full relay log per repo                 |
| `~/.local/share/rally/tries/<repo>/try-N.log`         | Per-try transcript                      |
| `~/.config/rally/config.toml`                         | User-level config (base for every repo) |
| `.rally/config.toml`                                  | Repo-level config overrides             |
| `.rally/state/tries.jsonl`                            | Try records (read by `rally tail`)      |
| `.rally/state/messages.jsonl`                         | Inbox messages                          |
| `.rally/state/agent_status.jsonl`                     | Agent status events                     |
| `.rally/summary.jsonl`                                | Run summaries and lap completions       |
| `.rally/instructions.md`                              | Project instructions                    |
| `.rally/agents/builtin/{ROLE}.md`                     | Rally-managed role instructions         |
| `.rally/agents/user/{ROLE}.md`                        | Your role instruction overrides         |

The `<repo>` segment is `<basenamePrefix>-<hash>`, derived from the
workspace path so multiple checkouts under one data dir never collide.

## Commands

```sh
rally start              # start or resume a relay
rally init               # initialise .rally/ in the current repo (workspace only)
rally init roles         # add default role routes (user config) + .rally/agents/{builtin,user}/ (no workspace scaffold)
rally init all           # full setup: workspace scaffold + roles
rally tail [--try N]     # follow a try's log
rally routes check       # validate [routes]
rally instructions edit  # edit project instructions
rally instructions show
rally update             # self-update from GitHub Releases
rally version            # print version (vX.Y.Z, vX.Y.Z-dev for source builds)
```

### `rally init` subcommands

| Command | What it does |
|---|---|
| `rally init` | Writes a comments-only repo `.rally/config.toml`, seeds the user-level `~/.config/rally/config.toml` if absent, scaffolds `.rally/state/`, and writes `.rally/.gitignore` entries. Idempotent — safe to re-run. |
| `rally init roles` | Adds `[routes]` entries for `junior`, `senior`, `ui`, `verify`, and `recovery` to the **user** config and sets up role instruction files under `.rally/agents/builtin/` (managed) and `.rally/agents/user/` (overrides). Does **not** touch workspace scaffold files (README, .gitignore, etc.). Idempotent. |
| `rally init all` | Runs `rally init` followed by `rally init roles` — full workspace + role setup in one step. The hidden alias `rally init-roles` also maps here for backward compatibility. Idempotent. |

For a fresh repo, `rally init all` is the quickest path to a fully configured workspace.

## Telemetry

Rally sends error, trace, and performance telemetry via the New Relic Go APM
agent when a license key is configured. Release binaries ship with a baked-in
New Relic license key so telemetry "just works" without extra setup; source
builds report nothing unless you provide a license key.

### Credential resolution and opt-out

The effective telemetry state is resolved in this order (first non-empty wins):

1. **`RALLY_TELEMETRY=0`** — force-disables all telemetry regardless of any
   credentials. No network calls, no files written.
2. **`[telemetry] enabled = false`** in `.rally/config.toml` — disables telemetry.
3. **`NEW_RELIC_LICENSE_KEY`** environment variable — overrides everything below.
4. **Baked-in default** (`DefaultNewRelicLicenseKey`) — injected by GoReleaser
   at release time; used when env and config are both empty.
5. **No license key** — telemetry stays off.

If you prefer to opt out, set `RALLY_TELEMETRY=0` or configure
`[telemetry] enabled = false`. Sentry telemetry has been entirely removed
(hard cutover in 0.9.1) and legacy Sentry configuration is ignored.

### What is sent

Rally sends structured error signals for operator-worthy failures (panics,
non-zero exits, freeze detections, stall timeouts, unfinalized agents,
relay stalls, and lap-integrity violations). Each event carries:

**Tags / Custom Attributes** (scalar, filterable):

| Tag | Example | Purpose |
|---|---|---|
| `relay_id` | `3` | Local relay counter (per workspace) |
| `run_id` | `7` | Local run counter |
| `try_id` | `12` | Local try counter |
| `role` | `junior` | Effective prompt role for the run/try |
| `runner` | `claude:claude-sonnet-4` | Harness and model |
| `repo` | `rally-a1b2c3` | Hashed repo identifier |
| `lap_id` | `abc123` | Lap identifier |
| `relay_guid` | `a1b2c3d4e5f6-rally-a1b2c3-20260610-3` | Globally unique relay id |
| `relay_started_at` | `2026-06-10T14:30:00Z` | Relay start (RFC 3339) |
| `machine_id_prefix` | `a1b2c3d4e5f6` | First 12 chars of anonymous machine id |
| `outcome` | `handoff_timeout` | Try lifecycle outcome (`completed`, `failed`, `run_timeout`, etc.) |
| `failure_category` | `usage_limit` | Stable failure taxonomy |
| `recovery_classification` | `repair_plan` | RECOVERY-only classification, when recorded |
| `agent_state` | `active` | Runner resilience state |
| `attempt` / `max_attempts` | `2` / `5` | Retry position and budget |
| `quota_scope` / `reset_at` / `reset_after` | `claude:opus` / `2026-…Z` / `30s` | Limit reset info (limit categories only) |

**Context blocks** (structured, not indexed):

| Context | Fields | Purpose |
|---|---|---|
| `rally` | `version`, `go_os`, `go_arch`, `term`, `machine_id`, `cwd` | Run environment |
| `failure_evidence` | `raw_signal`, `message` | Bounded provider text (limit categories only) |

Spans trace the relay → run → try hierarchy and carry prompt-size metrics
(total bytes plus a per-source breakdown). Try spans and structured logs
also record the lifecycle `outcome`, mark bounded handoff continuations with
`handoff_only=true`, and attach `recovery_classification` on RECOVERY runs
when one was recorded.

### Anonymous machine identity

On first run with telemetry active, Rally generates a random 128-bit
identifier and stores it at `<dataDir>/machine-id` (permissions `0600`).
The file location follows the configured data directory — by default
`~/.local/share/rally/machine-id`. This id is:

- **Not derived** from hostname, username, MAC address, or any host
  attribute — it is a pure random token.
- Used to group events from the same machine over time.
- **Resettable** by deleting the file; Rally generates a new one on the
  next telemetry-active run.
- Only written when telemetry is active. Disabled telemetry writes no file.

The first 12 hex characters are emitted as the `machine_id_prefix` tag
(low-cardinality, for grouping). The full 32-character id appears only in
the `rally` context block — never as a tag.

### Privacy guarantees

Rally **never** transmits:

- Hostname, username, or IP address. The New Relic agent is configured
  with a generic host display name (`rally-cli`) where supported.
- Task descriptions, codebase context, file contents, or agent
  transcripts. Fields named `prompt`, `output`, `transcript`,
  `current_task`, `log`, etc. are dropped entirely before any attributes
  are sent — no `[scrubbed]` placeholder attribute is emitted in their place.
- Your username in paths. Home-directory prefixes are collapsed to `~` in
  all telemetry values (contexts, structured logs, spans, and free-text
  fields like raw provider signals).
- String values longer than 4 KB are truncated.

### Failure categories and raw provider signals

Failures are classified by the error-classification taxonomy
(`usage_limit`, `short_rate_limit`, `provider_overloaded`,
`incomplete_finalization`, `agent_error`, …). Telemetry does not
re-classify — it reads the category straight off the runner's
`FailureEvidence`.

For the three provider-limit categories (`usage_limit`,
`short_rate_limit`, `provider_overloaded`), Rally attaches the bounded raw
provider response (`raw_signal`, max 256 runes) and a human-readable
`message` as the `failure_evidence` context block. Both are scrubbed
(home-prefix collapse + sensitive-key redaction + truncation) before
transmission. Non-limit categories attach no raw-signal context.

## Self-Updates

Rally features a built-in updater to fetch the latest binaries directly from GitHub Releases.
To upgrade Rally to the latest release, run:

```sh
rally update
```

If the companion `laps` binary is installed next to `rally` (the default for new installations), `rally update` will automatically upgrade `laps` to its corresponding compatible release as well.

Current Rally source and its checked-in agent workflows support `laps v0.8.1`
or newer. Rally relies on the claim file introduced in the `v0.8.x` line so a
bare `laps done` completes the lap Rally assigned, even when follow-up laps are
added to the head of the queue. Run `rally update` to install or upgrade the
bundled companion.

## Architecture

Rally is built around a few focused internal packages:

- `internal/store` — append-only JSONL files with in-memory caching and
  commit-then-truncate windowing.
- `internal/agent` — pluggable executors (Antigravity, Claude, Codex,
  Opencode, user-defined generic) sharing one prompt builder.
- `internal/relay` — deterministic agent cycling, retries, error
  resilience, freeze detection, graceful stop.
- `internal/routing` — `[routes]` parser, scheduler, override resolution.
- `cmd/rally` — Cobra CLI: `relay`, `init`, `tail`, `routes`, `update`,
  `version`, `instructions`.

## Development

We use `just` as a command runner for local development.

To list all available commands:
```sh
just
```

Common recipes include:
- `just build`: Build the `rally` binary into `bin/`.
- `just test`: Run the full test suite.
- `just check`: Check code formatting (`gofmt`) and run static analysis (`go vet`).
- `just fmt`: Automatically format all Go files.
- `just run <args>`: Compile and run the `rally` CLI with arguments.
- `just setup-hooks`: Configure the local Git hooks path.

### Running tests

If you don't have `just` installed, you can run tests directly with Go:

```sh
go test -count=1 ./...
```

### Git hooks

To catch formatting, vet, and test failures before they reach CI, enable the included Git hooks:

```sh
just setup-hooks
```
(Alternatively, run `./scripts/setup-hooks.sh` directly.)

This configures:
- **pre-commit**: Runs `just check` (vet + formatting) before staging a commit.
- **pre-push**: Runs `just test` before pushing to remote.

These hooks automatically delegate to `just` if it is installed, falling back to raw `go` commands otherwise.

## Release notes


Recent highlights — see GitHub Releases for the full history.

### v0.10.0 — Telemetry and error reporting improvements

This release improves telemetry and error reporting. Short rate-limit events are now classified as non-error (info) signals, retaining `event_kind=limit_signal` and `failure_category=short_rate_limit`, and are not reclassified as crashes or failures. This update also includes release notes listing exact historical incident IDs from Sentry, now described in New Relic/backend-neutral terms.

The historical incident IDs are:
- `RALLY-2`, `RALLY-3`, `RALLY-4`, `RALLY-6`, `RALLY-8`, `RALLY-9`, `RALLY-B`, `RALLY-C`
- `RALLY-Q`, `RALLY-K`, `RALLY-D`

### v0.9.0 — recovery role and per-run timeouts

Adds a hard per-run wall-clock budget (`run_timeout_secs`, default 75 m
across all retries) plus a secondary per-try cap (`try_timeout_secs`,
default 60 m), a bounded handoff-only resume on budget exhaustion
(`handoff_timeout_secs`, default 5 m), and a `recovery` role/route that
reconciles dirty handed-off state. RECOVERY engages on the two states that
leave a suspect half-finished tree — a dirty handoff or a handoff timeout
— is capped at two consecutive recovery runs per lap before escalating to a
`needs_user` issue, and records a structured classification
(`continue`/`discard`/`course_correct`/`repair_plan`/`needs_user`). Ordinary
failures keep routing through the existing resilience paths.

### v0.8.0 — Antigravity CLI harness

Adds the built-in `antigravity` harness with `ag`/`agy` aliases, native
`agy --print` execution, `antigravity_model` config, named model shortcuts,
and real-backend smoke coverage.

### v0.7.0 — resilient execution

Resume-aware retries, cheap in-place provider rotation, freeze detection,
opt-in liveness probes, and harness-specific error classification. Default
retry budget raised from 3 to 5. Platform support: freeze detection uses
log-mtime + connections + IO on Linux, log-mtime only on macOS, and is
disabled on Windows.

### v0.6.0 — role-aware routing

Adds `[routes]` with case-insensitive matching against lap `assignee`,
positional `:` splitting (last segment is quota only when numeric),
`--agent` override rosters with route references, and
`.rally/agents/{ASSIGNEE}.md` role files.

### v0.5.0 — harnesses and models

Namespaced `[harness.<name>]` config: named model shortcuts, user-defined
CLI agents with `command`/`model_flag`/`tail_stream`, `[laps]` and
`[fallback]` instruction sources, `schema_version = 2`.

### v0.4.0 — laps integration

Lap head-pull surfaces the `assignee` field; laps instructions injection
becomes unconditional in laps-backed mode. Progress log stays YAML.
