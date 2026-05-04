## Why

Rally's current bead-tracker support is a multi-backend (`beads`, `beads_rust`, microbeads) auto-detection mess that confuses both the codebase and the agent prompt. Laps (formerly known as microbeads) is the only tracker rally maintains active integration with, and its hook system is the cleanest integration surface available. Promoting laps to first-class — and removing every other backend reference — lets us tighten the prompt loop, formalise a `laps`-only agent contract, and clean up the human-facing progress log.

## What Changes

### Laps becomes the only first-class tracker
- Drop all `beads` and `beads_rust` references from the codebase — code, prompts, config, capability docs
- Rename the misleadingly-named `Beads string` config field to `LapsInstructions string` at the struct root. Update the default config block accordingly.
- Detect laps by TWO conditions: `.laps/laps.json` discoverable from cwd (the laps-specific marker per its SPEC) AND `laps` binary available on PATH. Both must be true.
- Representation: a simple `LapsEnabled bool` on the runner config — not a mode enum
- Two operating states: **laps enabled** when both conditions met; **no-backend** otherwise

### Agent contract: `laps` only (with one exception)
- When laps is enabled, agent prompts mention only `laps` commands. Agents never see `rally` CLI syntax
- Initial exit conditions in the prompt: `laps done <id>` (finished) and `laps handoff` (blocked)
- `laps wrapup` is taught contextually by `laps done`'s passback, not in initial instructions
- When laps is disabled, agents call `rally progress` directly — the explicit, documented exception

### Hook scripts rally installs (laps-enabled only)
- `laps done` after-hook (passback) — records the closed lap ID against the active run, then prints next-step `laps wrapup` instructions to the agent
- `laps handoff` hook-only command — single call that sets `RALLY_HANDOFF_STATE=1` and prints handoff-tuned instructions directing agent to call `laps wrapup` with summary/followups
- `laps wrapup` hook-only command — checks `RALLY_HANDOFF_STATE`: if `0`/missing, forwards to `rally progress --complete`; if `1`, resets to `0` and forwards to `rally progress --handoff` (which creates laps at queue head per `--followup`)

All hook scripts are thin shell layers that forward `$@` to an internal `rally progress` subcommand for real parsing. Rally only owns hook entries it keys with `rally:` prefix; user-edited hooks are preserved. Scripts are embedded via `//go:embed` and written to `.laps/hooks/rally/` in the workspace.

### Progress log refresh
- Progress log lives at `.rally/progress.yaml` (no migration from legacy location)
- Top-level array: `recent_runs`. Per-entry identifier: `run_id`
- New per-entry field `laps_completed`, present only when laps is enabled: list of lap IDs closed during the run, or the explicit string `"none"`
- New optional per-entry field `handoff` populated only when the handoff path finalised — captures summary, follow-ups, created lap IDs
- **Stub entries** when an agent ends without finalising via `laps wrapup` or `rally progress --complete`: relay loop writes the entry with `summary` set to the first 160 characters of the agent's final console-printed output. Guarantees `recent_runs` grows monotonically

### Prompt template
- Remove the `Header Context: Session ID, batch ID, current iteration, total iterations, and the agent name` block entirely — agents don't use it, it costs tokens, it leaks bookkeeping
- Laps-enabled prompt mentions `laps done` and `laps handoff` as exit conditions
- No-backend prompt mentions `rally progress --summary "..." --followup "..."` as the run-end action

## Capabilities

### New Capabilities
- `laps-only-integration`: Mode detection via `.laps/laps.json` + `laps` on PATH, head-pull adapter via `laps get head`, hook installer that maintains rally-keyed entries in `.laps/hooks.json`, scripts written to `.laps/hooks/rally/`
- `laps-hook-translator`: Hook scripts (`laps done` after-hook, `laps handoff` single-call with state flag, `laps wrapup` with handoff routing) that translate agent-facing `laps` invocations into internal `rally progress` calls
- `progress-log`: `.rally/progress.yaml` location, `recent_runs`/`run_id` schema, `laps_completed` and `handoff` fields, stub-entry rule, hook-driven `rally progress` subcommand (private when laps enabled, public when disabled)
- `agent-protocol-modes`: Distinct prompt-template instructions and `rally progress` visibility for laps-enabled vs. no-backend mode

### Modified Capabilities
_(None — prompt-template changes and stub-entry writing are captured under the new `agent-protocol-modes` and `progress-log` capabilities; the existing `relay-runner` and `executor` specs have no requirement-level behaviour changes.)_

## Impact

- New package: `internal/laps/`
- New shell scripts embedded in rally binary, written to `.laps/hooks/rally/`: `laps-done-hook.sh`, `laps-wrapup-hook.sh`, `laps-handoff-hook.sh`
- New ephemeral state file: `.rally/run-state.json` for the per-run handoff flag and recorded laps (gitignored)
- Progress log at `.rally/progress.yaml` (fresh creation, no migration)
- Codebase cleanup: removal of every `beads_rust` and `beads`-as-backend reference; removal of the `Beads string` config field
- Token usage: small reduction per try from removing the Header Context block
- Test dependency: real `laps` binary sourced from `lib/laps/` (github.com/mitchell-wallace/laps)
