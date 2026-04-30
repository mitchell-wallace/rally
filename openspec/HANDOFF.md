## Targeted Rally brief

Purpose: add lightweight role-aware routing and role-specific instructions while keeping `microbeads` process-neutral.

The key design is:

```txt
microbeads owns lightweight work tracking.
rally owns execution policy.
.rally/ owns repo-local, user-editable process policy.
```

### Required changes

Add support for an optional `assignee` field on beads.

This should be a plain string, not an enum. Examples:

```yaml
assignee: SENIOR
assignee: JUNIOR
assignee: UI
assignee: QA
assignee: VERIFY
```

`microbeads` should not attach semantics to the value beyond storing/displaying/editing it. No hardcoded validation, no workflow logic, no definition-of-done logic.

Rally should read the bead’s `assignee` value and use it to select:

1. a model/harness route list
2. an optional role instruction file from `.rally/agents/{ASSIGNEE}.md`

Suggested config shape:

```yaml
routes:
  SENIOR:
    - harness: codex
      model: gpt-5.5
    - harness: claude
      model: opus-4.7

  JUNIOR:
    - harness: opencode
      model: zai-coding-plan/glm-5.1
    - harness: opencode
      model: opencode-go/kimi-k2.6
    - harness: gemini
      model: gemini-pro-3.1-preview

  UI:
    - harness: gemini
      model: gemini-pro-3.1-preview
    - harness: claude
      model: sonnet-4.6

  QA:
    - harness: gemini
      model: gemini-pro-3.1-preview
    - harness: opencode
      model: opencode-go/kimi-k2.6
    - harness: claude
      model: sonnet-4.6

  VERIFY:
    - harness: codex
      model: gpt-5.5
    - harness: claude
      model: opus-4.7
```

Fallback should happen within the selected route list when a model times out, rate-limits, or fails before useful work starts. Avoid cross-role fallback by default, especially from `VERIFY` to weaker implementation roles.

If a bead has no `assignee`, Rally should use the current default behavior or a configured default route.

Suggested repo-local files:

```txt
.rally/routes.yml
.rally/agents/SENIOR.md
.rally/agents/JUNIOR.md
.rally/agents/UI.md
.rally/agents/QA.md
.rally/agents/VERIFY.md
```

Rally should inject matching role instructions into the agent prompt in addition to the base Rally instructions and the harness’s own instructions.

### Intended role semantics

`SENIOR` handles architecture-sensitive implementation, integration boundaries, test strategy, auth/session/sync/data correctness, and review-quality fixes.

`JUNIOR` handles bounded implementation tasks following existing patterns, mechanical changes, narrow bug fixes, fixture work, and clearly specified test implementation.

`UI` handles user-facing flow, visual states, copy, usability gaps, theming, layout, and interaction polish.

`QA` performs user-style behavioural verification. It should run the app, act like a user, follow the feature’s smoke/verification plan, and produce a structured report. It should not normally modify production code.

`VERIFY` is the technical gatekeeper. It reads plans/specs/tasks/QA reports/test results, decides whether the change is actually complete, and creates follow-up beads for gaps. It should not rubber-stamp based on checkboxes.

### Explicit non-goals for v0.1

Do not add risk enums, boundary enums, workflow engines, verification-state machines, or hardcoded OpenSpec semantics to `microbeads`.

Do not make Rally understand every possible definition of done. For now, Rally only needs role-aware routing and role instruction loading.

Do not make QA responsible for closing work. QA reports observations. VERIFY decides what blocks completion.

