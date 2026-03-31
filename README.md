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
- Rebuilds a repo-visible progress file at
  `docs/orchestration/rally-progress.yaml`.
- Auto-commits dirty workspace changes after a session completes.
- Supports scout mode for exploration-only passes.
- Can pull tasks from Beads when enabled.

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

Run a basic loop:

```sh
rally run "fix the failing tests without changing auth behavior"
```

Run multiple iterations across different CLIs:

```sh
rally run --iterations 4 --agent "cc:1 cx:2 ge:1 op:1" "stabilize CI failures"
```

Start a scout-only pass:

```sh
rally run --scout "find the highest-risk bugs in the release flow"
```

Open the TUI:

```sh
rally tui
```

## Common Commands

```sh
rally run [prompt...]
rally tui
rally init
rally update
rally instructions edit
rally instructions show
rally progress repair
```

## Agent Mix Examples

Repeat the flag:

```sh
rally run --agent cc:1 --agent cx:2 --agent ge:1 "reduce flaky tests"
```

Or pass the same mix as one string:

```sh
rally run --agent "cc:1 cx:2 ge:1" "reduce flaky tests"
```

If you do not provide `--agent`, Rally defaults to a mix of:

```text
claude:1 codex:2
```

## Configuration

Rally reads `rally.toml` from the workspace root.

Example:

```toml
claude_model = "sonnet"
codex_model = "o3"
gemini_model = "gemini-2.5-pro"
opencode_model = "anthropic/claude-sonnet-4"
beads = "auto"
```

You can create the file manually or let `rally init` write the initial config.

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

- Session transcripts and metadata in `~/.local/share/rally/sessions/`
- Repo progress in `docs/orchestration/rally-progress.yaml`
- Workspace config in `rally.toml`

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
2. Builds a prompt from your inline prompt or stored instructions.
3. Runs that agent CLI in the current repo.
4. Captures a transcript and session metadata.
5. Rebuilds `docs/orchestration/rally-progress.yaml`.
6. Auto-commits workspace changes if the repo became dirty.

That gives you a simple, repeatable multi-agent loop without having to manually
coordinate each pass.
