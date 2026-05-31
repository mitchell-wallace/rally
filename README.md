# Rally

Rally is a small CLI orchestrator that runs a repeatable coding loop across
multiple agent CLIs in the same repo. Hand it a task (or a queue of laps),
pick a mix of agents, and Rally cycles through them — iteration after
iteration — capturing transcripts, auto-committing progress, and rotating
away from agents that fail or stall.

It pairs well with Claude Code, Codex CLI, Gemini CLI, and Opencode. You
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
- At least one of `agy`, `claude`, `codex`, `gemini`, or `opencode`
  installed and already authenticated in your shell.

## Quick start

From the root of any git repo:

```sh
rally init                  # one-time: writes .rally/config.toml + scaffolding
rally start                 # run a single iteration with the default mix
rally start --iterations 4  # run four iterations
rally start --iterations 4 --agent "ag:1 cc:1 cx:2 ge:1 op:1"  # custom mix
```

While a relay is running, watch the current try's transcript live in another
shell:

```sh
rally tail              # latest try
rally tail --try 3      # specific try by id
```

If you Ctrl-C, Rally finishes the current try cleanly and exits. The next
`rally start` from the same workspace asks whether to resume the unfinished
relay or start fresh; `--resume` and `--new` skip the prompt.

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

## Driving Rally

### Supported built-in harnesses and aliases

| Alias      | Full name     | Binary    |
|------------|---------------|-----------|
| `ag`/`agy` | `antigravity` | `agy`     |
| `cc`       | `claude`      | `claude`  |
| `cx`       | `codex`       | `codex`   |
| `ge`       | `gemini`      | `gemini`  |
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
rally start --agent cc:1 --agent cx:2 --agent ge:1
rally start --agent "cc:1 cx:2 ge:1"
```

**Bare aliases in `--agent` round-robin one at a time.** `--agent "cc ge op"`
is equivalent to `cc:1 ge:1 op:1`: claude → gemini → opencode → claude → …
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

Rally reads `.rally/config.toml` from the workspace. `rally init` writes a
starter config with sensible defaults; you can edit it any time.
`rally init roles` extends that starter setup with role routes for
`junior`, `senior`, `ui`, and `verify`, plus matching markdown files under
`.rally/agents/`.

```toml
schema_version = 2
laps_instructions = ""
run_hooks_on_autocommit = false
data_dir = ""

[defaults]
iterations = 1
mix = "cc cx"
claude_model = "claude-opus-4.7"
codex_model = "gpt-5.4"
gemini_model = "gemini-3.1-pro-preview"
opencode_model = "zai-coding-plan/glm-5.1"
antigravity_model = "Gemini 3.5 Flash (High)"

[laps]
instructions_file = ".rally/laps_instructions.md"

[fallback]
instructions_file = ".rally/fallback_instructions.md"

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
JUNIOR  = ["op:z:4", "op:gk:2", "ge:1"]

[reliability]
freeze_threshold_secs = 180
liveness_probe        = false
retry_budget          = 5
```

### `[defaults]`

| Field                | Type   | Purpose                                              |
|----------------------|--------|------------------------------------------------------|
| `iterations`         | int    | Default iterations when `--iterations` is absent     |
| `mix`                | string | Default agent mix when `--agent` is absent           |
| `antigravity_model`  | string | Model label for the `ag`/`agy`/`antigravity` alias   |
| `claude_model`       | string | Model for the `cc`/`claude` alias                    |
| `codex_model`        | string | Model for the `cx`/`codex` alias                     |
| `gemini_model`       | string | Model for the `ge`/`gemini` alias                    |
| `opencode_model`     | string | Model for the `op`/`opencode` alias                  |

A bare alias like `cc` in a mix resolves through `[defaults].claude_model`
first, then falls back to the harness's hard-coded internal default.

### `[laps]` and `[fallback]`

| Section      | Field               | Purpose                                                          |
|--------------|---------------------|------------------------------------------------------------------|
| `[laps]`     | `instructions_file` | Prompt body injected on every laps-backed iteration              |
| `[fallback]` | `instructions_file` | Prompt body in no-backend mode when no ready lap exists          |

Both fall back to a built-in default if the file is missing or unreadable.
Injection in laps-backed mode is unconditional (per v0.4.0 — no toggle).
The fallback file is ignored in laps-backed mode.

### `[routes]` — role-aware routing

`[routes]` enables role-aware routing. Route keys are matched
case-insensitively against the active lap's `assignee` value, with
`default` reserved for the no-role / no-match case.

Each entry is one of:

- a bare harness alias such as `cx` or `ge`
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
  (`--agent "cc ge"` ⇒ `cc:1 ge:1`).
- In `[routes]`, bare aliases mean "stay until failure". If you want
  one-iteration round-robin from `[routes]`, you must add `:1`:

```toml
# Round-robin: one try per agent, then advance
default = ["cc:1", "ge:1", "op:1"]

# Stick on claude until it fails, then gemini until it fails, …
default = ["cc", "ge", "op"]
```

This split is intentional: command-line mixes are usually short and
exploratory (you want to try a roster), while routes are usually long-lived
preferences ("use claude for this role, fall back to gemini if it dies").

#### Selection priority

Routing on each iteration is:

1. `--agent` override route, if supplied.
2. Lap `assignee` matches a route name (case-insensitive).
3. `default`.

In no-backend mode there is no lap and no `assignee`, so Rally always uses
`default`. Non-default routes still load and validate, but are never
selected.

#### Role instruction files

When a lap has an `assignee`, Rally looks for
`.rally/agents/{ASSIGNEE}.md` using a case-insensitive directory scan. If a
file is found, its contents are inserted between Rally's base instructions
and the lap body. Missing files are silent. Rally treats the file contents
as opaque text — no front-matter parsing, no template.

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

Built-in harnesses (`ag`/`cc`/`cx`/`ge`/`op`) can declare named models but
**cannot** declare `command`, `model_flag`, `output_strategy`, or
`tail_stream`.

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

Tunes retry, freeze detection, and the liveness probe.

| Field                    | Type | Default | Purpose                                                            |
|--------------------------|------|---------|--------------------------------------------------------------------|
| `freeze_threshold_secs`  | int  | `180`   | Seconds of log inactivity before a try is considered frozen        |
| `liveness_probe`         | bool | `false` | Experimental side-channel probe for ambiguous freeze signals       |
| `retry_budget`           | int  | `5`     | Maximum retries per try before advancing to the next route entry   |

`[reliability.chars_per_token]` is an optional per-harness map of divisors
used by the token estimator. Defaults are baked into each harness adapter.

```toml
[reliability]
freeze_threshold_secs = 180
liveness_probe        = false
retry_budget          = 5

[reliability.chars_per_token]
claude = 3.5
codex  = 4.0
```

Freeze detection is conservative. A try is flagged frozen only when the
log file has not been modified for `freeze_threshold_secs` **and** the
agent has zero active TCP connections **and** its IO byte counters have
not advanced. On Linux all three conditions must hold; on macOS the
connection clause is treated as satisfied (no procfs equivalent); on
Windows freeze detection is disabled. Confirmed freezes are graceful-killed
(SIGTERM → 5-second drain → SIGKILL) and retried through the resume-aware
retry path.

The liveness probe is opt-in and skipped for harnesses whose adapter does
not support it. It sends a lightweight "respond with OK" prompt when the
freeze signal is ambiguous (mtime advancing but IO idle for 60 s). A
successful probe clears the freeze flag.

### Error classification

Rally maps known harness failures to retry strategies via a static table
in `internal/reliability/patterns.go`.

| Pattern                                  | Strategy            | Meaning                                      |
|------------------------------------------|---------------------|----------------------------------------------|
| opencode "API bad request" from provider | `rotate`            | Advance to the next route entry immediately  |
| gemini-cli exit 1                        | `resume + retry`    | Resume the session and retry once            |
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
| `.rally/config.toml`                                  | Workspace config                        |
| `.rally/state/tries.jsonl`                            | Try records (read by `rally tail`)      |
| `.rally/state/messages.jsonl`                         | Inbox messages                          |
| `.rally/state/agent_status.jsonl`                     | Agent status events                     |
| `.rally/state/summary.jsonl`                          | Run summaries and lap completions       |
| `.rally/instructions.md`                              | Project instructions                    |
| `.rally/agents/{ROLE}.md`                             | Role-specific instructions              |

The `<repo>` segment is `<basenamePrefix>-<hash>`, derived from the
workspace path so multiple checkouts under one data dir never collide.

## Commands

```sh
rally start              # start or resume a relay
rally init               # initialise .rally/ in the current repo
rally init roles         # add default role routes and .rally/agents/*.md
rally tail [--try N]     # follow a try's log
rally routes check       # validate [routes]
rally instructions edit  # edit project instructions
rally instructions show
rally update             # self-update from GitHub Releases
rally version            # print version (vX.Y.Z, vX.Y.Z-dev for source builds)
```

## Telemetry

Rally includes opt-in error reporting via Sentry to help improve reliability. It captures infrastructure and agent-class errors (e.g. rate limits, crashes, stall timeouts) but rigorously scrubs sensitive data.

- **To opt in:** Configure your DSN in `.rally/config.toml` or set the `SENTRY_DSN` environment variable:
  ```toml
  [telemetry]
  sentry_dsn = "https://your-dsn-key@sentry.io/project-id"
  ```
  The `SENTRY_DSN` environment variable takes precedence over the config file if both are set.
- **Kill switch:** You can forcefully disable all telemetry by setting `RALLY_TELEMETRY=0` in your environment.
- **What is sent:** Rally sends structured error signals for operator-worthy failures (e.g., panics, non-zero command exits, agent pause events, freeze detections, stall timeouts, and lap-integrity violations). Spans include execution metadata like `relay_id`, `run_id`, `try_id`, `role`, `runner`, `repo`, `lap_id`, and prompt metrics (total prompt size and a per-source breakdown of tokens).
- **What is NOT sent:** Rally NEVER sends your task description, codebase context, file contents, or agent transcripts. All potentially sensitive string fields (such as `prompt`, `output`, `transcript`, and the contents of `current_task.md`) are aggressively dropped or scrubbed locally before transmission.

## Self-Updates

Rally features a built-in updater to fetch the latest binaries directly from GitHub Releases.
To upgrade Rally to the latest release, run:

```sh
rally update
```

If the companion `laps` binary is installed next to `rally` (the default for new installations), `rally update` will automatically upgrade `laps` to its corresponding compatible release as well.

## Architecture

Rally is built around a few focused internal packages:

- `internal/store` — append-only JSONL files with in-memory caching and
  commit-then-truncate windowing.
- `internal/agent` — pluggable executors (Antigravity, Claude, Codex,
  Gemini, Opencode, user-defined generic) sharing one prompt builder.
- `internal/relay` — deterministic agent cycling, retries, error
  resilience, freeze detection, graceful stop.
- `internal/routing` — `[routes]` parser, scheduler, override resolution.
- `cmd/rally` — Cobra CLI: `relay`, `init`, `tail`, `routes`, `update`,
  `version`, `instructions`.

## Release notes

Recent highlights — see GitHub Releases for the full history.

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
