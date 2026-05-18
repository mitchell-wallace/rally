# Rally Data Directory

This directory contains rally's operational data. You can access this data
directly to understand the project's history and current state.

## JSONL Data Files (source of truth, git-tracked)
- `tries.jsonl` — One line per agent execution attempt
- `messages.jsonl` — Inbox messages for agents
- `relays.jsonl` — Relay session records
- `agent_status.jsonl` — Agent pause/freeze state history

## Quick Reference for Agents
- View recent tries (last 10): `tail -10 .rally/tries.jsonl | jq .`
- View pending messages: `cat .rally/messages.jsonl | jq 'select(.status==\"pending\")'`
- View current relay status: `tail -1 .rally/relays.jsonl | jq .`
- Counts: `wc -l .rally/*.jsonl`

## Config
- `config.toml` — Agent model configuration and runtime settings
