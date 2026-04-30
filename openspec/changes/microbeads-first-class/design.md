## Context

Rally currently supports `beads` (Go), `beads_rust`, and microbeads as task-tracker backends, with auto-detection picking whichever is installed. In practice this has produced churn: prompt templates that mention multiple bead variants confuse agents; the `Beads string` config field with values `"true"|"false"|"auto"` reads as a backend selector but actually toggles instruction injection; backends other than microbeads see no active integration work.

Rally also ships a human-readable progress log at `docs/orchestration/rally-progress.yaml` that pre-dates the v0.2.0 JSONL store (`.rally/{tries,relays,messages,agent_status}.jsonl`). The two coexist and serve different audiences — the JSONL files are durable system state; the YAML is a curated agent/human-facing summary.

The `rally progress` CLI is the only place agents are taught to call rally directly. That couples agent prompts to rally's CLI surface and handles the "finished" and "blocked" exit conditions identically.

## Goals / Non-Goals

**Goals:**
- One first-class tracker (microbeads), zero references to others in the codebase
- Hook-based integration via `mb-hooks.json` — no rally-side daemon, no microbeads modifications
- Agents in microbeads-backed mode never see `rally` CLI syntax in their prompt
- Distinct user-facing flows for "finished" vs. "blocked" run-end states
- Progress log records bead-completion data unambiguously and grows monotonically across runs

**Non-Goals:**
- Maintaining `beads` or `beads_rust` compatibility — users who want them can prompt-instruct agents directly without rally's involvement
- Modifying microbeads — `mb-hooks.json` is the only interface; microbeads stays rally-agnostic per its SPEC
- Re-deciding the progress log format (YAML vs JSON) — defer until usage data accrues
- Attempting to detect garbage output or enforce per-bead quality gates — that lives in the v0.6.0+ role workflow

## Decisions

### Detect microbeads by `.beads/mb.json`, not `mb` on PATH
**Chosen**: A repo is "microbeads-backed" iff `.beads/mb.json` is discoverable from cwd per the microbeads SPEC.

**Alternative considered**: `mb` on PATH plus any `.beads/` directory.

**Why**: A user can have microbeads installed globally yet be running regular `beads`, `beads_rust`, or some other fork in the present repo. Both alternatives use `.beads/` for storage. The `mb.json` filename is microbeads-specific (its SPEC §Storage names it), so file presence is a sound discriminator. The PATH+dir heuristic would produce false positives in mixed-tooling environments.

### Two-mode operation
**Chosen**: Two distinct modes (microbeads-backed, no-backend) determined at startup, governing hook installation, prompt-template content, and `rally progress` visibility.

**Alternative considered**: A single mode with microbeads as a soft requirement (warn-and-degrade if absent).

**Why**: Soft-requirement degrade paths historically grow into bugs — the warn becomes invisible, agents get inconsistent instructions, hooks get installed where mb won't fire them. An explicit mode flag makes the boundary unambiguous and lets the prompt template branch cleanly.

### Agent contract is `mb`-only in microbeads mode
**Chosen**: Agents in microbeads-backed mode see only `mb` commands in their prompt. Hook scripts translate to internal `rally progress` calls. In no-backend mode, agents call `rally progress` directly as the explicit exception.

**Alternative considered**: Agents always call `rally progress`; microbeads is an internal detail.

**Why**: Two reasons. First, agents already learn `mb done` to close beads — making it the entry point for both bead state *and* run state collapses two surfaces into one. Second, mb-hooks fire deterministically per command, which gives rally a structural seam (the hook script) to capture run state without needing the agent to remember a separate CLI. The no-backend exception exists because there's no `mb` to mediate.

### `mb wrapup` is taught contextually, not up-front
**Chosen**: Initial prompt instructions name only `mb done` and `mb handoff` as exit conditions. The `mb done` after-hook's passback teaches `mb wrapup` once the agent has actually finished a bead.

**Alternative considered**: Initial instructions list `mb done`, `mb wrapup`, `mb handoff` as a triplet up front.

**Why**: Up-front listing creates ambiguity — agents see "wrapup" and may try it standalone, or skip `mb done` thinking wrapup subsumes it. Context-driven teaching ties wrapup to the moment it's needed (right after closing a bead) and keeps the initial prompt smaller. The `mb done` → wrapup chain becomes an obvious narrative.

### `mb handoff` uses a two-call protocol on the same command name
**Chosen**: First call (no/minimal args) flips a per-run handoff flag in `.rally/run-state.json` and prints handoff-tuned instructions back to the agent. Second call (with `--reason` and `--followup`) does the actual work — creates blocker beads via `mb add head`, writes the handoff entry, clears the flag.

**Alternative considered (a)**: Single-call form with all args required up front.
**Alternative considered (b)**: Two distinct command names (`mb handoff-init`, `mb handoff-finalise`).

**Why**: When an agent realises they're stuck, the cognitive cost of constructing a complete handoff invocation is real. The two-call form gives them an "ask for help" first step, then echoes back the exact syntax for the second step. Reusing the same command name avoids inventing a new vocabulary; the hook script distinguishes by inspecting args. The flag in `.rally/run-state.json` lets the eventual progress write know whether the run ended via wrapup or handoff.

### Handoff follow-ups go to the queue head, not the tail
**Chosen**: `mb add head` for each `--followup` so blockers jump the queue.

**Alternative considered**: `mb add tail` (microbeads' default behaviour).

**Why**: Handoff means the current bead can't proceed until the blocker is addressed. Putting the blocker at the tail of the queue means rally would keep retrying the original bead in subsequent runs, all blocked, before reaching the unblock task. Head insertion makes "address blockers first" the default.

### Progress log stays YAML, moves to `.rally/progress.yaml`
**Chosen**: Keep YAML for now. Move the file from `docs/orchestration/rally-progress.yaml` to `.rally/progress.yaml`.

**Alternative considered**: Migrate to JSON immediately to match the "TOML for config, JSON for output" convention.

**Why**: The format question deserves real-usage data before it's settled — the file's audience is humans reading diffs, not just programs. The location move is a clear win regardless: rally runtime data belongs under `.rally/`, not `docs/`. Old file is copied once and left in place; user can git-rm at leisure.

### Stub entries derive summary from the agent's final console output
**Chosen**: When an agent ends without finalising via `mb wrapup` or second `mb handoff` call, the relay loop writes a stub progress entry with `summary` = first 160 characters of the agent's final console-printed message.

**Alternative considered (a)**: Skip the entry entirely (let `recent_runs` have gaps).
**Alternative considered (b)**: Derive summary from the JSONL store's `TryRecord.Summary`.

**Why**: Gaps in `recent_runs` make incomplete runs invisible — exactly when they're most important to surface. The agent's final console output is whatever rally already prints back to the operator at run-end, so the data is there without new plumbing. 160 chars matches the typical first-line length and stays scannable in YAML.

### Consolidated `progress-log` capability
**Chosen**: Bead-completion accounting, handoff entries, file location, schema, and stub-entry behaviour all live under one `progress-log` capability spec.

**Alternative considered**: Separate capabilities `progress-bead-accounting`, `progress-handoff`, `progress-log` as the original draft proposed.

**Why**: All three are schema/behaviour features of the same artifact (`.rally/progress.yaml`). Splitting them invites cross-capability inconsistency at archive time. The `progress-log` spec has multiple requirements that cover each feature.

### Hook installation is implicit, with user notification including absolute paths
**Chosen**: Hook installation runs implicitly on `rally relay` startup in microbeads-backed mode (no separate `rally hooks install` subcommand). The first run that installs or updates rally-keyed entries SHALL notify the operator with the absolute file paths of the installed hook scripts.

**Alternative considered**: Explicit `rally hooks install` / `rally hooks uninstall` subcommands; silent implicit install.

**Why**: Implicit-on-relay keeps the happy path frictionless. The notification matters because users may already have hooks for other tools (e.g. Claude Code hooks under `~/.claude/hooks/` or similar). Showing absolute paths to rally's installed scripts lets operators distinguish rally hooks from other tools' hooks at a glance, and gives them concrete files to inspect or remove if they want to opt out manually. The notification fires only when entries change — steady-state runs stay quiet.

### Drop the microbeads instruction toggle
**Chosen**: When microbeads-backed mode is detected, microbeads-related instructions are always injected into the prompt. There is no `auto`/`include`/`skip` toggle. The legacy `Beads string` config field is removed (not renamed-and-kept).

**Alternative considered**: Keep an `auto`/`include`/`skip` toggle so users can opt out of injection when their `CLAUDE.md`/`AGENTS.md` already covers the syntax.

**Why**: Mode detection already encodes the answer — having microbeads available is the trigger; the toggle becomes redundant. Supporting "microbeads-available-but-unused" as a distinct mode adds configuration surface for a niche case rally doesn't need to solve in v0.4.0. If a user has the syntax documented elsewhere, the duplication is small (a few lines of prompt) and removing it is a future refinement once usage data shows it matters.

**Spec follow-up**: the `Microbeads instruction toggle` requirement in `specs/microbeads-only-integration/spec.md` should be removed. Tracking as a downstream edit.

### `mb wrapup` requires `--summary`
**Chosen**: `mb wrapup` invocations without `--summary` are rejected with a non-zero exit and an error message surfaced back to the agent. Runs that produced nothing recordable still must record that fact via `mb handoff` (with a `--reason` explaining the no-progress state) or by exiting (which produces a stub entry).

**Alternative considered**: Allow a no-op `mb wrapup` form that records "agent had nothing to add" without requiring the agent to write a summary.

**Why**: Agents that produce nothing useful are exactly the runs an operator most needs to see articulated — either as a handoff (with reason) or as a stub entry derived from the agent's last console output. Letting `mb wrapup` succeed with no summary creates a third "blank intentional" path that's indistinguishable from "agent forgot to summarise" in the log. The contract stays sharp: `mb wrapup` means "I have something to record"; otherwise use `mb handoff` or exit.

### Drop `executor` from modified capabilities
**Chosen**: The existing `executor` spec doesn't have a backend-selection requirement, so there's no requirement-level modification to apply.

**Alternative considered**: List `executor` as modified to capture the conceptual change ("no backend selector").

**Why**: Modified-capability deltas need a real existing requirement to amend. The backend-selector concept lives only in code today (the `Beads string` field), not in the spec, so the change shows up as code/config edits without a spec delta. Listing it would create an empty modification that confuses archive consolidation.

## Risks / Trade-offs

- **Hook installer overwriting user-edited hooks** → Mitigation: rally only touches entries keyed with a `rally:` prefix in hook names; the installer does idempotent diff-and-merge so user hooks for the same `mb` commands coexist with rally's
- **Two-call `mb handoff` protocol depends on the agent reading the first call's instructions** → Mitigation: instructions are short and explicit; second call without `--reason` errors with a clear message. If agents systematically skip the second call, we can collapse to a one-shot form in a follow-up
- **Agents who stop without calling `mb done` or `mb handoff` produce stub entries with potentially uninformative summaries** → Mitigation: explicit `"none"` for `beads_completed` makes incomplete runs visible; the 160-char summary is bounded so worst-case noise is small; operators reviewing `recent_runs` can spot the pattern
- **`mb handoff` arg parsing in shell scripts is fragile around quoting** → Mitigation: the shell layer just forwards `$@` to `rally progress --handoff`; rally does the real parsing in Go where shell quoting is already consumed
- **File copy of legacy `rally-progress.yaml` could surprise users with stale data under `.rally/`** → Mitigation: copy is one-shot on first post-upgrade run; release note documents the move; user owns whether/when to git-rm the old path
- **Stale handoff flag in `.rally/run-state.json` if agent crashes between the two `mb handoff` calls** → Tracked as v0.7.0 concern (resume vs. fresh-start clearing). For v0.4.0, the flag is cleared at relay-runner start of next run, so it doesn't bleed across runs

## Migration Plan

1. **Code cleanup phase**: remove all `beads_rust` and `beads`-as-backend references. Rename `Beads string` config field. Verify no test fixtures reference removed identifiers.
2. **Storage move**: on first post-upgrade `rally relay` invocation, copy `docs/orchestration/rally-progress.yaml` → `.rally/progress.yaml` if the destination doesn't exist. Subsequent runs read/write `.rally/progress.yaml` only.
3. **Schema rename**: on first write of the new file, rewrite `recent_sessions` → `recent_runs` and `session_id` → `run_id` in place. Additive fields (`beads_completed`, `handoff`) appear from then on.
4. **Hook installation**: implicit on `rally relay` startup in microbeads-backed mode. Rally writes the three rally-keyed entries to `.beads/mb-hooks.json` and writes the hook script bodies to a stable on-disk location (e.g. `~/.local/share/rally/hooks/mb-{done,wrapup,handoff}-hook.sh`). When entries are added or updated, rally prints a one-time notification with the absolute paths of the script files so operators can distinguish rally's hooks from other tools' hooks (e.g. Claude Code hooks). Idempotent: steady-state re-runs don't duplicate and don't re-notify.
5. **Prompt template switch**: relay-runner branches on mode at prompt-build time. Tests cover both branches.

Rollback: revert the v0.4.0 release. The new `.rally/progress.yaml` is left behind harmlessly; the old `docs/orchestration/rally-progress.yaml` is still on disk because we never delete it. Hook entries can be removed by hand or by an explicit `rally hooks uninstall` if implemented.


