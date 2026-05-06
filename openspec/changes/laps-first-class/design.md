## Context

Rally currently supports `beads` (Go), `beads_rust`, and `laps` as task-tracker backends, with auto-detection picking whichever is installed. In practice this has produced churn: prompt templates that mention multiple bead variants confuse agents; the `Beads string` config field with values `"true"|"false"|"auto"` reads as a backend selector but actually toggles instruction injection; backends other than laps see no active integration work. Laps (formerly microbeads) has been fully rebranded — the CLI binary is `laps` and an individual data unit is a "lap".

Rally also ships a human-readable progress log at `docs/orchestration/rally-progress.yaml` that pre-dates the v0.2.0 JSONL store (`.rally/{tries,relays,messages,agent_status}.jsonl`). The two coexist and serve different audiences — the JSONL files are durable system state; the YAML is a curated agent/human-facing summary.

The `rally progress` CLI is the only place agents are taught to call rally directly. That couples agent prompts to rally's CLI surface and handles the "finished" and "blocked" exit conditions identically.

## Goals / Non-Goals

**Goals:**
- One first-class tracker (laps), zero references to others in the codebase
- Hook-based integration via `.laps/hooks.json` — no rally-side daemon, no laps modifications
- Agents when laps is enabled never see `rally` CLI syntax in their prompt
- Distinct user-facing flows for "finished" vs. "blocked" run-end states
- Progress log records lap-completion data unambiguously and grows monotonically across runs
- Tests use a real `laps` binary from `PATH` against a fixture workspace project

**Non-Goals:**
- Maintaining `beads` or `beads_rust` compatibility — users who want them can prompt-instruct agents directly without rally's involvement
- Modifying laps — `.laps/hooks.json` is the only interface; laps stays rally-agnostic per its SPEC
- Re-deciding the progress log format (YAML vs JSON) — defer until usage data accrues
- Migrating legacy progress yaml — `.rally/progress.yaml` is fresh; old file is irrelevant
- Attempting to detect garbage output or enforce per-lap quality gates — that lives in the v0.6.0+ role workflow

## Decisions

### Detect laps by BOTH `.laps/laps.json` AND `laps` on PATH
**Chosen**: Laps is enabled iff `.laps/laps.json` is discoverable from cwd per the laps SPEC AND the `laps` binary is available on PATH. Both conditions required.

**Alternative considered**: `.laps/laps.json` alone is sufficient.

**Why**: Rally shells out to `laps` for head-pull, hook registration, and lap creation. If `laps` isn't available, none of those operations work. Requiring both ensures the integration surface is actually functional, not just structurally present.

### Simple bool, not a mode enum
**Chosen**: `LapsEnabled bool` on the runner config. Code branches on this bool directly.

**Alternative considered**: A `Mode` enum with `LapsBacked` and `NoBackend` variants.

**Why**: There are only two states and no plan for a third. A bool is simpler to pass, branch on, and test. If a third mode ever appears (unlikely given the "one tracker" goal), it can be refactored then.

### Agent contract is `laps`-only when laps is enabled
**Chosen**: Agents with laps enabled see only `laps` commands in their prompt. Hook scripts translate to internal `rally progress` calls. When laps is disabled, agents call `rally progress` directly as the explicit exception.

**Alternative considered**: Agents always call `rally progress`; laps is an internal detail.

**Why**: Two reasons. First, agents already learn `laps done` to close laps — making it the entry point for both lap state *and* run state collapses two surfaces into one. Second, laps hooks fire deterministically per command, which gives rally a structural seam (the hook script) to capture run state without needing the agent to remember a separate CLI. The no-backend exception exists because there's no `laps` to mediate.

### `laps wrapup` is taught contextually, not up-front
**Chosen**: Initial prompt instructions name only `laps done` and `laps handoff` as exit conditions. The `laps done` after-hook's passback teaches `laps wrapup` once the agent has actually finished a lap.

**Alternative considered**: Initial instructions list `laps done`, `laps wrapup`, `laps handoff` as a triplet up front.

**Why**: Up-front listing creates ambiguity — agents see "wrapup" and may try it standalone, or skip `laps done` thinking wrapup subsumes it. Context-driven teaching ties wrapup to the moment it's needed (right after closing a lap) and keeps the initial prompt smaller. The `laps done` → wrapup chain becomes an obvious narrative.

### `laps handoff` is a single call that directs to `laps wrapup`
**Chosen**: `laps handoff` sets `RALLY_HANDOFF_STATE=1` in `.rally/run-state.json` and prints instructions directing the agent to call `laps wrapup --summary "..." --followup "..."`. The wrapup hook checks this state flag: if set, it routes to the handoff path (which creates laps at queue head per `--followup`). This means wrapup is always the data-entry terminal, regardless of completion or handoff.

**Alternative considered (a)**: Two-call `laps handoff` protocol where first call sets flag and second call with `--reason`/`--followup` does the work.
**Alternative considered (b)**: Single-call `laps handoff` that takes all args and does everything itself.

**Why**: The single-call-to-wrapup pattern keeps `laps wrapup` as the universal "provide your summary and followups" terminal. Agents have one data-entry surface regardless of exit path. The handoff hook's only job is to signal intent and teach the agent what to put in wrapup. This is simpler than a two-call protocol and avoids inventing arg parsing in the handoff hook.

### Followups from handoff-path wrapup go to queue head
**Chosen**: When wrapup routes through the handoff path, each `--followup` is inserted via `laps add head` so blockers jump the queue.

**Alternative considered**: `laps add tail` (laps' default behaviour).

**Why**: Handoff means the current lap can't proceed until the blocker is addressed. Putting the blocker at the tail of the queue means rally would keep retrying the original lap in subsequent runs, all blocked, before reaching the unblock task. Head insertion makes "address blockers first" the default.

### Progress log at `.rally/progress.yaml`, no migration
**Chosen**: Fresh file at `.rally/progress.yaml`. No copy from legacy location. No schema migration of old keys.

**Alternative considered**: One-shot copy from `docs/orchestration/rally-progress.yaml` with key renames.

**Why**: The legacy file was a different format serving a different era of rally. Carrying forward stale entries adds complexity for no user value — operators can reference the old file if they want history. Starting fresh keeps the code simple and the progress log relevant.

### Stub entries derive summary from the agent's final console output
**Chosen**: When an agent ends without finalising via `laps wrapup` or `rally progress --complete`, the relay loop writes a stub progress entry with `summary` = first 160 characters of the agent's final console-printed message.

**Alternative considered (a)**: Skip the entry entirely (let `recent_runs` have gaps).
**Alternative considered (b)**: Derive summary from the JSONL store's `TryRecord.Summary`.

**Why**: Gaps in `recent_runs` make incomplete runs invisible — exactly when they're most important to surface. The agent's final console output is whatever rally already prints back to the operator at run-end, so the data is there without new plumbing. 160 chars matches the typical first-line length and stays scannable in YAML.

### Consolidated `progress-log` capability
**Chosen**: Lap-completion accounting, handoff entries, file location, schema, and stub-entry behaviour all live under one `progress-log` capability spec.

**Alternative considered**: Separate capabilities for each feature.

**Why**: All are schema/behaviour features of the same artifact (`.rally/progress.yaml`). Splitting them invites cross-capability inconsistency at archive time.

### Hook installation is implicit, with user notification
**Chosen**: Hook installation runs implicitly on `rally relay` startup when laps is enabled (no separate `rally hooks install` subcommand). The first run that installs or updates rally-keyed entries SHALL notify the operator with the paths of the installed hook scripts.

**Alternative considered**: Explicit `rally hooks install` / `rally hooks uninstall` subcommands; silent implicit install.

**Why**: Implicit-on-relay keeps the happy path frictionless. The notification matters because users may already have hooks for other tools. Showing paths to rally's installed scripts lets operators distinguish rally hooks from other tools' hooks, and gives them files to inspect or remove. The notification fires only when entries change — steady-state runs stay quiet.

### Hook scripts written to `.laps/hooks/rally/` (workspace-local)
**Chosen**: Hook scripts embedded in rally binary via `//go:embed`, written to `.laps/hooks/rally/` in the workspace, referenced from `.laps/hooks.json` by relative path.

**Alternative considered**: Global location like `~/.local/share/rally/hooks/`.

**Why**: Workspace-local keeps everything self-contained. The `.laps/` directory already belongs to the laps ecosystem. A `hooks/rally/` subdirectory is clearly scoped. No cross-workspace pollution, no global state to manage.

### Drop the laps instruction toggle
**Chosen**: When laps is enabled, laps-related instructions are always injected. No toggle. The legacy `Beads string` field is removed outright.

**Alternative considered**: Keep an `auto`/`include`/`skip` toggle.

**Why**: The bool already encodes the answer — having laps enabled is the trigger. The toggle becomes redundant.

### `laps wrapup` requires `--summary`
**Chosen**: `laps wrapup` invocations without `--summary` are rejected with a non-zero exit and an error message surfaced back to the agent.

**Alternative considered**: Allow a no-op `laps wrapup` that records "agent had nothing to add".

**Why**: Agents that produce nothing useful are exactly the runs an operator most needs to see articulated — either as a handoff or as a stub entry. Letting `laps wrapup` succeed with no summary creates a "blank intentional" path indistinguishable from "agent forgot to summarise". The contract stays sharp.

## Risks / Trade-offs

- **Hook installer overwriting user-edited hooks** → Mitigation: rally only touches entries keyed with `rally:` prefix; the installer does idempotent diff-and-merge so user hooks coexist
- **Agents who stop without calling `laps wrapup` produce stub entries with potentially uninformative summaries** → Mitigation: explicit `"none"` for `laps_completed` makes incomplete runs visible; the 160-char summary is bounded; operators reviewing `recent_runs` can spot the pattern
- **`laps wrapup` arg parsing in shell scripts is fragile around quoting** → Mitigation: the shell layer just forwards `$@` to `rally progress`; rally does the real parsing in Go where shell quoting is already consumed
- **Stale handoff flag in `.rally/run-state.json` if agent crashes between `laps handoff` and `laps wrapup`** → For v0.4.0, the flag is cleared at relay-runner start of next run, so it doesn't bleed across runs
- **Real `laps` binary in tests adds an environment dependency** → integration tests use a real `laps` binary from `PATH` against a reusable fixture workspace; suites that need it skip cleanly when `laps` is absent

## Migration Plan

1. **Code cleanup phase**: remove all `beads_rust` and `beads`-as-backend references. Remove `Beads string` config field. Verify no test fixtures reference removed identifiers.
2. **Hook installation**: implicit on `rally relay` startup when laps is enabled. Rally writes the three rally-keyed entries to `.laps/hooks.json` and writes embedded hook scripts to `.laps/hooks/rally/`. When entries are added or updated, rally prints a notification with the paths. Idempotent: steady-state re-runs don't duplicate and don't re-notify.
3. **Prompt template switch**: relay-runner branches on `LapsEnabled` at prompt-build time. Tests cover both branches.

Rollback: revert the release. The `.rally/progress.yaml` is left behind harmlessly. Hook entries can be removed by hand.
