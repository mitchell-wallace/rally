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
