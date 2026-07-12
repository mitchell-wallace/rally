---
name: prepare-laps
description: Convert OpenSpec changes, implementation plans, specs, task lists, or rough feature requests into an ordered flat root queue or named stint queues for Rally. Use when decomposing work into role-aware laps, assigning intern/junior/senior/architect/review/verify/qa/recovery tasks, adding phase verification, preparing agent handoffs, or when the user invokes prepare-laps, prepare-flat-laps, or prepare-stints to select smart, explicitly flat, or explicitly stint-shaped output.
license: MIT
metadata:
  author: rally
  version: "2.1"
---

# Prepare Laps

Turn a plan into a Rally-native Laps queue that another agent can execute one lap at a time. Choose a flat root sequence or multiple named stint sub-queues from the user's intent and the plan's dependency shape. The output should be concrete enough to keep agents on track, but not so prescriptive that it steals ownership of the fine implementation details.

Requires the `laps` CLI v0.9.0 or newer for named stints, scoped batch JSON task creation, and claim-flow support. Rally injects per-role guidance from `.rally/agents/<assignee>.md` at run time — do not duplicate role intros inside lap descriptions.

## Core Rules

- Treat every lap as a handoff to a different agent.
- Use assignees exactly: `intern`, `junior`, `senior`, `architect`, `review`, `verify`, `qa`, `recovery`. Set them via the `--assignee` flag; never encode the role in the title.
- Treat the root queue as deliberately flat and ordered, not as a dependency graph. A stint is a named hierarchical sub-queue referenced from that flat root queue; stint shape does not add dependency or parallel-execution fields to laps.
- Split each implementation phase into 1–3 laps.
  - 1 lap: mechanical setup, narrow config, isolated file changes, simple docs.
  - 2 laps: familiar cross-module work, implementation plus focused tests, moderate uncertainty.
  - 3 laps: high-risk boundaries, broad tests, UI flows with states, migrations/backcompat, significant refactors.
- Split large test-writing phases aggressively — usually 2–3 laps by layer, harness, or scenario family.
- When a key file being modified has no dedicated test file, add a baseline-tests lap before the modification laps. This gives the implementation agent a safety net and catches regressions early. Route baseline-tests laps to the same role that would write the implementation tests (usually `junior` for mechanical coverage, `senior` if the behavior under test is subtle).
- Add a `verify` lap immediately after any single high-risk lap (production data path, auth/session/sync behavior, migrations, broad shared contracts, brownfield architecture changes).
- Otherwise insert `verify` every 2–4 implementation laps **or at natural slice boundaries** (e.g., "all user-visible UX before infra"), whichever comes first.
- After the final phase, add one `verify` lap covering the whole outcome.
- For lightweight greenfield examples or Rally role-routing smoke tests, a single final `verify` is enough. Spend saved laps on implementation depth.
- Verification laps may fix only tiny, safe one-liners. Anything larger becomes a new focused lap added to the head of the queue.
- If any lap uncovers a blocker, the assigned agent should `laps add head ...` for it before marking the lap done.
- Keep laps tight and well-defined. Do not pad lap descriptions with "report any uncertainty" boilerplate — surfacing plan problems is the planning agent's job (see Workflow), and execution-time blockers are already covered by the head-lap rule above.
- For OpenSpec work, only `verify` laps check off `tasks.md` boxes, and only after verifying the work is done correctly and with sufficient thoroughness and quality. Implementation laps (`intern`/`junior`/`senior`) do the work and report it but must not tick `tasks.md`; a checked box means "verified done," not "attempted."
- Diff and cleanup instructions must be branch-target aware. Do not assume `main`; tell `verify` laps to identify the intended merge target from the user, PR metadata, repo docs, branch config, or recent history before using `git diff <target>...HEAD`.
- Work that predates the first lap in the current batch is valid baseline context, even when it is not part of the current request. `verify` may flag it as pre-existing, but must not add cleanup laps that remove it unless the user explicitly asks.
- Never ask a lap to rewrite git history (`reset`, `rebase`, squash, amend-away, force-push) as a cleanup strategy. Prefer additive commits, explicit revert commits, or a user-approved recovery branch so reverted work remains backtrackable.
- Do not classify `.laps/`, `.rally/config.toml`, or `.rally/agents/` as disposable runtime noise. They are normally tracked planning/config artifacts. High-churn runtime/debug artifacts under `.rally/state/` should be pruned/exported separately.

## Workflow

1. **Orient**
   - Check the root pipeline with `laps list --root` and, when stints exist or are requested, inspect the full shape with `laps list --tree` and `laps stints ls`.
   - If a selected queue still holds laps from a previous, already-committed batch, clear them before adding the new batch. Laps are intermediate state; OpenSpec and git history are the durable record, so completed laps already preserved in git are safe to remove. Use `laps prune 0 --root` or `laps prune 0 --stint <name>` to drop done laps and the matching scoped `laps delete <id>` for stale todo laps, leaving only the current batch.
   - If the change path or name you were given does not resolve (e.g. a typo), do not fail: list `openspec/changes/` (and `openspec/changes/archive/`) and confirm the intended change before planning. Never silently plan a different change.
   - Confirm `.rally/agents/<role>.md` exists for the roles you plan to assign. If missing, instruct the user to run `rally init roles`, or add an early setup lap that runs it. Do not paste role definitions into the skill or into laps.
   - Confirm Rally route support if relevant: `.rally/config.toml`, `rally routes check`.
   - If the input is an OpenSpec change with tasks/specs already written, run `openspec status --change "<name>" --json` and `openspec instructions apply --change "<name>" --json`, then read the returned `contextFiles`.
   - If the input is a proposal **without** tasks/specs, either (a) plan directly from the proposal — fine when the work is light or already well-explored in conversation, or (b) nudge the user to run `opsx:ff` first when scope or risk is unclear. Default to (a) for ≤10 laps of well-understood work and (b) for larger or hazier work.
   - If no change name is provided and multiple active OpenSpec changes exist, ask or use the user's latest context. Do not silently plan the wrong change.
   - If the change declares a dependency on another change ("depends on #N", "after `<change>`", or a "post-`<change>` world"), verify during prepare-laps that the dependency has actually landed **and** that the working tree matches the end-state it promised — inspect the tree, do not trust the proposal's narrative. Resolve or report any mismatch to the user now. Do not embed dependency-detective instructions into individual laps: pre-change dependency checking belongs here, and mid-change dependency verification is the standing job of the `verify` role.
   - For non-OpenSpec input, inspect the provided plan/files and explore the codebase just enough to identify phases, risks, dependencies, and verification commands.
   - For small ad-hoc requests, missing plan files, or very short specs, fold relevant facts directly into each lap description instead of pointing at a file.

2. **Select the queue mode**
   - Treat the three invocation surfaces as intent passed to this one skill: `prepare-laps` is smart/default mode, `prepare-flat-laps` forces flat mode, and `prepare-stints` forces stint mode. Equivalent natural-language requests carry the same intent.
   - **Explicit flat:** when the user says `prepare-flat-laps`, "keep this flat," "no stints," "single sequence," or equivalent, write one ordered root sequence. Preserve useful phase boundaries in lap titles and descriptions, but never turn them into stints, even when the plan has natural independent slices.
   - **Explicit stints:** when the user says `prepare-stints`, asks to break work into stints, or requests parallel lanes or independent tracks, decompose the plan into multiple named stint queues, one per independent phase or workstream. This skill shapes queues only; do not invent parallel fields or perform Lanes split/merge mechanics.
   - **Smart/default:** when the target is the root queue and no mode is requested, use one flat sequence at the root tail. If the plan has clearly independent workstreams with no shared-file conflicts and no ordering dependencies, propose the exact stint names and grouping and ask for confirmation before writing. Never silently restructure a plan presented as a flat sequence. If independence is uncertain, stay flat and tell the operator that the conservative mode was chosen.
   - A request for stints is deliberate even when the resulting root pipeline remains serial. Enqueued stint references execute in root order; parallel execution, if desired, is an external orchestration concern.

3. **Shape phases**
   - Prefer outcome-oriented phases: setup, core behavior, integration, user-facing surfaces, tests, docs/migration, cleanup.
   - Preserve real dependencies, but avoid over-rigid microplans. Give architecture guidance and acceptance criteria; let the assigned agent choose local implementation details.
   - For under-defined implementation work, add an early `senior` exploration/design lap; when the plan, architecture, sequencing, or remaining lap assignments need repair without implementation, add an `architect` lap. The output should be decisions and follow-up head laps if the work expands.

4. **Assign roles**
   - `intern`: prescribed, mechanical, reversible edits where the approach is already chosen. Use only for exact scoped changes; escalate on design ambiguity.
   - `junior`: bounded implementation inside established architecture. This is the default implementation lane; local autonomy is allowed, but cross-subsystem or contract decisions are not.
   - `senior`: design-sensitive implementation. Use for architecture-aware code changes, auth/session/sync/data correctness, migrations, significant new patterns, tricky debugging, and bounded plan corrections.
   - `architect`: plan-only diagnosis, architecture, decomposition, sequencing, and reassignment. It does not write code or tests.
   - `review`: findings-first code review of a scoped diff, branch, relay, or implementation range. It must load and follow the `auto-code-review` skill; auto-fix only when explicitly requested.
   - `verify`: acceptance evidence, OpenSpec verification, test audit, and follow-up head-lap creation. Mostly read-only; reports pass/fail and follow-up laps.
   - `qa`: black-box user-style testing of observable workflows. Reports defects without editing code.
   - `recovery`: dirty, failed, timed-out, incomplete, or incoherent state reconciliation. Use only to restore a safe baseline or route to architect/implementation.
   - Decision tree: choose `recovery` first for incoherent state; `architect` for plan/architecture/sequencing changes without implementation; `review` for scoped code review; `verify` for acceptance evidence; `qa` for external user-style testing; `intern` for exact mechanical scope; `junior` for bounded normal implementation; `senior` for design-sensitive implementation; `architect` again if implementation proves the remaining plan wrong.
   - Escalation map: `intern` → `junior`/`senior`; `junior` → `senior`/`architect`; `senior` → `architect` or `recovery` if dirty; `architect` → implementation laps; `review` → `senior`/`junior` fixes, `architect` decisions, then `verify`; `verify` → `junior`/`senior` fixes, `architect` for ambiguous criteria, `review` for code-risk concerns; `qa` → `junior`/`senior` fixes, `architect` for product ambiguity, then `verify`; `recovery` → continue/discard/course_correct/architect/needs_user.
   - The `assignee` field is the contract. Rally loads `.rally/agents/<assignee>.md` and prepends it to the prompt; the lap description does not need to repeat that. Sharing roles across laps is also a teamwork goal — route exact mechanical work to `intern`, normal implementation to `junior`, design-sensitive work to `senior`, plan repair to `architect`, review to `review`, evidence gates to `verify`, external workflow testing to `qa`, and dirty state repair to `recovery`.

5. **Write each lap**
   Inclusion is dynamic. Most laps include 4–5 of these sections; pick what serves the work:
   - **Context** — source artifacts, prior-phase assumptions, relevant files. Skip when the title + acceptance are fully self-explanatory or when the lap is open-ended.
   - **Outcome** — the observable end state. Almost always include.
   - **Files & scope** — what to touch, what to avoid. Skip for exploratory or design laps where files aren't known yet.
   - **Design** — architectural constraints, patterns to follow, risk notes, subtleties. Include when judgment beyond "follow the obvious path" is required (always for `senior`; sometimes for `junior`; for `intern`, provide the exact pattern instead of broad design space).
   - **Acceptance** — tests, commands, smoke checks, docs updates, observable behavior. Almost always include.

6. **Add tasks**
   - **Flat mode:** use one `laps add tail --root --json '[...]'` call for planned work in execution order. The explicit `--root` prevents an active stint from capturing a plan intended for the root queue; when no stint is active, this is the structurally explicit form of `laps add tail --json`. JSON array input is validated and written as one queue update, preserves array order, and avoids leaving a partially-created plan if one lap is invalid. For large generated payloads, pipe the array with `laps add tail --root --json -` to avoid shell quoting and argument-length problems.
   - **Stint mode:** choose short, descriptive kebab-case names such as `auth`, `search`, or `ui-polish`, never ordinal names such as `stint-1`. For each workstream, run `laps stints new <name>`, then add its ordered lap array atomically with `laps add tail --stint <name> --json '[...]'`. Do not use raw `-f <name>`: that addresses `.laps/<name>.json`, not `.laps/stints/<name>.laps.json`.
   - Enqueue prepared stints in their required root order with `laps stints enqueue <name> tail`. Unless the user explicitly needs unqueued stints for an external parallel workflow, enqueue each one. If cross-stint acceptance needs a final whole-outcome gate, append one root `verify` lap after the stint references, or use a final clearly named integration stint when integration itself is substantive.
   - When regrouping laps that already exist, create the destination stint first and use `laps transfer <stint> <task-id>... --root` (or the appropriate source `--stint <name>`). Transfer validates and moves the batch across queue files together while preserving ids and task fields; it does not create the destination implicitly.
   - For urgent blockers, use `laps add head --json '[...]'` or `laps add after <id> --json '[...]'`; array order is preserved for every position, so do not reverse it manually.
   - Use a single JSON object for one planned lap. Use `--title`/`--description` flags only for short ad-hoc laps where the description is a plain sentence.
   - Always set `--assignee`; rally routes from it.
   - **Format skeleton** — the object *shape* only, not a content template. Do not mimic these placeholder values, section counts, or phrasing; write real Context/Outcome/Files & scope/Design/Acceptance prose per the "Write each lap" guidance.

     ```json
     [
       {
         "title": "<short imperative lap title, no role prefix>",
         "assignee": "intern | junior | senior | architect | review | verify | qa | recovery",
         "description": "<multi-section prose: Context, Outcome, Files & scope, Design, Acceptance>"
       }
     ]
     ```

   - Run `laps list --root` and `laps list --tree` at the end. In stint mode, also run `laps stints show <name>` for each prepared stint. Sanity-check queue order, role order, `verify` placement, stint names and boundaries, and the final full-outcome verification lap.
   - Commit the prepared `.laps/laps.json` queue to git after the sanity check unless the user explicitly says not to. If this prepare-laps session also updates this skill, include that skill edit in the same commit so the queue and planning convention land together.
   - Report the selected mode and why, plus any meaningful uncertainties or plan problems you hit while planning — circular or contradictory task dependencies, work that does not map cleanly onto laps or independent stints, missing prerequisites, or a plan claim that contradicts the working tree. Raise these in your summary to the operator; do not bury them inside lap descriptions or silently plan around them. This planning-time reporting is the planning agent's responsibility, distinct from the `verify` role's mid-change verification.

## Testing Laps

- **Bundled tests (default):** include tests in the implementation lap when the tests are tightly scoped to that lap's outcome and the combined work is a reasonable size. The acceptance section should specify test names or patterns.
- **Split tests into a separate lap** when:
  - The implementation is high-risk and you want the test lap to serve as a thorough review (e.g., state machine transitions, scheduler interactions).
  - The test matrix is large enough that bundling would make the implementation lap too big (>3 scenario families or >2 layers).
  - Tests require a different perspective or role than the implementation (rare — usually same role).
- **Baseline tests** (before modification): when a key file has no existing test coverage and you are about to heavily modify it, add a baseline-tests lap before the modification lap. This tests the current behavior so the implementation agent has a safety net.

## Verification Laps

For OpenSpec work, a phase `verify` lap should tell the agent to use the `openspec-verify-change` skill against the same change, then focus the report on the phase just completed. The final full-change `verify` lap should run the complete OpenSpec verification and inspect the whole diff.

For non-OpenSpec work, verification laps should read the original request, inspect the diff, run the relevant tests, perform any realistic smoke checks, and report findings first. They should create new head laps for substantive gaps rather than turning review into a hidden implementation phase.

Verification lap descriptions should include:

- Identify the branch target/base before diffing; use that target in diff commands.
- Identify the first lap/try in the current batch and treat earlier branch work as pre-existing unless the user asks to include it in scope.
- Do not rewrite git history. If scope cleanup is needed, add a focused lap that uses additive/revert commits or asks the user for an explicit recovery strategy.
- Review with appropriate depth: trace the core lines of dependency — key call sites, the symbols actually added or removed, the prompt/string actually emitted, the commit actually produced — not just whether tests pass. "Tests are green" is not sufficient verification for a high-risk lap.
- For OpenSpec work, check off the `tasks.md` boxes for the tasks this lap verified as correctly and thoroughly done. Do not check boxes for work the implementation laps merely attempted — verification is the gate for ticking a box.

## Skill Maintenance

At the end of a prepare-laps session, update this skill when:

- The user corrects lap size, phase shape, queue mode, stint boundaries, role assignment, or `verify` cadence.
- A recurring class of lap is too vague, too large, or too small.
- Rally's role-loading behavior, OpenSpec output shape, or Laps CLI behavior changes.
- A verification failure reveals a better standard check or follow-up-lap pattern.

Keep the main workflow general. Role definitions themselves live in rally (`rally init roles` writes them to `.rally/agents/`); do not duplicate them in this skill.
