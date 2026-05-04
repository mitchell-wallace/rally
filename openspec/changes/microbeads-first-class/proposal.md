## Why

Rally's current bead-tracker support is a multi-backend (`beads`, `beads_rust`, microbeads) auto-detection mess that confuses both the codebase and the agent prompt. Microbeads is the only tracker rally maintains active integration with, and its hook system is the cleanest integration surface available. Promoting microbeads to first-class — and removing every other backend reference — lets us tighten the prompt loop, formalise an `mb`-only agent contract, and clean up the human-facing progress log.

## What Changes

### Microbeads becomes the only first-class tracker
- Drop all `beads` and `beads_rust` references from the codebase — code, prompts, config, capability docs
- Rename the misleadingly-named `Beads string` config field to `MicrobeadsInstructions string` at the struct root. Update the default config block accordingly.
- Detect microbeads by TWO conditions: `.beads/mb.json` discoverable from cwd (the microbeads-specific marker per its SPEC) AND `mb` binary available on PATH. Both must be true.
- Representation: a simple `MicrobeadsEnabled bool` on the runner config — not a mode enum
- Two operating states: **microbeads enabled** when both conditions met; **no-backend** otherwise

### Agent contract: `mb` only (with one exception)
- When microbeads is enabled, agent prompts mention only `mb` commands. Agents never see `rally` CLI syntax
- Initial exit conditions in the prompt: `mb done <id>` (finished) and `mb handoff` (blocked)
- `mb wrapup` is taught contextually by `mb done`'s passback, not in initial instructions
- When microbeads is disabled, agents call `rally progress` directly — the explicit, documented exception

### Hook scripts rally installs (microbeads-enabled only)
- `mb done` after-hook (passback) — records the closed microbead ID against the active run, then prints next-step `mb wrapup` instructions to the agent
- `mb handoff` hook-only command — single call that sets `RALLY_HANDOFF_STATE=1` and prints handoff-tuned instructions directing agent to call `mb wrapup` with summary/followups
- `mb wrapup` hook-only command — checks `RALLY_HANDOFF_STATE`: if `0`/missing, forwards to `rally progress --complete`; if `1`, resets to `0` and forwards to `rally progress --handoff` (which creates microbeads at queue head per `--followup`)

All hook scripts are thin shell layers that forward `$@` to an internal `rally progress` subcommand for real parsing. Rally only owns hook entries it keys with `rally:` prefix; user-edited hooks are preserved. Scripts are embedded via `//go:embed` and written to `.beads/hooks/rally/` in the workspace.

### Progress log refresh
- Progress log lives at `.rally/progress.yaml` (no migration from legacy location)
- Top-level array: `recent_runs`. Per-entry identifier: `run_id`
- New per-entry field `microbeads_completed`, present only when microbeads is enabled: list of microbead IDs closed during the run, or the explicit string `"none"`
- New optional per-entry field `handoff` populated only when the handoff path finalised — captures summary, follow-ups, created microbead IDs
- **Stub entries** when an agent ends without finalising via `mb wrapup` or `rally progress --complete`: relay loop writes the entry with `summary` set to the first 160 characters of the agent's final console-printed output. Guarantees `recent_runs` grows monotonically

### Prompt template
- Remove the `Header Context: Session ID, batch ID, current iteration, total iterations, and the agent name` block entirely — agents don't use it, it costs tokens, it leaks bookkeeping
- Microbeads-enabled prompt mentions `mb done` and `mb handoff` as exit conditions
- No-backend prompt mentions `rally progress --summary "..." --followup "..."` as the run-end action

## Capabilities

### New Capabilities
- `microbeads-only-integration`: Mode detection via `.beads/mb.json` + `mb` on PATH, head-pull adapter via `mb get head`, hook installer that maintains rally-keyed entries in `.beads/mb-hooks.json`, scripts written to `.beads/hooks/rally/`
- `mb-hook-translator`: Hook scripts (`mb done` after-hook, `mb handoff` single-call with state flag, `mb wrapup` with handoff routing) that translate agent-facing `mb` invocations into internal `rally progress` calls
- `progress-log`: `.rally/progress.yaml` location, `recent_runs`/`run_id` schema, `microbeads_completed` and `handoff` fields, stub-entry rule, hook-driven `rally progress` subcommand (private when microbeads enabled, public when disabled)
- `agent-protocol-modes`: Distinct prompt-template instructions and `rally progress` visibility for microbeads-enabled vs. no-backend mode

### Modified Capabilities
_(None — prompt-template changes and stub-entry writing are captured under the new `agent-protocol-modes` and `progress-log` capabilities; the existing `relay-runner` and `executor` specs have no requirement-level behaviour changes.)_

## Impact

- New package: `internal/microbeads/`
- New shell scripts embedded in rally binary, written to `.beads/hooks/rally/`: `mb-done-hook.sh`, `mb-wrapup-hook.sh`, `mb-handoff-hook.sh`
- New ephemeral state file: `.rally/run-state.json` for the per-run handoff flag and recorded microbeads (gitignored)
- Progress log at `.rally/progress.yaml` (fresh creation, no migration)
- Codebase cleanup: removal of every `beads_rust` and `beads`-as-backend reference; removal of the `Beads string` config field
- Token usage: small reduction per try from removing the Header Context block
- Test dependency: real `mb` binary sourced from `lib/mb/` (github.com/mitchell-wallace/microbeads)
