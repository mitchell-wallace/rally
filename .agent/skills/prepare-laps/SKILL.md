---
name: prepare-laps
description: Convert OpenSpec changes, implementation plans, specs, task lists, or rough feature requests into an ordered Laps queue for Rally. Use when decomposing work into role-aware laps, assigning JUNIOR/SENIOR/UI/VERIFY tasks, adding phase verification, or preparing agent handoffs from OpenSpec or non-OpenSpec plans.
license: MIT
metadata:
  author: rally
  version: "1.0"
---

# Prepare Laps

Turn a plan into a Rally-native Laps queue that another agent can execute one lap at a time. The output should be concrete enough to keep agents on track, but not so prescriptive that it steals ownership of the fine implementation details.

Requires the `laps` CLI. OpenSpec-aware mode also requires the `openspec` CLI.

## Core Rules

- Treat every lap as a handoff to a different agent.
- Use assignees exactly: `JUNIOR`, `SENIOR`, `UI`, `VERIFY`.
- Split each OpenSpec phase into 1-3 implementation laps.
  - 1 lap: mechanical setup, narrow config, isolated file changes, simple docs.
  - 2 laps: familiar cross-module work, implementation plus focused tests, moderate uncertainty.
  - 3 laps: high-risk boundaries, broad tests, UI flows with states, migrations/backcompat, significant refactors.
- Split large test-writing phases aggressively. A broad test phase is often 2-3 laps by layer, harness, or scenario family.
- Add a `VERIFY` lap after any critical lap where mistakes could seriously break a brownfield app, production data path, auth/session/sync behavior, migration, or broad shared contract.
- Otherwise, add `VERIFY` after a few implementation tasks rather than after every lap. A normal cadence is 2-3 `JUNIOR`/implementation laps per `VERIFY`, adjusted by risk and uncertainty.
- After the final phase, add one `VERIFY` lap for the full OpenSpec change or full non-OpenSpec outcome.
- For explicitly lightweight greenfield examples or Rally role-routing smoke tests, one final `VERIFY` lap is often enough. Spend the saved laps on implementation depth, especially a second `JUNIOR` pass for small apps that need fleshing out.
- Verification laps may fix only tiny, safe issues. Anything larger than a few obvious one-liners becomes a new focused lap added to the head of the queue.
- If a lap uncovers a blocker, the agent doing that lap should run `laps add head ...` for the blocker before continuing or marking the lap done.

## Workflow

1. **Orient**
   - Check existing work with `laps list`.
   - Check Rally role support with `.rally/config.toml`, `rally routes check`, and `.rally/agents/` when present.
   - If role instruction files are missing and the user wants a runnable Rally setup, create `.rally/agents/{ROLE}.md` from [role-templates.md](references/role-templates.md) or add an early setup lap to do it.
   - If the input is an OpenSpec change, run `openspec status --change "<name>" --json` and `openspec instructions apply --change "<name>" --json`, then read the returned `contextFiles`.
   - If no change name is provided and more than one active OpenSpec change exists, ask or use the user's latest context. Do not silently plan the wrong change.
   - If the input is not OpenSpec, inspect the provided plan/files and explore the codebase just enough to identify phases, risks, dependencies, and verification commands.
   - For small ad-hoc requests, missing plan files, or very short specs, include the relevant source text directly in each lap instead of only pointing to a file. If a rationale/planning file is tiny, fold the relevant facts into the lap context and avoid making agents chase the file.

2. **Shape phases**
   - Prefer outcome-oriented phases: setup, core behavior, integration, UI, tests, docs/migration, cleanup.
   - Preserve real dependencies, but avoid over-rigid microplans. Give architecture guidance and acceptance criteria; let the assigned agent choose local implementation details.
   - For under-defined work, add an early `SENIOR` or `UI` exploration/design lap before implementation. Its output should be decisions and follow-up head laps if the work expands.

3. **Assign roles**
   - `JUNIOR`: bounded junior/mid-level tasks, mechanical changes, fixture work, narrow bugs, focused tests following existing patterns.
   - `SENIOR`: senior-level tasks, new integrations, architecture-sensitive changes, auth/session/sync/data correctness, migrations, significant new patterns.
   - `UI`: frontend design-heavy work, layout, interaction polish, visual states, copy, responsive behavior.
   - `VERIFY`: verification, code review, OpenSpec verification, test audit, follow-up lap creation.
   - Read [role-templates.md](references/role-templates.md) when you need role introductions or `.rally/agents/{ROLE}.md` starter text.

4. **Write each lap**
   Include:
   - Role intro or role-file assumption.
   - Context: OpenSpec change name, source artifacts, relevant files, preceding phase assumptions.
   - Outcome: the observable thing this lap should leave true.
   - Scope: what to touch and what to avoid.
   - Guidance: architectural constraints, existing patterns, risk notes.
   - Acceptance: tests, commands, smoke checks, docs updates.
   - Handoff: record blockers, add larger blockers to `laps` head, and leave enough summary for the next agent.

5. **Add tasks**
   - Use `laps add tail --json '{"title":"...","description":"...","assignee":"JUNIOR"}'` for planned work in execution order.
   - If inserting urgent blockers, use `laps add head`. When adding multiple head blockers, add them in reverse execution order or use `laps add after <id>`.
   - Prefer JSON mode for multiline descriptions; it avoids shell-escaped `\n` mistakes.
   - Keep assignees on the task, not only in the title. Rally routes from the lap's `assignee` field.
   - Run `laps list` at the end and sanity-check role order, phase review placement, and final full-change verification.

## Verification Laps

For OpenSpec work, a phase `VERIFY` lap should tell the agent to use `openspec-verify-change` against the same change, then focus the report on the phase just completed. The final full-change `VERIFY` lap should run the complete OpenSpec verification and inspect the whole diff.

For non-OpenSpec work, verification laps should read the original request, inspect the diff, run the relevant tests, perform any realistic smoke checks, and report findings first. They should create new head laps for substantive gaps instead of turning review into a hidden implementation phase.

## Skill Maintenance

At the end of a prepare-laps session, update this skill or its references when:

- The user corrects lap size, phase shape, or role assignment.
- A recurring class of lap is too vague, too large, or too small.
- A role intro needs stronger guidance.
- OpenSpec output shape, Laps CLI behavior, or Rally role routing changes.
- A verification failure reveals a better standard check or follow-up-lap pattern.

Keep the main workflow general. Put examples, role wording, and evolving conventions in references.
