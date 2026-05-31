# Prayer-app-2 Rally Review Notes

- Feature branches commonly target `staging`, not `main`. Confirm before diffing, but bias toward `staging` when branch history shows it was created from `origin/staging`.
- Use `pnpm` for verification commands.
- `.laps/` is the structured planning system and should be tracked.
- `.rally/config.toml` and `.rally/agents/` are stable Rally configuration and should be tracked.
- High-churn `.rally` runtime/debug files such as `state/tries.jsonl`, `state/relays.jsonl`, `summary.jsonl`, `state/agent_status.jsonl`, hook audits, relay logs, and harness logs should be preserved locally for review but may be pruned or exported rather than carried forever in git.
- Sentry or similar observability can be used to retain historical debug logs from container-based runs. Do not use it as the live source of truth for lap scheduling or role/config state.
- Dune containers usually mount this repo at `/workspace` and persist agent data under `/persist/agent`.
- When investigating a relay from a Dune container, start with:
  - `.laps/laps.json`
  - `.rally/summary.jsonl`
  - `.rally/state/relays.jsonl`
  - `.rally/state/tries.jsonl`
  - `log_path` values from `state/tries.jsonl`
- For noisy harness logs, extract with `jq`, `rg`, or Python. Useful fields/patterns include `git reset`, `git rebase`, `git diff`, `git status`, `laps add`, `laps done`, `wrapup`, `summary`, `commit_hash`, and `files_changed`.
- Branch recovery should prefer a new branch with a suffix such as `-2`, additive commits, and rescue tags. Avoid force-pushing or rewriting unless the user explicitly asks.
