## Context

Rally currently supports `beads` (Go), `beads_rust`, and microbeads as task-tracker backends, with auto-detection picking whichever is installed. In practice this has produced churn: prompt templates that mention multiple bead variants confuse agents; the `Beads string` config field with values `"true"|"false"|"auto"` reads as a backend selector but actually toggles instruction injection; backends other than microbeads see no active integration work.

Rally also ships a human-readable progress log at `docs/orchestration/rally-progress.yaml` that pre-dates the v0.2.0 JSONL store (`.rally/{tries,relays,messages,agent_status}.jsonl`). The two coexist and serve different audiences — the JSONL files are durable system state; the YAML is a curated agent/human-facing summary.

The `rally progress` CLI is the only place agents are taught to call rally directly. That couples agent prompts to rally's CLI surface and handles the "finished" and "blocked" exit conditions identically.

## Goals / Non-Goals

**Goals:**
- One first-class tracker (microbeads), zero references to others in the codebase
- Hook-based integration via `mb-hooks.json` — no rally-side daemon, no microbeads modifications
- Agents when microbeads is enabled never see `rally` CLI syntax in their prompt
- Distinct user-facing flows for "finished" vs. "blocked" run-end states
- Progress log records microbead-completion data unambiguously and grows monotonically across runs
- Tests use real `mb` binary (sourced from github.com/mitchell-wallace/microbeads at `lib/mb/`)

**Non-Goals:**
- Maintaining `beads` or `beads_rust` compatibility — users who want them can prompt-instruct agents directly without rally's involvement
- Modifying microbeads — `mb-hooks.json` is the only interface; microbeads stays rally-agnostic per its SPEC
- Re-deciding the progress log format (YAML vs JSON) — defer until usage data accrues
- Migrating legacy progress yaml — `.rally/progress.yaml` is fresh; old file is irrelevant
- Attempting to detect garbage output or enforce per-microbead quality gates — that lives in the v0.6.0+ role workflow

## Decisions

### Detect microbeads by BOTH `.beads/mb.json` AND `mb` on PATH
**Chosen**: Microbeads is enabled iff `.beads/mb.json` is discoverable from cwd per the microbeads SPEC AND the `mb` binary is available on PATH. Both conditions required.

**Alternative considered**: `.beads/mb.json` alone is sufficient.

**Why**: Rally shells out to `mb` for head-pull, hook registration, and microbead creation. If `mb` isn't available, none of those operations work. Requiring both ensures the integration surface is actually functional, not just structurally present.

### Simple bool, not a mode enum
**Chosen**: `MicrobeadsEnabled bool` on the runner config. Code branches on this bool directly.

**Alternative considered**: A `Mode` enum with `MicrobeadsBacked` and `NoBackend` variants.

**Why**: There are only two states and no plan for a third. A bool is simpler to pass, branch on, and test. If a third mode ever appears (unlikely given the "one tracker" goal), it can be refactored then.

### Agent contract is `mb`-only when microbeads is enabled
**Chosen**: Agents with microbeads enabled see only `mb` commands in their prompt. Hook scripts translate to internal `rally progress` calls. When microbeads is disabled, agents call `rally progress` directly as the explicit exception.

**Alternative considered**: Agents always call `rally progress`; microbeads is an internal detail.

**Why**: Two reasons. First, agents already learn `mb done` to close microbeads — making it the entry point for both microbead state *and* run state collapses two surfaces into one. Second, mb-hooks fire deterministically per command, which gives rally a structural seam (the hook script) to capture run state without needing the agent to remember a separate CLI. The no-backend exception exists because there's no `mb` to mediate.

### `mb wrapup` is taught contextually, not up-front
**Chosen**: Initial prompt instructions name only `mb done` and `mb handoff` as exit conditions. The `mb done` after-hook's passback teaches `mb wrapup` once the agent has actually finished a microbead.

**Alternative considered**: Initial instructions list `mb done`, `mb wrapup`, `mb handoff` as a triplet up front.

**Why**: Up-front listing creates ambiguity — agents see "wrapup" and may try it standalone, or skip `mb done` thinking wrapup subsumes it. Context-driven teaching ties wrapup to the moment it's needed (right after closing a microbead) and keeps the initial prompt smaller. The `mb done` → wrapup chain becomes an obvious narrative.

### `mb handoff` is a single call that directs to `mb wrapup`
**Chosen**: `mb handoff` sets `RALLY_HANDOFF_STATE=1` in `.rally/run-state.json` and prints instructions directing the agent to call `mb wrapup --summary "..." --followup "..."`. The wrapup hook checks this state flag: if set, it routes to the handoff path (which creates microbeads at queue head per `--followup`). This means wrapup is always the data-entry terminal, regardless of completion or handoff.

**Alternative considered (a)**: Two-call `mb handoff` protocol where first call sets flag and second call with `--reason`/`--followup` does the work.
**Alternative considered (b)**: Single-call `mb handoff` that takes all args and does everything itself.

**Why**: The single-call-to-wrapup pattern keeps `mb wrapup` as the universal "provide your summary and followups" terminal. Agents have one data-entry surface regardless of exit path. The handoff hook's only job is to signal intent and teach the agent what to put in wrapup. This is simpler than a two-call protocol and avoids inventing arg parsing in the handoff hook.

### Followups from handoff-path wrapup go to queue head
**Chosen**: When wrapup routes through the handoff path, each `--followup` is inserted via `mb add head` so blockers jump the queue.

**Alternative considered**: `mb add tail` (microbeads' default behaviour).

**Why**: Handoff means the current microbead can't proceed until the blocker is addressed. Putting the blocker at the tail of the queue means rally would keep retrying the original microbead in subsequent runs, all blocked, before reaching the unblock task. Head insertion makes "address blockers first" the default.

### Progress log at `.rally/progress.yaml`, no migration
**Chosen**: Fresh file at `.rally/progress.yaml`. No copy from legacy location. No schema migration of old keys.

**Alternative considered**: One-shot copy from `docs/orchestration/rally-progress.yaml` with key renames.

**Why**: The legacy file was a different format serving a different era of rally. Carrying forward stale entries adds complexity for no user value — operators can reference the old file if they want history. Starting fresh keeps the code simple and the progress log relevant.

### Stub entries derive summary from the agent's final console output
**Chosen**: When an agent ends without finalising via `mb wrapup` or `rally progress --complete`, the relay loop writes a stub progress entry with `summary` = first 160 characters of the agent's final console-printed message.

**Alternative considered (a)**: Skip the entry entirely (let `recent_runs` have gaps).
**Alternative considered (b)**: Derive summary from the JSONL store's `TryRecord.Summary`.

**Why**: Gaps in `recent_runs` make incomplete runs invisible — exactly when they're most important to surface. The agent's final console output is whatever rally already prints back to the operator at run-end, so the data is there without new plumbing. 160 chars matches the typical first-line length and stays scannable in YAML.

### Consolidated `progress-log` capability
**Chosen**: Microbead-completion accounting, handoff entries, file location, schema, and stub-entry behaviour all live under one `progress-log` capability spec.

**Alternative considered**: Separate capabilities for each feature.

**Why**: All are schema/behaviour features of the same artifact (`.rally/progress.yaml`). Splitting them invites cross-capability inconsistency at archive time.

### Hook installation is implicit, with user notification
**Chosen**: Hook installation runs implicitly on `rally relay` startup when microbeads is enabled (no separate `rally hooks install` subcommand). The first run that installs or updates rally-keyed entries SHALL notify the operator with the paths of the installed hook scripts.

**Alternative considered**: Explicit `rally hooks install` / `rally hooks uninstall` subcommands; silent implicit install.

**Why**: Implicit-on-relay keeps the happy path frictionless. The notification matters because users may already have hooks for other tools. Showing paths to rally's installed scripts lets operators distinguish rally hooks from other tools' hooks, and gives them files to inspect or remove. The notification fires only when entries change — steady-state runs stay quiet.

### Hook scripts written to `.beads/hooks/rally/` (workspace-local)
**Chosen**: Hook scripts embedded in rally binary via `//go:embed`, written to `.beads/hooks/rally/` in the workspace, referenced from `mb-hooks.json` by relative path.

**Alternative considered**: Global location like `~/.local/share/rally/hooks/`.

**Why**: Workspace-local keeps everything self-contained. The `.beads/` directory already belongs to the microbeads ecosystem. A `hooks/rally/` subdirectory is clearly scoped. No cross-workspace pollution, no global state to manage.

### Drop the microbeads instruction toggle
**Chosen**: When microbeads is enabled, microbeads-related instructions are always injected. No toggle. The legacy `Beads string` field is removed outright.

**Alternative considered**: Keep an `auto`/`include`/`skip` toggle.

**Why**: The bool already encodes the answer — having microbeads enabled is the trigger. The toggle becomes redundant.

### `mb wrapup` requires `--summary`
**Chosen**: `mb wrapup` invocations without `--summary` are rejected with a non-zero exit and an error message surfaced back to the agent.

**Alternative considered**: Allow a no-op `mb wrapup` that records "agent had nothing to add".

**Why**: Agents that produce nothing useful are exactly the runs an operator most needs to see articulated — either as a handoff or as a stub entry. Letting `mb wrapup` succeed with no summary creates a "blank intentional" path indistinguishable from "agent forgot to summarise". The contract stays sharp.

## Risks / Trade-offs

- **Hook installer overwriting user-edited hooks** → Mitigation: rally only touches entries keyed with `rally:` prefix; the installer does idempotent diff-and-merge so user hooks coexist
- **Agents who stop without calling `mb wrapup` produce stub entries with potentially uninformative summaries** → Mitigation: explicit `"none"` for `microbeads_completed` makes incomplete runs visible; the 160-char summary is bounded; operators reviewing `recent_runs` can spot the pattern
- **`mb wrapup` arg parsing in shell scripts is fragile around quoting** → Mitigation: the shell layer just forwards `$@` to `rally progress`; rally does the real parsing in Go where shell quoting is already consumed
- **Stale handoff flag in `.rally/run-state.json` if agent crashes between `mb handoff` and `mb wrapup`** → For v0.4.0, the flag is cleared at relay-runner start of next run, so it doesn't bleed across runs
- **Real `mb` binary in tests adds a build dependency** → `lib/mb/` is vendored from github.com/mitchell-wallace/microbeads; CI builds it once

## Migration Plan

1. **Code cleanup phase**: remove all `beads_rust` and `beads`-as-backend references. Remove `Beads string` config field. Verify no test fixtures reference removed identifiers.
2. **Hook installation**: implicit on `rally relay` startup when microbeads is enabled. Rally writes the three rally-keyed entries to `.beads/mb-hooks.json` and writes embedded hook scripts to `.beads/hooks/rally/`. When entries are added or updated, rally prints a notification with the paths. Idempotent: steady-state re-runs don't duplicate and don't re-notify.
3. **Prompt template switch**: relay-runner branches on `MicrobeadsEnabled` at prompt-build time. Tests cover both branches.

Rollback: revert the release. The `.rally/progress.yaml` is left behind harmlessly. Hook entries can be removed by hand.
