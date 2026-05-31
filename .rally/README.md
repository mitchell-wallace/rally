# Rally Data Directory

This directory contains rally's operational data. You can access this data
directly to understand the project's history and current state.

## Tracked Data Files (source of truth, git-tracked)
- `summary.jsonl` — Run summaries and lap completions (replaces progress.yaml)

## State Data Files (ephemeral/machine-churned, un-tracked)
- `state/tries.jsonl` — One line per agent execution attempt
- `state/messages.jsonl` — Inbox messages for agents
- `state/relays.jsonl` — Relay session records
- `state/agent_status.jsonl` — Agent pause/freeze state history

## Quick Reference for Agents
- View recent tries (last 10): `tail -10 .rally/state/tries.jsonl | jq .`
- View pending messages: `cat .rally/state/messages.jsonl | jq 'select(.status=="pending")'`
- View current relay status: `tail -1 .rally/state/relays.jsonl | jq .`
- Counts: `wc -l .rally/state/*.jsonl`

## Config
- `config.toml` — Agent model configuration and runtime settings
