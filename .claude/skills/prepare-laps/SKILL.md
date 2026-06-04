---
name: prepare-laps
description: Convert OpenSpec changes, implementation plans, specs, task lists, or rough feature requests into an ordered Laps queue for Rally. Use when decomposing work into role-aware laps, assigning JUNIOR/SENIOR/UI/VERIFY tasks, adding phase verification, or preparing agent handoffs from OpenSpec or non-OpenSpec plans.
license: MIT
metadata:
  author: rally
  version: "2.0"
---

# Prepare Laps

Turn a plan into a Rally-native Laps queue that another agent can execute one lap at a time. The output should be concrete enough to keep agents on track, but not so prescriptive that it steals ownership of the fine implementation details.

Requires the `laps` CLI v0.7.0 or newer for batch JSON task creation. Rally injects per-role guidance from `.rally/agents/<assignee>.md` at run time — do not duplicate role intros inside lap descriptions.

## Core Rules

- Treat every lap as a handoff to a different agent.
- Use assignees exactly: `JUNIOR`, `SENIOR`, `UI`, `VERIFY`. Set them via the `--assignee` flag; never encode the role in the title.
- Split each implementation phase into 1–3 laps.
  - 1 lap: mechanical setup, narrow config, isolated file changes, simple docs.
  - 2 laps: familiar cross-module work, implementation plus focused tests, moderate uncertainty.
  - 3 laps: high-risk boundaries, broad tests, UI flows with states, migrations/backcompat, significant refactors.
- Split large test-writing phases aggressively — usually 2–3 laps by layer, harness, or scenario family.
- When a key file being modified has no dedicated test file, add a baseline-tests lap before the modification laps. This gives the implementation agent a safety net and catches regressions early. Route baseline-tests laps to the same role that would write the implementation tests (usually JUNIOR for mechanical coverage, SENIOR if the behavior under test is subtle).
- Add a `VERIFY` lap immediately after any single high-risk lap (production data path, auth/session/sync behavior, migrations, broad shared contracts, brownfield architecture changes).
- Otherwise insert `VERIFY` every 2–4 implementation laps **or at natural slice boundaries** (e.g., "all user-visible UX before infra"), whichever comes first.
- After the final phase, add one `VERIFY` lap covering the whole outcome.
- For lightweight greenfield examples or Rally role-routing smoke tests, a single final `VERIFY` is enough. Spend saved laps on implementation depth.
- Verification laps may fix only tiny, safe one-liners. Anything larger becomes a new focused lap added to the head of the queue.
- If any lap uncovers a blocker, the assigned agent should `laps add head ...` for it before marking the lap done.
- Every lap should instruct its agent to surface meaningful uncertainties and apparent plan problems — circular or contradictory task dependencies, work that does not map cleanly onto a lap, missing prerequisites, a plan claim that contradicts the tree — rather than silently working around them. Route these to a head lap (`laps add head`) or the wrapup summary so they reach a human or the VERIFY role.
- For OpenSpec work, only `VERIFY` laps check off `tasks.md` boxes, and only after verifying the work is done correctly and with sufficient thoroughness and quality. Implementation laps (`JUNIOR`/`SENIOR`/`UI`) do the work and report it but must not tick `tasks.md`; a checked box means "verified done," not "attempted."
- Diff and cleanup instructions must be branch-target aware. Do not assume `main`; tell VERIFY laps to identify the intended merge target from the user, PR metadata, repo docs, branch config, or recent history before using `git diff <target>...HEAD`.
- Work that predates the first lap in the current batch is valid baseline context, even when it is not part of the current request. VERIFY may flag it as pre-existing, but must not add cleanup laps that remove it unless the user explicitly asks.
- Never ask a lap to rewrite git history (`reset`, `rebase`, squash, amend-away, force-push) as a cleanup strategy. Prefer additive commits, explicit revert commits, or a user-approved recovery branch so reverted work remains backtrackable.
- Do not classify `.laps/`, `.rally/config.toml`, or `.rally/agents/` as disposable runtime noise. They are normally tracked planning/config artifacts. High-churn runtime/debug artifacts under `.rally/state/` should be pruned/exported separately.

## Workflow

1. **Orient**
   - Check existing work with `laps list`.
   - If the queue still holds laps from a previous, already-committed batch, clear them before adding the new batch. Laps are intermediate state; OpenSpec and git history are the durable record, so completed laps already preserved in git are safe to remove. Use `laps prune 0` to drop done laps and `laps delete <id>` for stale todo laps, leaving only the current batch. (Laps has no edit command — to revise a lap, delete and re-add it.)
   - If the change path or name you were given does not resolve (e.g. a typo), do not fail: list `openspec/changes/` (and `openspec/changes/archive/`) and confirm the intended change before planning. Never silently plan a different change.
   - Confirm `.rally/agents/<role>.md` exists for the roles you plan to assign. If missing, instruct the user to run `rally init roles`, or add an early setup lap that runs it. Do not paste role definitions into the skill or into laps.
   - Confirm Rally route support if relevant: `.rally/config.toml`, `rally routes check`.
   - If the input is an OpenSpec change with tasks/specs already written, run `openspec status --change "<name>" --json` and `openspec instructions apply --change "<name>" --json`, then read the returned `contextFiles`.
   - If the input is a proposal **without** tasks/specs, either (a) plan directly from the proposal — fine when the work is light or already well-explored in conversation, or (b) nudge the user to run `opsx:ff` first when scope or risk is unclear. Default to (a) for ≤10 laps of well-understood work and (b) for larger or hazier work.
   - If no change name is provided and multiple active OpenSpec changes exist, ask or use the user's latest context. Do not silently plan the wrong change.
   - If the change declares a dependency on another change ("depends on #N", "after `<change>`", or a "post-`<change>` world"), verify during prepare-laps that the dependency has actually landed **and** that the working tree matches the end-state it promised — inspect the tree, do not trust the proposal's narrative. Resolve or report any mismatch to the user now. Do not embed dependency-detective instructions into individual laps: pre-change dependency checking belongs here, and mid-change dependency verification is the standing job of the `VERIFY` role.
   - For non-OpenSpec input, inspect the provided plan/files and explore the codebase just enough to identify phases, risks, dependencies, and verification commands.
   - For small ad-hoc requests, missing plan files, or very short specs, fold relevant facts directly into each lap description instead of pointing at a file.

2. **Shape phases**
   - Prefer outcome-oriented phases: setup, core behavior, integration, UI, tests, docs/migration, cleanup.
   - Preserve real dependencies, but avoid over-rigid microplans. Give architecture guidance and acceptance criteria; let the assigned agent choose local implementation details.
   - For under-defined work, add an early `SENIOR` or `UI` exploration/design lap before implementation. Its output should be decisions and follow-up head laps if the work expands.

3. **Assign roles**
   - `JUNIOR`: bounded mechanical or pattern-following work, narrow bug fixes, focused tests following existing patterns, simple UI wiring (a single button, an extra setting).
   - `SENIOR`: design quality and risk management — architecture-sensitive changes, auth/session/sync/data correctness, migrations, significant new patterns, tricky debugging.
   - `UI`: non-trivial visual design judgment — new modals, animations, multi-state interactions, new layouts, tone-shaping copy. Use UI when design judgment matters; use JUNIOR when the work is one-off wiring with no judgment call.
   - `VERIFY`: verification, code review, OpenSpec verification, test audit, follow-up head-lap creation.
   - The `assignee` field is the contract. Rally loads `.rally/agents/<assignee>.md` and prepends it to the prompt; the lap description does not need to repeat that. Sharing roles across laps is also a teamwork goal — route UI judgment to the UI model, architecture to SENIOR, mechanical work to JUNIOR.

4. **Write each lap**
   Inclusion is dynamic. Most laps include 4–5 of these sections; pick what serves the work:
   - **Context** — source artifacts, prior-phase assumptions, relevant files. Skip when the title + acceptance are fully self-explanatory or when the lap is open-ended.
   - **Outcome** — the observable end state. Almost always include.
   - **Files & scope** — what to touch, what to avoid. Skip for exploratory or design laps where files aren't known yet.
   - **Design** — architectural constraints, patterns to follow, risk notes, subtleties. Include when judgment beyond "follow the obvious path" is required (always for SENIOR; often for UI; sometimes for JUNIOR).
   - **Acceptance** — tests, commands, smoke checks, docs updates, observable behavior. Almost always include.

5. **Add tasks**
   - Use one `laps add tail --json '[...]'` call for planned work in execution order. JSON array input is validated and written as one queue update, preserves array order, and avoids leaving a partially-created plan if one lap is invalid. For large generated payloads, pipe the array with `laps add tail --json -` to avoid shell quoting and argument-length problems.
   - For urgent blockers, use `laps add head --json '[...]'` or `laps add after <id> --json '[...]'`; array order is preserved for every position, so do not reverse it manually.
   - Use a single JSON object for one planned lap. Use `--title`/`--description` flags only for short ad-hoc laps where the description is a plain sentence.
   - Always set `--assignee`; rally routes from it.
   - **Format skeleton** — the object *shape* only, not a content template. Do not mimic these placeholder values, section counts, or phrasing; write real Context/Outcome/Files & scope/Design/Acceptance prose per the "Write each lap" guidance.

     ```json
     [
       {
         "title": "<short imperative lap title, no role prefix>",
         "assignee": "JUNIOR | SENIOR | UI | VERIFY",
         "description": "<multi-section prose: Context, Outcome, Files & scope, Design, Acceptance>"
       }
     ]
     ```

   - Run `laps list` at the end and sanity-check role order, VERIFY placement, and the final full-outcome verification lap.

## Testing Laps

- **Bundled tests (default):** include tests in the implementation lap when the tests are tightly scoped to that lap's outcome and the combined work is a reasonable size. The acceptance section should specify test names or patterns.
- **Split tests into a separate lap** when:
  - The implementation is high-risk and you want the test lap to serve as a thorough review (e.g., state machine transitions, scheduler interactions).
  - The test matrix is large enough that bundling would make the implementation lap too big (>3 scenario families or >2 layers).
  - Tests require a different perspective or role than the implementation (rare — usually same role).
- **Baseline tests** (before modification): when a key file has no existing test coverage and you are about to heavily modify it, add a baseline-tests lap before the modification lap. This tests the current behavior so the implementation agent has a safety net.

## Verification Laps

For OpenSpec work, a phase `VERIFY` lap should tell the agent to use the `openspec-verify-change` skill against the same change, then focus the report on the phase just completed. The final full-change `VERIFY` lap should run the complete OpenSpec verification and inspect the whole diff.

For non-OpenSpec work, verification laps should read the original request, inspect the diff, run the relevant tests, perform any realistic smoke checks, and report findings first. They should create new head laps for substantive gaps rather than turning review into a hidden implementation phase.

Verification lap descriptions should include:

- Identify the branch target/base before diffing; use that target in diff commands.
- Identify the first lap/try in the current batch and treat earlier branch work as pre-existing unless the user asks to include it in scope.
- Do not rewrite git history. If scope cleanup is needed, add a focused lap that uses additive/revert commits or asks the user for an explicit recovery strategy.
- Review with appropriate depth: trace the core lines of dependency — key call sites, the symbols actually added or removed, the prompt/string actually emitted, the commit actually produced — not just whether tests pass. "Tests are green" is not sufficient verification for a high-risk lap.
- Report meaningful uncertainties and any apparent plan problems found while verifying (contradictory tasks, scope that does not map onto the change, premises that no longer hold), and create head laps for substantive gaps.
- For OpenSpec work, check off the `tasks.md` boxes for the tasks this lap verified as correctly and thoroughly done. Do not check boxes for work the implementation laps merely attempted — verification is the gate for ticking a box.

## Skill Maintenance

At the end of a prepare-laps session, update this skill when:

- The user corrects lap size, phase shape, role assignment, or VERIFY cadence.
- A recurring class of lap is too vague, too large, or too small.
- Rally's role-loading behavior, OpenSpec output shape, or Laps CLI behavior changes.
- A verification failure reveals a better standard check or follow-up-lap pattern.

Keep the main workflow general. Role definitions themselves live in rally (`rally init roles` writes them to `.rally/agents/`); do not duplicate them in this skill.
