---
name: phone-a-friend
description: Call another agent vlia cli as a real collaborator, use only when told explicitly to phone a friend, otherwise prefer your harness's native subagent tool.
license: MIT
metadata:
  author: rally
  version: "1.0"
---

# Phone A Friend

Use another agent as a real collaborator through CLI delegation. The friend should receive a bounded ask, enough context to be useful, and explicit rules about whether it may edit files.

Delegate bounded design, review, debugging, or implementation questions to another local agent CLI through Rally. Use when an agent needs an independent second opinion, model-specific critique, CLI-based delegation, or user-visible comparison between model outputs; includes Rally agent syntax, model notes, failure recording, and feedback calibration.

Two delegation channels:

- **Rally** (`rally relay ...`) — preferred when available. Records tries, handles failure patterns, keeps model syntax consistent, and feeds back into role routes.
- **Direct headless CLI** (`agy --print ...`, `claude -p ...`, `codex exec ...`, `gemini --prompt ...`, `opencode run ...`) — first-class fallback. Use when Rally is unavailable, when you want a single isolated one-shot, or when the user names the CLI directly.

See [rally-cli.md](references/rally-cli.md) for full CLI shapes and prompt templates.

## Sandbox & Permission-Skipping Policy

Headless CLIs typically require permission-skip flags (`--dangerously-skip-permissions`, `--dangerously-bypass-approvals-and-sandbox`, `--yolo`, `OPENCODE_PERMISSION='{"*":"allow"}'`) to run unattended. These flags are dangerous in untrusted environments — they let the friend run arbitrary commands without prompting.

Decide based on the current working directory:

- **Sandbox (skipping allowed):** cwd looks like `/workspace`, `/sandbox`, `/tmp/...`, a container/devcontainer mount, an ephemeral CI checkout, or another disposable root. Permission-skip flags are fine — the blast radius is the sandbox itself.
- **Host machine (skipping NOT allowed without explicit user opt-in):** cwd is inside the user's home directory, especially `~/Documents/...`, `~/Code/...`, `~/projects/...`, or any other long-lived working directory. Do not pass permission-skip flags. Either:
  1. Route through Rally (it manages its own isolation/recording), or
  2. Run the friend in read-only mode (no edit flags, no `--yolo`, etc.) and have it produce a plan/diff/critique for you to apply, or
  3. Ask the user before passing a skip flag, and quote the exact command you want to run.

If the environment is ambiguous (e.g. a mounted host directory inside a container), treat it as host until proven otherwise.

## When To Use

**Only use this skill when the user explicitly asks to phone a friend or delegate to another model/CLI.** For all other subagent needs, prefer your harness's native subagent tool (e.g. the Task tool in opencode).

When explicitly requested:
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

4. **Dispatch the call**
   - **Timeouts:** agent CLIs can be slow. Use at least 20 minutes (1200000ms) for direct CLI calls. For `rally relay` shell commands that may run for hours, set timeout accordingly or omit it.
   - **Through Rally (preferred):**
     ```bash
     rally relay --new --iterations 1 --agent "<agent-or-route>" "<prompt>"
     ```
     Examples:
     ```bash
     rally relay --new --iterations 1 --agent "ge:gemini-3.1-pro-preview" "<read-only UI critique prompt>"
     rally relay --new --iterations 1 --agent "cx:gpt-5.4-mini" "<focused code review prompt>"
     rally relay --new --iterations 1 --agent "SENIOR" "<architecture review prompt>"
     ```
     Use `rally tail` in another terminal if you need the live transcript.
   - **Direct headless CLI (when Rally is unavailable or unnecessary):** apply the sandbox policy above before adding any permission-skip flag. Prefer `opencode` over `claude -p` as the default — Anthropic will soon bill `claude -p` usage at a higher rate to steer non-interactive traffic away from the interactive CLI, so opencode is the cheaper default friend.
     ```bash
     # In a sandbox (e.g. cwd /workspace): permission-skip env var is OK.
     OPENCODE_PERMISSION='{"*":"allow"}' opencode run "$PROMPT" --format json --model "<model>"

     # On the host machine (e.g. cwd ~/Documents/...): drop the env var; read-only friend.
     opencode run "$PROMPT" --format json --model "<model>"
     ```
   - Read [rally-cli.md](references/rally-cli.md) for the full per-CLI shapes, agent syntax, and prompt templates.

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

- Check relay logs, `.rally/state/tries.jsonl`, `.rally/state/agent_status.jsonl`, and `rally tail`.
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
