## Why

Rally's current bead-tracker support is a multi-backend (`beads`, `beads_rust`, microbeads) auto-detection mess that confuses both the codebase and the agent prompt. Microbeads is the only tracker rally maintains active integration with, and its hook system is the cleanest integration surface available. Promoting microbeads to first-class — and removing every other backend reference — lets us tighten the prompt loop, formalise an `mb`-only agent contract, and clean up the human-facing progress log.

## What Changes

### Microbeads becomes the only first-class tracker
- Drop all `beads` and `beads_rust` references from the codebase — code, prompts, config, capability docs
- Rename the misleadingly-named `Beads string` config field; microbeads-instruction injection lives under a clearly-microbeads-scoped key
- Detect microbeads by `.beads/mb.json` discoverable from cwd (the microbeads-specific marker per its SPEC). `mb` on PATH or a bare `.beads/` directory is **not** sufficient — users may have microbeads installed but be running another bead tool in this repo
- Two-mode operation: **microbeads-backed** when `.beads/mb.json` exists; **no-backend** otherwise

### Agent contract: `mb` only (with one exception)
- In microbeads-backed mode, agent prompts mention only `mb` commands. Agents never see `rally` CLI syntax
- Initial exit conditions in the prompt: `mb done <id>` (finished) and `mb handoff` (blocked)
- `mb wrapup` is taught contextually by `mb done`'s passback, not in initial instructions
- In no-backend mode, agents call `rally progress` directly — the explicit, documented exception

### Hook scripts rally installs (microbeads-backed only)
- `mb done` after-hook (passback) — records the closed bead ID against the active run, then prints next-step `mb wrapup` instructions to the agent
- `mb wrapup` hook-only command — agent's data-entry point for run-end progress (`--summary`, repeatable `--followup`)
- `mb handoff` hook-only command — two-call protocol on the same name: first call signals intent and prints handoff-tuned instructions; second call (with `--reason`/`--followup`) creates blocker beads at the queue head via `mb add head` and writes the handoff entry

All hook scripts are thin shell layers that forward `$@` to an internal `rally progress` subcommand for real parsing. Rally only owns hook entries it keys; user-edited hooks for the same commands are preserved.

### Progress log refresh
- Move `rally-progress.yaml` from `docs/orchestration/` to `.rally/progress.yaml`. Format question (YAML vs other) deferred until after some real usage
- Top-level rename: `recent_sessions` → `recent_runs`. Per-entry rename: `session_id` → `run_id`
- New per-entry field `beads_completed`, present only when microbeads is active: list of bead IDs closed during the run, or the explicit string `"none"`
- New optional per-entry field `handoff` populated only when `mb handoff` finalised — captures reason, follow-ups, created bead IDs
- **Stub entries** when an agent ends without finalising via `mb wrapup` or the second `mb handoff` call: relay loop writes the entry with `summary` set to the first 160 characters of the agent's final console-printed output. Guarantees `recent_runs` grows monotonically

### Prompt template
- Remove the `Header Context: Session ID, batch ID, current iteration, total iterations, and the agent name` block entirely — agents don't use it, it costs tokens, it leaks bookkeeping
- Microbeads-backed mode prompt mentions `mb done` and `mb handoff` as exit conditions
- No-backend mode prompt mentions `rally progress --summary "..." --followup "..."` as the run-end action

## Capabilities

### New Capabilities
- `microbeads-only-integration`: Mode detection via `.beads/mb.json`, head-pull adapter via `mb get head`, hook installer that maintains rally-keyed entries in `.beads/mb-hooks.json`
- `mb-hook-translator`: Hook scripts (`mb done` after-hook, `mb wrapup` hook-only, `mb handoff` hook-only with two-call protocol) that translate agent-facing `mb` invocations into internal `rally progress` calls
- `progress-log`: `.rally/progress.yaml` location, `recent_runs`/`run_id` schema, `beads_completed` and `handoff` fields, stub-entry rule, hook-driven `rally progress` subcommand (private in microbeads mode, public in no-backend mode)
- `agent-protocol-modes`: Distinct prompt-template instructions and `rally progress` visibility for microbeads-backed vs. no-backend mode

### Modified Capabilities
_(None — prompt-template changes and stub-entry writing are captured under the new `agent-protocol-modes` and `progress-log` capabilities; the existing `relay-runner` and `executor` specs have no requirement-level behaviour changes.)_

## Impact

- New packages: `internal/beads/microbeads/`, `internal/progress/`
- New shell scripts shipped with rally (registered idempotently in `.beads/mb-hooks.json`): `mb-done-hook.sh`, `mb-wrapup-hook.sh`, `mb-handoff-hook.sh`
- New ephemeral state file: `.rally/run-state.json` for the per-run handoff flag (gitignored)
- File move: `docs/orchestration/rally-progress.yaml` → `.rally/progress.yaml`; old file copied once on first post-upgrade run, left in place for user to git-rm
- Codebase cleanup: removal of every `beads_rust` and `beads`-as-backend reference; rename of the misleading `Beads string` config field
- Token usage: small reduction per try from removing the Header Context block
