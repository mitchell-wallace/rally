# Rally CLI Reference For Phone-A-Friend

## Preferred Rally Invocation

```bash
rally relay --new --iterations 1 --agent "<agent-or-route>" "<prompt>"
```

Use `--new` for one-off friend calls so an unfinished relay does not capture the prompt. Use `--resume` only when you intentionally want the prior unfinished relay.

## Built-In Aliases

| Alias | Harness | Binary |
|---|---|---|
| `cc` | `claude` | `claude` |
| `cx` | `codex` | `codex` |
| `ge` | `gemini` | `gemini` |
| `op` | `opencode` | `opencode` |

`--agent` accepts aliases, raw `harness:model` strings, named models, quotas, and configured route names:

```bash
rally relay --new --iterations 1 --agent "cc:1 cx:1 ge:1" "<prompt>"
rally relay --new --iterations 1 --agent "cc:opus op:z" "<prompt>"
rally relay --new --iterations 1 --agent "SENIOR" "<prompt>"
rally relay --new --iterations 1 --agent "op:opencode-go/kimi-k2.6" "<prompt>"
```

In `--agent`, bare aliases rotate one at a time: `--agent "cc ge op"` means `cc:1 ge:1 op:1`. In `[routes]`, a bare alias means "stay here until it fails"; add `:1` in routes for round-robin behavior.

Run `rally routes check` before relying on role routes or named models.

## Prompt Templates

### Read-Only Review

```text
You are being phoned as an independent reviewer. Do not edit files.

Goal:
<what I need help deciding>

Context:
- User request: <summary>
- Relevant files: <paths>
- Current hypothesis: <what I think>
- Constraints: <tests, style, risk, deadlines>

Please return:
1. Key risks or blind spots.
2. Recommended approach.
3. Any files/commands I should inspect.
4. Confidence and assumptions.
```

### UI / Design Direction

```text
You are being phoned for UI/product direction. Do not edit files.

User goal:
<goal>

Product context:
<audience, workflow, current design conventions>

Please propose:
1. A component/page direction.
2. Interaction and state details.
3. What to avoid.
4. A short rationale that I can compare with the user's taste.
```

### Write-Scoped Implementation

```text
You are being delegated a bounded implementation lap.

You may edit only:
- <paths or modules>

Do not touch:
- <excluded paths>

Task:
<concrete outcome>

Acceptance:
- <commands/tests/smoke checks>

If you discover a blocker outside scope, stop and report it instead of broadening the diff.
```

## Direct CLI Fallback

Prefer Rally because it records tries, handles known failure patterns, and keeps model syntax consistent. If Rally is unavailable or you need an isolated read-only one-shot, these are the shapes Rally's built-in adapters use:

```bash
claude -p "$PROMPT" --dangerously-skip-permissions --output-format stream-json --verbose --model "<model>"
codex exec --dangerously-bypass-approvals-and-sandbox --json --model "<model>" "$PROMPT"
GEMINI_CLI_TRUST_WORKSPACE=true gemini --prompt "$PROMPT" --yolo --output-format json --model "<model>"
OPENCODE_PERMISSION='{"*":"allow"}' opencode run "$PROMPT" --format json --model "<model>"
```

Direct CLI output formats differ by tool. Save enough transcript or summary to justify decisions you bring back.
