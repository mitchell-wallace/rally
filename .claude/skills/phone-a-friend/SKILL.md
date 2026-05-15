---
name: phone-a-friend
description: Delegate bounded design, review, debugging, or implementation questions to another local agent CLI through Rally. Use when an agent needs an independent second opinion, model-specific critique, CLI-based delegation, or user-visible comparison between model outputs; includes Rally agent syntax, model notes, failure recording, and feedback calibration.
license: MIT
metadata:
  author: rally
  version: "1.0"
---

# Phone A Friend

Use another agent as a real collaborator through CLI delegation. The friend should receive a bounded ask, enough context to be useful, and explicit rules about whether it may edit files.

Requires the `rally` CLI for the preferred workflow. Direct fallback requires the relevant agent CLI.

## When To Use

- You need an independent design, architecture, UI, testing, or code-review perspective.
- You are stuck on a failure and want a second debugging hypothesis.
- The user asks for another model or agent to contribute.
- A task would benefit from comparing model strengths, such as asking Gemini for UI/component ideas or Codex/Claude for review.

Do not delegate just to feel busy. Keep the local agent responsible for integrating, verifying, and explaining the result.

## Default Workflow

1. **Choose the delegation shape**
   - Read-only: ask for a plan, critique, review, or debugging hypotheses. Tell the friend not to edit files.
   - Write-scoped: only when the user has asked for agent delegation or the scope is clearly safe. Give owned paths and review the diff afterward.
   - If the friend may edit files in the main workspace, start from a known `git status`, make the write scope disjoint from your own current edits, and inspect the diff before accepting anything.
   - If the edit could collide with active work, use a separate worktree or convert it into a lap for Rally instead of ad hoc delegation.
   - Laps-backed: for real implementation work, prefer creating/using a lap so Rally has role, queue, and handoff context.

2. **Gather context**
   - Run `git status --short`.
   - Identify relevant files, commands, failing output, user intent, and constraints.
   - Remove secrets, private tokens, and irrelevant logs.
   - State what answer shape you want: recommendation, patch, critique, test plan, risk list, or ranked options.

3. **Pick a friend**
   - Read [model-notes.md](references/model-notes.md) for current slugs, user feedback, strengths/weaknesses, and failure signatures.
   - Read `.rally/config.toml` and run `rally routes check` when using repo routes or named models.
   - Use `ge`, not `gm`, for Gemini. Do not replace user-provided model slugs with older guesses.

4. **Run through Rally**
   - Prefer:
     ```bash
     rally relay --new --iterations 1 --agent "<agent-or-route>" "<prompt>"
     ```
   - Examples:
     ```bash
     rally relay --new --iterations 1 --agent "ge:gemini-3.1-pro-preview" "<read-only UI critique prompt>"
     rally relay --new --iterations 1 --agent "cx:gpt-5.4-mini" "<focused code review prompt>"
     rally relay --new --iterations 1 --agent "SENIOR" "<architecture review prompt>"
     ```
   - Use `rally tail` in another terminal if you need the live transcript.
   - Read [rally-cli.md](references/rally-cli.md) for agent syntax, direct CLI fallback, and prompt templates.

5. **Integrate**
   - Treat the friend as evidence, not authority.
   - Summarize what it contributed, what you accept, what you reject, and why.
   - Verify any code or claims locally.
   - For subjective output, occasionally ask the user for calibration: "gemini-3.1-pro-preview designed this component direction; how do you like it?" Do this when the user's taste matters, not after every delegation.

6. **Record learning**
   - Update [model-notes.md](references/model-notes.md) when the user assigns a strength/weakness to a model, a model produces a notably good/bad result, a slug changes, or a failure/rate-limit signature appears.
   - Record concrete evidence: date, model slug, task type, observed behavior, and whether it came from the user or an agent run.

## Failure Handling

If the friend stalls, rate-limits, or exits strangely:

- Check `.rally/relays/relay-N.log`, `.rally/tries.jsonl`, `.rally/agent_status.jsonl`, and `rally tail`.
- Classify whether this is auth, rate limit, freeze, bad model slug, bad custom harness config, or a genuine task failure.
- If the failure signature is new or recurring, update `model-notes.md` with the exact symptom and recommended response.
- Try a cheaper/faster model only when that still answers the original question. Otherwise report the degraded state.

## Skill Maintenance

At the end of a phone-a-friend session, update this skill or its references when:

- The user gives taste or quality feedback about a model.
- A new model slug works or fails.
- A model's strengths, weaknesses, cost, latency, auth state, or rate-limit behavior changes.
- A prompt pattern reliably produces better or worse friend output.
- Rally CLI syntax or route behavior changes.

Keep durable model knowledge in `model-notes.md` and CLI mechanics in `rally-cli.md`.
