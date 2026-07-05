# Rally — Agent Guide

## Terminology

### Hierarchy: relay > outing > try

- **Relay**: a campaign of outings processing a queue of laps (tasks).
- **Outing**: one driver assigned to one lap. A lap can have multiple
  outings if skipped to a different driver. Each outing tracks its own retry
  budget.
- **Try**: one invocation of a driver. An outing can have multiple tries
  (retries).

### Driver

A **driver** is a harness + model combination (e.g. `claude` harness with
`sonnet-4` model, or `opencode` harness with `gemini-2.5-pro` model). Distinct
from a **role**, which is a semantic label for what the driver does.

The orchestrator package `internal/relay/runner` and its `Runner` type keep
their names; "attempt N of M" remains the ordinal phrasing for operator-facing
output while `try` is the record noun.

### Rally, laps, hooks, and role instructions

- **Rally** orchestrates outings: it selects a driver for the current lap,
  builds the prompt, injects project and role instructions, records progress,
  and manages retries or route fallback.
- **Laps** owns the work queue. A lap's `assignee` is a routing label naming a
  role; Rally maps that role to a configured driver via `.rally/config.toml`.
  Built-in roles (roles v2): the implementation ladder `intern`/`junior`/
  `senior` plus the control-and-assurance roles `architect` (plan-only),
  `review` (auto-code-review skill wrapper), `verify` (acceptance evidence),
  `qa` (black-box testing), and `recovery` (state reconciliation). Custom role
  names remain valid; `ui` is retired as a built-in — UI/branding guidance
  lives in repo skills.
- **Role instructions** under `.rally/agents/` tell the already-assigned driver
  how to perform that kind of work. They resolve `user/<role>.md` (your
  overrides) over `builtin/<role>.md` (Rally-managed, regenerated from the binary
  each outing) over the embedded default. They should not redefine routing or
  encourage agents to create laps directly. New work is normally created
  indirectly through the handoff flow.
- **Laps hooks** in `.laps/hooks.json` bridge the agent-facing commands back
  into Rally. The prompt tells agents to finish with `laps done` or
  `laps handoff`; those hooks then reveal the `laps wrapup ...` command that
  records progress or creates follow-up laps.

The intended flow is:

1. Rally reads the current lap from `.laps/` and routes it using the lap's
   assignee.
2. Rally injects `.rally/agents/<assignee>.md` as role guidance for the chosen
   driver.
3. The driver performs the assigned work.
4. The driver calls `laps done` when complete, or `laps handoff` when blocked.
5. The Rally-installed laps hook asks the driver to call `laps wrapup ...`,
   which records progress and, for handoff, creates follow-up laps at the head
   of the queue.

### Tool boundaries: rally, laps, and OpenSpec

- **Laps is Rally's permanent backend**, not one work-queue option among many.
  Rally always drives laps; the two ship and version together. Code may assume
  laps is present.
- **Current Rally source supports laps v0.8.1 or newer.** Rally relies on the
  claim file introduced in the v0.8.x line so bare `laps done` completes the
  lap Rally assigned. Run `rally update` to install or upgrade the bundled
  companion.
- **Commit `.laps/laps.json`**: The work queue state file `.laps/laps.json` tracks
  the campaign's progress and must be committed and pushed to Git. Do not
  gitignore, delete, or omit it from commits.
- **OpenSpec is optional.** Rally is not married to OpenSpec — they're dating.
  Rally core, the executor, and the default role docs (`.rally/agents/<role>.md`)
  stay OpenSpec-agnostic. Nothing in rally should *require* OpenSpec to function
  or feel complete, and OpenSpec-specific references should not leak into
  rally's generic surfaces.
- **OpenSpec coupling lives in the `prepare-laps` skill.** That layer has strong
  OpenSpec support: it decomposes a change into laps and injects OpenSpec-aware
  instructions into a lap *only when that lap has a related change* (e.g. "mark
  off the relevant `tasks.md` boxes"). Smoothing the integration with
  OpenSpec-specific references is expected there — and only there.
- **"Draft" an OpenSpec change = a single `draft.md` only.** When the user asks
  to *draft* a change (as opposed to propose/write/flesh out), create just one
  `draft.md` artifact in the change folder as a substitute for the full
  proposal/design/tasks/specs flow — do not generate the full artifact set. The
  point is to capture intent without premature over-scoping: the change can be
  explored and expanded later when it's ready, avoiding stale file/symbol
  references that go out of date as the codebase moves. See
  `improve-harness-consistency/draft.md` for the shape.

### Prompt package naming

Prompt content lives in packages whose names reflect *who* is being prompted:

- **`internal/user_prompt`** holds prompts authored *for the user* — rally's
  interactive CLI prompts (confirmations, selects, free-text input).
- **`internal/agent_prompt`** holds prompts fed *to the agent* — the embedded
  `general/` (shared finalize/headless snippets) and `roles/` (per-role
  guidance) `.md` sources composed into each agent session prompt.

When adding new prompt content, pick the package by audience, not by feature
area, and keep the distinction intact.

## Observability (New Relic)

Rally reports telemetry through the New Relic Go APM agent. When investigating
relay/outing/try failures, use New Relic APM transactions, errors, and custom
events (`RallyTry`, `RallyFailure`, `RallyDiagnostic`) to understand the
failure.

Use the New Relic CLI as `newrelic`, not a shell alias. On this machine the snap
command is `newrelic-cli.newrelic`, and `/home/mitchell/.local/bin/newrelic`
provides a PATH-visible shim for non-interactive agent shells. The configured
profile is `rally` for account `8182741`; pass `--profile rally --accountId
8182741` on investigative commands so agents do not depend on implicit profile
selection. If authentication looks suspect, first smoke-test with:

```sh
newrelic nerdgraph query '{ actor { user { name email } } }' --profile rally --accountId 8182741
```

Note: Sentry was removed in Rally 0.9.1. Historical issue IDs (e.g., `RALLY-2`,
`RALLY-Q`) mentioned in older release notes or planning docs are Sentry references
and cannot be queried in New Relic.

## Git and commit conventions

Rally auto-commits at several points; agents should understand (but not
reimplement) these conventions. See the "Git and commit conventions"
section in README.md for the full table and state-folding rules.

In short:

- **Setup commits** (`rally: initialize workspace`, `rally: install laps
  hooks`) are path-scoped and always use `--no-verify`.
- **Per-attempt work commits** (`rally: run N attempt M (harness)`) are
  `git add -A` with `--no-verify` unless `run_hooks_on_autocommit` is set.
- **Lap-boundary commits** are agent-authored: use
  `<lap-description>: done` or `<lap-description>: in progress (handoff)`
  as the hook scripts instruct.
- **State folding** folds `.rally/`/`.laps/` bookkeeping into the existing
  commit (amends rally-prefixed HEAD with ` [+state]`; creates a single
  `rally: update state` only when HEAD is not rally-authored).
- **Leftover-work guidance** is injected when the working tree is dirty at
  outing start (excluding `.rally/`/`.laps/`), reminding the agent to review
  and commit those changes first.

## Skill-directed actions (commits and subagents)

When a skill checked into this repo (or one the user names directly) instructs
you to make a **local commit** or **spawn a subagent** as part of its workflow,
do so — its presence in the codebase or the user's clear reference to it is the
authorization. You do not need to stop and ask; that would just re-litigate a
decision the repo has already made. This overrides the general "commit only when
asked / don't spawn subagents unless asked" defaults *for actions the invoked
skill actually prescribes*.

The exception is anything that leaves this machine:

- **`git push` still requires explicit approval** — a skill that commits does not
  thereby authorize a push. Ask first, unless the user's request is itself a call
  to publish (e.g. invoking the release workflow / `rally-release`, which
  implicitly approves the pushes that workflow performs).
- Other outward-facing actions (opening PRs, posting to external services) keep
  their normal confirm-first treatment unless the skill or user clearly scopes
  them in.

## Releasing

Rally uses GoReleaser via GitHub Actions to publish releases. The workflow
triggers on `v*` tags but **skips** GoReleaser if a release for that tag already
exists on GitHub.

### How to cut a release

Tags are created automatically by `.github/workflows/auto-tag.yml` when
`internal/buildinfo/VERSION` changes on `main`. **Do not create or push
`vX.Y.Z` tags by hand** — push a VERSION bump and let CI tag for you.

Before bumping the version, verify that the release secret gate is in place:
- Check that `RALLY_NEW_RELIC_LICENSE_KEY` (secret) and `RALLY_NEW_RELIC_APP_NAME` (variable) are still configured in GitHub before cutting a release.
- Keep the license key secret.
- Do not push tags manually.

1. Update the version in `internal/buildinfo/VERSION` (e.g. `0.2.0`). The
   file is committed under `internal/buildinfo/` so Go's `embed` can read it;
   dev builds (`go build`) report `vX.Y.Z-dev` using this value.
2. `main.Version` stays `"dev"` in source — GoReleaser injects the real
   version via ldflags at build time, which takes precedence over the embed.
3. Commit and push to `main`. Conventionally the commit message is
   `chore: bump version to X.Y.Z`.
4. `auto-tag` will create `vX.Y.Z`, push it to origin, and dispatch the
   release workflow (`.github/workflows/release.yml`).

The CI release workflow will:
- Check whether a GitHub release for this tag already exists.
- If it does, the job succeeds immediately (no-op).
- If it doesn't, run GoReleaser to build binaries and create the release.

### Important notes

- When someone says "bump version" in normal maintenance work, assume that
  means incrementing the patch version in `internal/buildinfo/VERSION` as part
  of the update unless they explicitly ask for a minor or major bump.
- **Don't re-push an existing tag** expecting CI to rebuild. If you need to redo
  a release, delete it first: `gh release delete v0.2.0 && git tag -d v0.2.0 &&
  git push origin :refs/tags/v0.2.0`, then bump VERSION again to re-trigger
  auto-tag (or push a new patch).
- GoReleaser reads the version from the git tag, not the `VERSION` file. The
  `auto-tag` workflow tags from the VERSION file, so the two stay in sync as
  long as you only edit VERSION (never tag by hand).
- The `install.sh` script is uploaded as a release asset (configured in
  `.goreleaser.yaml`).
