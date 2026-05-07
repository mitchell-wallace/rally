# Rally

Rally is a small CLI orchestrator for running a repeatable coding loop across
multiple agent CLIs in the same repo.

It is built for people who want to rotate work across tools like Claude Code,
Codex CLI, Gemini CLI, and Opencode without manually re-running prompts,
tracking iterations, or rebuilding progress files after every pass.

## What Rally Does

- Runs one or more agent sessions against the current workspace.
- Cycles deterministically through an agent mix such as `cc:1 cx:2 ge:1`,
  or through role-aware routes selected from a lap's `assignee`.
- Accepts agent mixes either by repeating the flag or by passing one quoted
  string: `--agent "cc:1 cx:2 op:1"`.
- Stores transcripts and per-session metadata outside the repo by default.
- Auto-commits dirty workspace changes after a session completes.
- Can pull tasks from Laps when enabled.

## Supported Agent CLIs

Rally currently knows how to call these binaries if they are available on your
`PATH`:

- `claude`
- `codex`
- `gemini`
- `opencode`

Aliases for the agent mix flag:

- `cc` = Claude
- `cx` = Codex
- `ge` = Gemini
- `op` = Opencode

For Opencode runs, Rally automatically sets:

```sh
OPENCODE_PERMISSION='{"*":"allow"}'
```

## Install

Install the latest release from GitHub Releases:

```sh
curl -fsSL https://raw.githubusercontent.com/mitchell-wallace/rally/main/install.sh | sh
```

This installs `rally` into `~/.local/bin/rally`.

## Prerequisites

Before using Rally, make sure:

- You are inside a git repo.
- The agent CLIs you want to use are installed.
- Each CLI is already authenticated and usable from your shell.
- `~/.local/bin` is on your `PATH` if you installed via `install.sh`.

## Quick Start

Initialize the repo once:

```sh
rally init
```

Run a basic relay:

```sh
rally relay
```

Run multiple iterations across different CLIs:

```sh
rally relay --iterations 4 --agent "cc:1 cx:2 ge:1 op:1"
```

## Common Commands

```sh
rally relay
rally init
rally routes check
rally update
rally instructions edit
rally instructions show
rally version
```

## Agent and Override Examples

Repeat the flag:

```sh
rally relay --agent cc:1 --agent cx:2 --agent ge:1
```

Or pass the same mix as one string:

```sh
rally relay --agent "cc:1 cx:2 ge:1"
```

Named models (defined in `[harness.<name>.models]`):

```sh
rally relay --agent "cc:opus op:z"
```

Mix bare aliases, quota-bearing aliases, and named models in one string:

```sh
rally relay --agent "cc:opus cx:2 op:z"
```

Role references are valid in `--agent` only. They inline a configured
`[routes]` entry into the override roster:

```sh
rally relay --agent "SENIOR"
rally relay --agent "op:opencode-go/fancy-new-model DEFAULT:1"
```

In the second example, Rally runs the direct Opencode entry until failure,
then consumes one entry from the `default` route before returning to the
direct entry. Role references are rejected inside `[routes]` itself.

If you do not provide `--agent`, Rally defaults to a mix of:

```text
claude:1 codex:2
```

## Configuration

Rally reads `.rally/config.toml` from the workspace. Run `rally init` to
generate a starter config with sensible defaults.

Example (v2 schema):

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

[laps]
instructions_file = ".rally/laps_instructions.md"

[fallback]
instructions_file = ".rally/fallback_instructions.md"

[harness.cc.models]
opus = "claude-opus-4-7"
sonnet = "claude-sonnet-4-6"

[harness.op.models]
z = "zai-coding-plan/glm-5.1"
gk = "opencode-go/kimi-k2.6"

[routes]
default = ["cc:opus:1", "cx", "op:gk:2-4"]
SENIOR = ["cx", "cc:opus"]
JUNIOR = ["op:z:4", "op:gk:2", "ge"]
UI = ["ge:2-5", "cc:sonnet"]
VERIFY = ["cx", "cc:opus"]

[reliability]
freeze_threshold_secs = 180
liveness_probe = false
retry_budget = 5

[reliability.chars_per_token]
claude = 3.5
codex = 4.0
gemini = 4.0
opencode = 4.0
```

### `[defaults]`

Per-harness default models and relay defaults live here. A bare alias like `cc`
in a mix resolves through `[defaults].claude_model` first, then falls back to
the harness's hard-coded internal default.

| Field              | Type   | Purpose                                        |
|--------------------|--------|------------------------------------------------|
| `iterations`       | int    | Default iteration count when `--iterations` is absent |
| `mix`              | string | Default agent mix when `--agent` is absent     |
| `claude_model`     | string | Default model for the `cc`/`claude` alias      |
| `codex_model`      | string | Default model for the `cx`/`codex` alias       |
| `gemini_model`     | string | Default model for the `ge`/`gemini` alias      |
| `opencode_model`   | string | Default model for the `op`/`opencode` alias    |

### `[laps]`

| Field               | Type   | Purpose                                               |
|----------------------|--------|-------------------------------------------------------|
| `instructions_file`  | string | Path to instruction content injected in laps-backed mode. Falls back to the built-in default when absent or unreadable. |

Injection is unconditional in laps-backed mode (per v0.4.0). There is no
toggle.

### `[fallback]`

| Field               | Type   | Purpose                                               |
|----------------------|--------|-------------------------------------------------------|
| `instructions_file`  | string | Path to prompt content used in no-backend mode when no ready lap exists. Falls back to the built-in default. |

The fallback file has no effect in laps-backed mode.

### `[routes]`

`[routes]` enables role-aware routing. Route keys are matched
case-insensitively against the active lap's `assignee` value, with
`default` reserved for the no-role / no-match case. The current in-repo
`assignee` documentation lives in [openspec/HANDOFF.md](openspec/HANDOFF.md)
and [openspec/changes/laps-first-class/specs/laps-only-integration/spec.md](openspec/changes/laps-first-class/specs/laps-only-integration/spec.md).

Each route entry is one of:

- a bare harness alias such as `cx` or `ge`
- a named model such as `cc:opus` or `op:z`
- a raw `harness:model` string such as `op:opencode-go/kimi-k2.6`
- any of the above with an optional trailing quota: `:N` or `:N-M`

Quota rules:

- no quota: keep using the entry until it fails
- `:N`: rotate after exactly `N` consecutive runs
- `:N-M`: prefer rotating after `N`, but allow up to `M` if every other entry
  is exhausted or frozen

Routing priority on each iteration is:

1. `--agent` override route, if supplied
2. lap `assignee` match
3. `default`

In no-backend mode there is no lap and no `assignee`, so Rally always uses
`default`. Non-default routes still load, but they are never selected.

### Role Instruction Files

When a lap has an `assignee`, Rally looks for a matching file in
`.rally/agents/{ASSIGNEE}.md` using a case-insensitive directory scan. If a
file is found, its contents are inserted between Rally's base instructions and
the lap body. Missing files are silent. Rally treats the file contents as
opaque text; it does not parse front-matter or impose a template.

### `[harness.<name>]` — Named models and user-defined harnesses

Each harness can declare named model shortcuts under `[harness.<name>.models]`.
A mix entry of the form `<alias>:<model-name>` resolves through this table.

```toml
[harness.cc.models]
opus = "claude-opus-4-7"
sonnet = "claude-sonnet-4-6"

[harness.op.models]
z = "zai-coding-plan/glm-5.1"
gk = "opencode-go/kimi-k2.6"
```

With the above, `--agent "cc:opus op:z"` resolves to Claude with
`claude-opus-4-7` and Opencode with `zai-coding-plan/glm-5.1`.

Model names must be non-numeric identifiers. A name like `4` is rejected so
quota parsing stays unambiguous.

Built-in harnesses (`cc`/`cx`/`ge`/`op` and their full names) can declare
named models but **cannot** declare `command`, `model_flag`, `output_strategy`,
or `tail_stream`.

### User-defined harnesses

A harness that declares `command` registers a new CLI agent. This is how you
add support for a custom tool without recompiling rally.

Example — a `droid` harness:

```toml
[harness.droid]
command = ["droid", "run", "$PROMPT"]
model_flag = "--model"
output_strategy = "tail"
tail_stream = "combined"
output_lines = 40

[harness.droid.models]
default = "droid-v1"
fast = "droid-v1-turbo"
```

**`command`** is an array of strings passed to `exec`. The literal `$PROMPT` is
replaced positionally with the prompt body. If `$PROMPT` does not appear
anywhere in `command`, the prompt is piped on stdin instead. Substitution is
**positional, not shell** — no shell interpolation occurs, so shell
metacharacters in the prompt are safe.

**`model_flag`** controls how the resolved model is appended to the command:

| `model_flag` value | Behaviour                                                    |
|--------------------|--------------------------------------------------------------|
| `"--model"` (set)  | Appends `[model_flag, model]` when a model is resolved      |
| `""` (empty)       | Appends `[model]` positionally when a model is resolved     |
| omitted            | Never appends a model; harness uses its own internal default|

When `model_flag` is omitted and a non-empty model is resolved, rally logs a
one-line note that the model could not be passed to the harness.

**`tail_stream`** selects which output stream to capture: `stdout`, `stderr`,
or `combined` (default). **`output_lines`** controls how many trailing lines
to surface (default 40). The only supported `output_strategy` is `"tail"`.

`$MODEL` is **not** a recognised placeholder in `command`. If present, the
config loader rejects it with a clear error directing you to `model_flag`.

### `[reliability]`

The `[reliability]` table controls retry, freeze-detection, and liveness-probe behaviour introduced in v0.7.0.

| Field                    | Type   | Default | Purpose                                                              |
|--------------------------|--------|---------|----------------------------------------------------------------------|
| `freeze_threshold_secs`  | int    | `180`   | Seconds of log inactivity before a try is considered frozen          |
| `liveness_probe`         | bool   | `false` | Enable experimental side-channel probe for ambiguous freeze signals  |
| `retry_budget`           | int    | `5`     | Maximum retries per try before advancing to the next route entry     |

`[reliability.chars_per_token]` is an optional map of per-harness divisors used by the token estimator. When absent, the harness adapter's built-in default is used.

```toml
[reliability]
freeze_threshold_secs = 180
liveness_probe = false
retry_budget = 5

[reliability.chars_per_token]
claude = 3.5
codex = 4.0
```

**Freeze detection** is conservative by design: a try is flagged frozen only when the log file has not been modified for `freeze_threshold_secs` **and** the agent has zero active TCP connections **and** its IO byte counters have not advanced. On Linux all three conditions must hold; on macOS the connection clause is treated as satisfied (no procfs equivalent); on Windows freeze detection is disabled entirely.

When a freeze is confirmed, Rally graceful-kills the agent process group (SIGTERM → 5-second drain → SIGKILL), counts the try as retry-eligible, and re-attempts it through the resume-aware retry path if the harness supports session resume.

**Liveness probe** is opt-in and skipped for harnesses whose adapter does not declare support. The probe sends a lightweight "respond with OK" side-channel prompt when the freeze signal is ambiguous (log mtime is advancing but IO has been idle for 60 s). A successful probe clears the freeze flag and lets the try continue; failure or timeout confirms the freeze.

### Error Classification

Rally classifies harness failures using a static pattern table in `internal/reliability/patterns.go`. This table is the single place to update when a harness CLI changes its error output or a new pattern is discovered.

| Pattern                                  | Strategy            | Meaning                                      |
|------------------------------------------|---------------------|----------------------------------------------|
| opencode "API bad request" from provider | `rotate`            | Advance to the next route entry immediately  |
| gemini-cli exit 1                        | `resume + retry`    | Resume the session and retry once            |
| claude rate-limit interrupt              | `wait + resume`     | Wait for the cooldown, then resume and retry |
| codex completion despite limit warning   | `no-op`             | Treat as a successful completion             |
| unknown failure                          | `fresh restart`     | Start a new try from scratch                 |

Patterns are matched deterministically against the last N lines of the try log. New patterns are added to the `ErrorPatterns` slice; missing patterns fall through to `fresh restart`, which is the safe default.

### `schema_version`

The config file carries a top-level `schema_version` integer (currently `2`).
If absent, the file is treated as version 1 and accepted silently. On mismatch,
rally logs a one-line warning and proceeds. Every config write emits
`schema_version = 2`.

### Backwards compatibility

v0.2.x configs with root-level `claude_model`, `codex_model`, etc. still load.
If both root-level and `[defaults]` values exist, `[defaults]` takes precedence
and a deprecation note is logged. Config writes always emit the new shape
(models under `[defaults]`).

## Validating Routes

Use `rally routes check` before a relay or in CI to validate `[routes]`,
resolve named models, and catch quota syntax errors early.

```sh
rally routes check
```

The command exits non-zero on parse, resolution, or quota errors. It prints
warnings for soft problems such as a missing `default` route, and info lines
for declared non-default routes that no current lap `assignee` references.

Example Make target:

```make
routes-check:
	rally routes check
```

Example CI step:

```sh
rally routes check
go test ./...
```

### Other settings

By default Rally uses `--no-verify` for its post-run autocommit checkpoint so
repo hooks cannot block progress/logging commits. Set
`run_hooks_on_autocommit = true` if you want those fallback commits to run the
workspace's normal Git hooks.

## Project Instructions

Use project instructions when you want every session to inherit repo-specific
guidance:

```sh
rally instructions edit
```

Rally stores those instructions in its data directory and includes them in each
session prompt.

## Where Rally Stores State

By default, Rally keeps runtime state under:

```text
~/.local/share/rally
```

Useful outputs:

- Relay logs in `~/.local/share/rally/relays/relay-N.log`
- Recent relay log cache in `.rally/relays/relay-N.log`
- Workspace config in `.rally/config.toml`
- Try records in `.rally/tries.jsonl`
- Messages in `.rally/messages.jsonl`
- Agent status in `.rally/agent_status.jsonl`

## Updating

Rally has a built-in self-update command that downloads the latest compatible
release asset from GitHub Releases and replaces the current binary:

```sh
rally update
```

Rally also performs a background update check on normal startup unless
`RALLY_NO_UPDATE_CHECK=1` is set.

## How a Rally Loop Works

Each iteration:

1. Selects the active route (`--agent` override, lap `assignee`, or
   `default`) and then picks the next agent from that route.
2. Builds a prompt from your project instructions, inbox messages, and recent
   try context, plus any matching `.rally/agents/{ASSIGNEE}.md` file.
3. Runs that agent CLI in the current repo.
4. Captures a transcript and session metadata.
5. Appends filtered relay output to `~/.local/share/rally/relays/relay-N.log`
   and mirrors the latest logs into `.rally/relays/`.
6. Records the try result in `.rally/tries.jsonl`.
7. Auto-commits workspace changes if the repo became dirty.

That gives you a simple, repeatable multi-agent loop without having to manually
coordinate each pass.

## Architecture (v0.2.0)

Rally v0.2.0 uses a new internal architecture:

- **JSONL store** (`internal/store/`) — append-only JSONL files with
  in-memory caching and commit-then-truncate windowing.
- **Executor interface** (`internal/agent/`) — pluggable agents (Claude, Codex,
  Gemini, Opencode, and test fixtures) with a shared prompt builder.
- **Relay runner** (`internal/relay/`) — deterministic agent cycling, retry
  logic, error resilience (pause/freeze), and graceful stop support.
- **Cobra CLI** (`cmd/rally/main.go`) — subcommands: `relay`, `init`,
  `instructions`, `update`, `version`.

## Release Notes

### v0.7.0

**Resilient execution.** Rally gains resume-aware retries, cheap in-place provider rotation, freeze detection, opt-in liveness probes, and harness-specific error classification.

- **Resume-aware retries** — Harness adapters declare `ResumeSupported()`. When a try fails and a session ID was captured, the retry passes the harness-specific resume flag instead of restarting from scratch. Preserves `.rally/run-state.json` across resume retries; clears it on fresh-start retries.
- **Cheap rotation** — When the scheduler advances to a next entry that uses the same harness with a different model, Rally calls `RotateModel` on the existing adapter instead of tearing down and respawning the process. Falls back to teardown when the adapter does not support rotation or returns an error.
- **Freeze detection** — Uses v0.3.0 monitoring signals (log mtime, connection count, IO delta) to detect stalled tries. Default threshold is 180 seconds, tunable via `[reliability].freeze_threshold_secs`. Confirmed freezes are graceful-killed and retried via the resume path.
- **Liveness probe** — Optional side-channel prompt for ambiguous freeze signals. Gated by `[reliability].liveness_probe = true` and per-adapter capability. Default off.
- **Error classification** — Known harness error patterns are mapped to retry strategies (`rotate`, `resume + retry`, `wait + resume`, `no-op`, `fresh restart`) in `internal/reliability/patterns.go`. New patterns are added to that table.
- **Retry budget bumped** — Default per-try retry budget raised from 3 to 5, configurable via `[reliability].retry_budget`.
- **Platform notes** — Freeze detection uses log-mtime + connections + IO on Linux, log-mtime only on macOS, and is disabled on Windows.

### v0.6.0

**Role-aware routing.** Rally adds a top-level `[routes]` table in
`.rally/config.toml`, selected per iteration by `--agent` override, lap
`assignee`, then `default`.

- Route entries use positional `:` splitting with the last segment treated as a
  quota only when it is numeric (`:N`) or numeric range (`:N-M`).
- Numeric-only shortcut/model keys are rejected so route quotas stay
  unambiguous.
- `--agent` now accepts override rosters built from direct entries, named
  models, and route-name references such as `DEFAULT:1`.
- `rally routes check` validates `[routes]`, resolves shortcuts, and surfaces
  unreachable non-default routes without failing on warnings alone.
- Rally can load `.rally/agents/{ASSIGNEE}.md` case-insensitively and inject it
  into the prompt. Rally only provides the loader contract here; authoring the
  role file contents remains workspace-specific.

### v0.5.0

**Harnesses + models structure.** The config file gains namespaced sections
for per-harness model shortcuts, defaults, and user-defined harnesses.

- **`[defaults]`** section: `iterations`, `mix`, and per-harness default models
  (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`) move here
  from the file root. Root-level model fields still load with a deprecation
  note; `[defaults]` takes precedence on conflict.
- **`[harness.<name>.models]`**: declare named model shortcuts per harness.
  Use them in mixes as `cc:opus`, `op:z`, etc. Unresolved names get
  did-you-mean suggestions scoped to the same harness.
- **`[harness.<name>]` with `command`**: register a user-defined CLI agent.
  Supports `model_flag` (set / empty / omitted), `$PROMPT` positional
  substitution (or stdin fallback), `tail_stream`, and `output_lines`.
  Substitution is positional, not shell.
- **`[laps]`** and **`[fallback]`** sections for instruction content
  sources in laps-backed and no-backend modes respectively.
- **`AgentMix.Cycle`** is re-typed from `[]string` (harness aliases) to
  `[]ResolvedAgent` (typed `(harness, model)` records). External code that
  imports `internal/relay` and reads `Cycle` directly will need updating.
- **`schema_version = 2`** is emitted on every config write. Absent version is
  treated as 1; mismatch produces a warning.
- No migration of progress YAML is required — the relay label format
  round-trips through the new resolver.

### v0.4.0

- Laps integration: lap head-pull surfaces `assignee` field.
- Injection of laps instructions is unconditional in laps-backed
  mode. The legacy `Beads` flat field has been removed (no rename — the field
  is gone). Progress log remains YAML.
