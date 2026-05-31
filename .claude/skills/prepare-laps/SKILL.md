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

Requires the `laps` CLI. Rally injects per-role guidance from `.rally/agents/<assignee>.md` at run time — do not duplicate role intros inside lap descriptions.

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
- Diff and cleanup instructions must be branch-target aware. Do not assume `main`; tell VERIFY laps to identify the intended merge target from the user, PR metadata, repo docs, branch config, or recent history before using `git diff <target>...HEAD`.
- Work that predates the first lap in the current batch is valid baseline context, even when it is not part of the current request. VERIFY may flag it as pre-existing, but must not add cleanup laps that remove it unless the user explicitly asks.
- Never ask a lap to rewrite git history (`reset`, `rebase`, squash, amend-away, force-push) as a cleanup strategy. Prefer additive commits, explicit revert commits, or a user-approved recovery branch so reverted work remains backtrackable.
- Do not classify `.laps/`, `.rally/config.toml`, or `.rally/agents/` as disposable runtime noise. They are normally tracked planning/config artifacts. High-churn runtime/debug artifacts under `.rally/state/` should be pruned/exported separately.

## Workflow

1. **Orient**
   - Check existing work with `laps list`.
   - Confirm `.rally/agents/<role>.md` exists for the roles you plan to assign. If missing, instruct the user to run `rally init roles`, or add an early setup lap that runs it. Do not paste role definitions into the skill or into laps.
   - Confirm Rally route support if relevant: `.rally/config.toml`, `rally routes check`.
   - If the input is an OpenSpec change with tasks/specs already written, run `openspec status --change "<name>" --json` and `openspec instructions apply --change "<name>" --json`, then read the returned `contextFiles`.
   - If the input is a proposal **without** tasks/specs, either (a) plan directly from the proposal — fine when the work is light or already well-explored in conversation, or (b) nudge the user to run `opsx:ff` first when scope or risk is unclear. Default to (a) for ≤10 laps of well-understood work and (b) for larger or hazier work.
   - If no change name is provided and multiple active OpenSpec changes exist, ask or use the user's latest context. Do not silently plan the wrong change.
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
   - Use `laps add tail` for planned work in execution order. Use `--json '{"title":"...","description":"...","assignee":"..."}'` by default — most lap descriptions contain quotes, backticks, or special characters that make shell quoting painful. Use `--title`/`--description` flags for short ad-hoc laps where the description is a plain sentence.
   - For urgent blockers, use `laps add head` (reverse execution order for multiple) or `laps add after <id>`.
   - Always set `--assignee`; rally routes from it.
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

## Skill Maintenance

At the end of a prepare-laps session, update this skill when:

- The user corrects lap size, phase shape, role assignment, or VERIFY cadence.
- A recurring class of lap is too vague, too large, or too small.
- Rally's role-loading behavior, OpenSpec output shape, or Laps CLI behavior changes.
- A verification failure reveals a better standard check or follow-up-lap pattern.

Keep the main workflow general. Role definitions themselves live in rally (`rally init roles` writes them to `.rally/agents/`); do not duplicate them in this skill.
