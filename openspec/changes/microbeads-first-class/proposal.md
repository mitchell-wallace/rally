## Why

Microbeads becomes rally's only first-class task tracker. Previous flexibility around `beads` (Go) and `beads_rust` is dropped: users who want those tools can wire them in via prompt-level instructions to the agent, but rally itself does not see, name, configure, or coordinate with them. There should be no remaining traces of `beads_rust` (or `beads` as a backend) anywhere in the rally codebase.

This change also refines the agent-facing protocol and the human-readable progress log:

- The relay prompt currently injects a *Header Context: Session ID, batch ID, current iteration, total iterations, and the agent name* block. Agents don't use it, it costs tokens, and it leaks rally's internal bookkeeping. Drop it.
- `rally-progress.yaml` lives at `docs/orchestration/rally-progress.yaml` despite being rally runtime data. Move it under `.rally/` and revisit shape after some real usage.
- `recent_sessions` should be `recent_runs` (entries are per-run, not per-session). Add `beads_completed` per entry so closed-bead state is unambiguous.
- Agents currently end their run by calling `rally progress` directly. That couples agent prompts to rally's CLI and conflates two distinct exit conditions (finished vs. blocked). Replace with a small `mb`-only protocol that hides the rally CLI behind hook scripts.

## Agent contract

In a microbeads-active workspace the agent is told only about `mb` commands. The exit conditions in their initial instructions are:

- **`mb done <id>`** — they finished the bead's work
- **`mb handoff`** — they hit a blocker that prevents finishing the current bead

`mb wrapup` is not part of the initial instructions; the agent learns about it from the passback of `mb done` (so the wrap-up step is contextual to having just finished a bead, not a thing they think about up front).

In a workspace without microbeads, agents call `rally progress` directly — explicitly the exception to the "agents don't touch rally CLI" rule, since there is no `mb` to mediate.

## What Changes

### Drop all `beads` / `beads_rust` traces
- Delete `beads_rust` and `beads` (Go) support, references, capability matrix entries, prompt-template hints, and CLI help text — anywhere in the codebase
- Rename the existing `Beads string` field on `V2Config` (currently controls microbeads-instruction injection: `true | false | auto`) to live under a clearly-microbeads-scoped key. Either flatten to `microbeads_instructions = "auto"` at config root or move into a `[microbeads]` table — TBD during implementation, both work; the rename is what matters

### Microbeads detection
- At rally startup, detect microbeads activity by looking for `.beads/mb.json` discoverable from cwd (per microbeads SPEC §Storage). The presence of `mb` on PATH or a `.beads/` directory alone is **not** sufficient — users may have microbeads installed but be using regular `beads`, `beads_rust`, or some other fork in the present repo, and those tools also live under `.beads/`. The `mb.json` file is microbeads-specific.
- "Microbeads-backed" mode when `.beads/mb.json` exists; "no-backend" mode otherwise
- The mode determines hook installation, prompt instructions, and whether `rally progress` is private or public

### Hooks installed by rally (microbeads-backed mode only)

Microbeads is rally-agnostic by design (`../microbeads/SPEC.md`); integration is purely via `mb-hooks.json` entries. Rally's installer writes/maintains:

- **`mb done` after-hook** (`passback: true`) — runs a small script that:
  1. Records the just-closed bead ID against the active run via internal `rally progress --record-bead $id`
  2. Prints back to the agent (passback) the next-step instructions:
     ```
     ✓ Marked done. Wrap up this run before exiting:
       mb wrapup --summary "<one-line summary>" [--followup "<note>"] ...
     ```
  `mb done` stays an isolated, self-contained command; the wrap-up step is the agent's *next* call, driven by these instructions.

- **`mb wrapup` hook-only command** — the agent's data-entry point for run-end progress. The hook script:
  1. Forwards args (`--summary "..."`, repeatable `--followup "..."`) to internal `rally progress --finalise ...`
  2. `rally progress` writes the entry to `.rally/progress.yaml` (renamed location, see below) with `summary`, `followups`, and the accumulated `beads_completed`
  3. Prints back: `Progress recorded.`

- **`mb handoff` hook-only command** — the agent's blocker escape hatch. Two-step protocol on the same command name:
  - **First call**, args empty or just `--init` (whatever the agent types when they realise they're stuck): the hook script flips a per-run handoff flag in `.rally/run-state.json`, then prints back instructions tuned to the handoff scenario, e.g.:
    ```
    Handoff initiated for the current bead. To finalise, run:
      mb handoff --reason "<why this is blocked>" \
                 --followup "<what needs to happen first>" \
                 [--followup "<another follow-up>"] ...
    Each --followup becomes a new bead inserted at the queue HEAD so the blocker
    is addressed before this bead is retried.
    ```
  - **Second call**, args populated: the hook script
    1. For each `--followup`, calls `mb add head --title "<text>"` so blockers jump the queue
    2. Calls internal `rally progress --handoff --reason "..." --followup "..."` to write the handoff entry
    3. Clears the handoff flag in `.rally/run-state.json`
    4. Prints back: `Handoff recorded. Created N follow-up bead(s) at queue head. Original bead remains open for next run.`

The flag mechanism lets `rally progress`'s eventual write know whether the run ended in `done`+`wrapup` or in `handoff`, so the entry's shape is decided at write time rather than guessed.

### `rally progress` subcommand
Two visibility modes:

- **Microbeads-backed mode**: private, called only by hook scripts. Agent prompt does not mention it. Flag surface (internal):
  - `--record-bead <id>` (repeatable)
  - `--finalise --summary "..." --followup "..."` (called by `mb wrapup`)
  - `--handoff --reason "..." --followup "..."` (called by second `mb handoff`)
- **No-backend mode**: public, documented in agent prompt as the explicit exception. Flag surface (agent-facing):
  - `rally progress --summary "..." --followup "..."` — single-call run-end progress entry

The same code path serves both; only the prompt-template and CLI help differ.

### `rally-progress.yaml` move and schema updates
- Move from `docs/orchestration/rally-progress.yaml` to `.rally/progress.yaml`. Old path no longer read or written.
- One-shot copy on first run post-upgrade if old file exists; old file left in place untouched (user can git rm at leisure)
- Top-level: `recent_sessions` → `recent_runs`
- Entry-level: `session_id` → `run_id` (matches v0.2.0 `TryRecord.RunID`)
- New per-entry field, **only present when microbeads is the active backend**:
  - `beads_completed: ["mb-a3f2", "mb-b91c"]` — IDs of beads marked done during the run
  - `beads_completed: "none"` — explicit empty marker (not `null`, not `[]`) when microbeads is active and no beads were closed
  - Field omitted entirely in no-backend mode
- New optional per-entry field `handoff: { reason, followups, created_bead_ids }` populated only when the run ended via `mb handoff`
- Stays YAML; the format question is deferred for review after some usage

### Prompt template updates
- Remove the `Header Context` block entirely
- Microbeads-backed mode: instructions section mentions `mb done <id>` and `mb handoff` as the two exit conditions; does not mention `mb wrapup` (taught by `mb done` passback) or `rally progress`
- No-backend mode: instructions section mentions `rally progress --summary "..." --followup "..."` as the run-end action

## Capabilities

### New Capabilities
- `microbeads-only-integration`: First-class microbeads adapter — head-pull via `mb get head`, hook installer that maintains rally-keyed `mb-hooks.json` entries, microbeads-or-no-backend mode detection at startup
- `mb-hook-translator`: Hook scripts (`mb done` after-hook, `mb wrapup` hook-only, `mb handoff` hook-only) that translate agent-facing `mb` invocations into internal `rally progress` calls
- `progress-bead-accounting`: `beads_completed` field on every progress entry when microbeads is active, populated via the `mb done` after-hook
- `progress-handoff`: `handoff` field on entries where `mb handoff` was finalised, with reason, follow-ups, and IDs of the beads created at the queue head
- `agent-protocol-modes`: Distinct prompt-template instructions for microbeads-backed vs. no-backend mode; `rally progress` public/private toggle follows mode

### Modified Capabilities
- `relay-runner`: Next-task selection routed through microbeads adapter; prompt template no longer includes header context block; mode-specific exit-condition instructions
- `executor`: No backend selector — microbeads detection is automatic
- `progress-log`: Moved to `.rally/progress.yaml`; `recent_sessions` → `recent_runs`; `session_id` → `run_id`; `beads_completed` and `handoff` fields added; format stays YAML pending later review

## Impact

- New package: `internal/beads/microbeads/`
- New package: `internal/progress/` for the hook-driven `rally progress` subcommand and YAML rewrites
- New shell scripts shipped with rally: `mb-done-hook.sh`, `mb-wrapup-hook.sh`, `mb-handoff-hook.sh`, idempotently registered in `.beads/mb-hooks.json` under rally-keyed names; thin shell layers that forward `$@` to `rally progress` for real parsing
- New ephemeral state file: `.rally/run-state.json` for the per-run handoff flag and any future per-run scratch state (gitignored — runtime data, not durable record)
- File move: `docs/orchestration/rally-progress.yaml` → `.rally/progress.yaml`; old path ignored after move
- Codebase cleanup: remove every `beads_rust` and `beads`-as-backend reference; rename the misleading `Beads string` config field
- Risk: the `mb handoff` two-call protocol relies on the agent reading the first call's instructions and following them. Mitigation: instructions are short and explicit; the second call defaults to a clear error if no `--reason` is provided. If agents systematically skip the second call, we can collapse to a one-shot form later
- Stub entries for incomplete runs: if the agent's session ends without `mb done` finalising via `mb wrapup` and without `mb handoff` finalising, the relay loop writes a stub progress entry so `recent_runs` always grows monotonically. Stub `summary` is the **first 160 characters of the agent's final output message** (the text rally prints back to the console at run-end). `beads_completed` still reflects whatever the `mb done` after-hook accumulated (often `"none"` for stubs, but may have IDs if the agent closed beads but skipped wrapup). The explicit `"none"` makes the incomplete-run state unambiguous
- Risk: hook installer overwriting user-edited hooks — rally only touches entries it owns (keyed-prefix merge); user hooks for the same command names are preserved alongside
