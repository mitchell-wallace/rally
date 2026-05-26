# Rally CLI Reference For Phone-A-Friend

## Preferred Rally Invocation

```bash
rally relay --new --iterations 1 --agent "<agent-or-route>" "<prompt>"
```

Use `--new` for one-off friend calls so an unfinished relay does not capture the prompt. Use `--resume` only when you intentionally want the prior unfinished relay.

## Built-In Aliases

| Alias | Harness | Binary |
|---|---|---|
| `ag`/`agy` | `antigravity` | `agy` |
| `cc` | `claude` | `claude` |
| `cx` | `codex` | `codex` |
| `ge` | `gemini` | `gemini` |
| `op` | `opencode` | `opencode` |

`--agent` accepts aliases, raw `harness:model` strings, named models, quotas, and configured route names:

```bash
rally relay --new --iterations 1 --agent "cc:1 cx:1 ge:1" "<prompt>"
rally relay --new --iterations 1 --agent "agy:Gemini 3.5 Flash (High)" "<prompt>"
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

## Direct Headless CLI

Prefer Rally because it records tries, handles known failure patterns, and keeps model syntax consistent. Use direct CLI when Rally is unavailable, the user names a CLI explicitly, or you need an isolated one-shot.

### Sandbox vs Host (must read before adding skip flags)

Permission-skip flags (`--dangerously-skip-permissions`, `--dangerously-bypass-approvals-and-sandbox`, `--yolo`, `OPENCODE_PERMISSION='{"*":"allow"}'`) let the friend run arbitrary tool calls without prompting. Allowed only in a sandbox.

- **Sandbox — skip flags OK.** cwd matches `/workspace`, `/workspace/...`, `/sandbox`, `/tmp/...`, or another disposable container/CI root.
- **Host — skip flags NOT OK.** cwd is inside the user's home directory, especially `~/Documents/...`. Run the friend in read-only mode (drop the skip flag), route through Rally, or ask the user before adding the flag and quote the exact command.
- **Ambiguous mounts** (e.g. a host directory bind-mounted into a container): treat as host.

A quick `pwd` check before the call is enough. If `pwd` starts with `/home/<user>/` or `/Users/<user>/`, it's host.

### Sandboxed (skip flags applied — same shapes Rally's adapters use)

```bash
agy --print-timeout=20s --dangerously-skip-permissions --print "$PROMPT"
claude -p "$PROMPT" --dangerously-skip-permissions --output-format stream-json --verbose --model "<model>"
codex exec --dangerously-bypass-approvals-and-sandbox --json --model "<model>" "$PROMPT"
GEMINI_CLI_TRUST_WORKSPACE=true gemini --prompt "$PROMPT" --yolo --output-format json --model "<model>"
OPENCODE_PERMISSION='{"*":"allow"}' opencode run "$PROMPT" --format json --model "<model>"
```

### Host (read-only — skip flags removed)

```bash
agy --print-timeout=20s --print "$PROMPT"
claude -p "$PROMPT" --output-format stream-json --verbose --model "<model>"
codex exec --json --model "<model>" "$PROMPT"
gemini --prompt "$PROMPT" --output-format json --model "<model>"
opencode run "$PROMPT" --format json --model "<model>"
```

In host mode, instruct the friend in the prompt itself to produce a plan, diff, or critique rather than apply changes — the absence of skip flags will surface a permission prompt on any write attempt, which is the desired safety net.

Direct CLI output formats differ by tool. `agy` 1.0.0 has no model flag; Rally changes `~/.gemini/antigravity-cli/settings.json` for configured Antigravity model runs and restores it afterward. Save enough transcript or summary to justify decisions you bring back.
