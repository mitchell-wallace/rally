# Rally — Agent Guide

## Releasing

Rally uses GoReleaser via GitHub Actions to publish releases. The workflow
triggers on `v*` tags but **skips** GoReleaser if a release for that tag already
exists on GitHub.

### How to cut a release

1. Update the version in `VERSION` (e.g. `0.2.0`).
2. Update `main.Version` default in `cmd/rally/main.go` if needed (it stays
   `"dev"` — GoReleaser injects the real version via ldflags at build time).
3. Commit: `git commit -am "Prepare rally v0.2.0 release"`
4. Tag: `git tag v0.2.0`
5. Push both: `git push && git push --tags`

The CI workflow (`.github/workflows/release.yml`) will:
- Check whether a GitHub release for this tag already exists.
- If it does, the job succeeds immediately (no-op).
- If it doesn't, run GoReleaser to build binaries and create the release.

### Important notes

- When someone says "bump version" in normal maintenance work, assume that
  means incrementing the patch version in `VERSION` as part of the update
  unless they explicitly ask for a minor or major bump.
- **Don't re-push an existing tag** expecting CI to rebuild. If you need to redo
  a release, delete it first: `gh release delete v0.2.0 && git tag -d v0.2.0 &&
  git push origin :refs/tags/v0.2.0`, then re-tag and push.
- GoReleaser reads the version from the git tag, not the `VERSION` file. Keep
  them in sync to avoid confusion.
- The `install.sh` script is uploaded as a release asset (configured in
  `.goreleaser.yaml`).
