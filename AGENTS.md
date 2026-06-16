# Rally — Agent Guide

## Terminology

### Hierarchy: relay > run > try

- **Relay**: a campaign of runs processing a queue of laps (tasks).
- **Run**: one runner assigned to one lap. A lap can have multiple
  runs if skipped to a different runner. Each run tracks its own retry budget.
- **Try**: one invocation of a runner. A run can have multiple tries (retries).

### Runner

A **runner** is a harness + model combination (e.g. `claude` harness with
`sonnet-4` model, or `opencode` harness with `gemini-2.5-pro` model). Distinct
from a **role**, which is a semantic label for what the runner does.

### Rally, laps, hooks, and role instructions

- **Rally** orchestrates runs: it selects a runner for the current lap, builds
  the prompt, injects project and role instructions, records progress, and
  manages retries or route fallback.
- **Laps** owns the work queue. A lap's `assignee` is a routing label such as
  `junior`, `senior`, `ui`, or `verify`; Rally maps that role to a configured
  runner via `.rally/config.toml`.
- **Role instructions** in `.rally/agents/<role>.md` tell the already-assigned
  runner how to perform that kind of work. They should not redefine routing or
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
   runner.
3. The runner performs the assigned work.
4. The runner calls `laps done` when complete, or `laps handoff` when blocked.
5. The Rally-installed laps hook asks the runner to call `laps wrapup ...`,
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

## Observability (Sentry)

Rally reports telemetry to the `moved-by-the-word/rally` Sentry project. When
investigating relay/run/try failures, use the **`sentry`** CLI — it is
authenticated (e.g. `sentry issue list moved-by-the-word/rally`,
`sentry issue events moved-by-the-word/RALLY-<n>`). Do **not** use `sentry-cli`:
it is a different, unauthenticated binary on this machine and will fail. (Note
that the `sentry-cli` *skill* refers to that unauthenticated binary; prefer the
`sentry` CLI here.)

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
  run start (excluding `.rally/`/`.laps/`), reminding the agent to review
  and commit those changes first.

## Releasing

Rally uses GoReleaser via GitHub Actions to publish releases. The workflow
triggers on `v*` tags but **skips** GoReleaser if a release for that tag already
exists on GitHub.

### How to cut a release

Tags are created automatically by `.github/workflows/auto-tag.yml` when
`internal/buildinfo/VERSION` changes on `main`. **Do not create or push
`vX.Y.Z` tags by hand** — push a VERSION bump and let CI tag for you.

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
