# Prayer-app-2 Rally Review Notes

- Feature branches commonly target `staging`, not `main`. Confirm before diffing, but bias toward `staging` when branch history shows it was created from `origin/staging`.
- Use `pnpm` for verification commands.
- Generated Rally state lives in `.rally/` and `.laps/`. Preserve it on disk for forensics; do not delete it as cleanup. If it should not be part of PR history, ignore/untrack it rather than removing the files.
- Dune containers usually mount this repo at `/workspace` and persist agent data under `/persist/agent`.
- When investigating a relay from a Dune container, start with:
  - `.laps/laps.json`
  - `.rally/progress.yaml`
  - `.rally/relays.jsonl`
  - `.rally/tries.jsonl`
  - `log_path` values from `tries.jsonl`
- For noisy harness logs, extract with `jq`, `rg`, or Python. Useful fields/patterns include `git reset`, `git rebase`, `git diff`, `git status`, `laps add`, `laps done`, `wrapup`, `summary`, `commit_hash`, and `files_changed`.
- Branch recovery should prefer a new branch with a suffix such as `-2`, additive commits, and rescue tags. Avoid force-pushing or rewriting unless the user explicitly asks.
