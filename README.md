# Rally

Rally is a small CLI orchestrator for running a repeatable coding loop across
multiple agent CLIs in the same repo.

It is built for people who want to rotate work across tools like Claude Code,
Codex CLI, Gemini CLI, and Opencode without manually re-running prompts,
tracking iterations, or rebuilding progress files after every pass.

## What Rally Does

- Runs one or more agent sessions against the current workspace.
- Cycles deterministically through an agent mix such as `cc:1 cx:2 ge:1`.
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
rally update
rally instructions edit
rally instructions show
rally version
```

## Agent Mix Examples

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

Mix bare aliases, weighted aliases, and named models in one string:

```sh
rally relay --agent "cc:opus cx:2 op:z"
```

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

[microbeads]
instructions_file = ".rally/mb_instructions.md"

[fallback]
instructions_file = ".rally/fallback_instructions.md"
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

### `[microbeads]`

| Field               | Type   | Purpose                                               |
|----------------------|--------|-------------------------------------------------------|
| `instructions_file`  | string | Path to instruction content injected in microbeads-backed mode. Falls back to the built-in default when absent or unreadable. |

Injection is unconditional in microbeads-backed mode (per v0.4.0). There is no
toggle.

### `[fallback]`

| Field               | Type   | Purpose                                               |
|----------------------|--------|-------------------------------------------------------|
| `instructions_file`  | string | Path to prompt content used in no-backend mode when no ready bead exists. Falls back to the built-in default. |

The fallback file has no effect in microbeads-backed mode.

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

1. Picks the next agent from the configured mix.
2. Builds a prompt from your project instructions, inbox messages, and recent
   try context.
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
- **`[microbeads]`** and **`[fallback]`** sections for instruction content
  sources in microbeads-backed and no-backend modes respectively.
- **`AgentMix.Cycle`** is re-typed from `[]string` (harness aliases) to
  `[]ResolvedAgent` (typed `(harness, model)` records). External code that
  imports `internal/relay` and reads `Cycle` directly will need updating.
- **`schema_version = 2`** is emitted on every config write. Absent version is
  treated as 1; mismatch produces a warning.
- No migration of progress YAML is required — the relay label format
  round-trips through the new resolver.

### v0.4.0

- Microbeads integration: bead head-pull surfaces `assignee` field.
- Injection of microbeads instructions is unconditional in microbeads-backed
  mode. The legacy `Beads` flat field has been removed (no rename — the field
  is gone). Progress log remains YAML.
