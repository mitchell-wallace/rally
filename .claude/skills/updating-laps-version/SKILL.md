---
name: updating-laps-version
description: Bump the minimum required laps version across Rally's codebase. Use when MinLapsVersion needs to change, or when auditing that all laps version references are consistent.
license: MIT
metadata:
  author: rally
  version: "0.1"
---

# Updating Laps Version

When Rally starts relying on a newer laps CLI feature, the minimum version must be bumped in multiple locations. This skill ensures all references stay in sync.

## Locations that reference laps version

| File | What to update | How |
|------|---------------|-----|
| `internal/release/release.go` | `MinLapsVersion` constant | Change the string literal |
| `.github/workflows/test.yml` | CI install `go install ... @vX.Y.Z` | Change the `@v` suffix |
| `AGENTS.md` | "supports laps vX.Y.Z or newer" | Update the version number |
| `README.md` | "support laps vX.Y.Z" | Update the version number |
| `openspec/specs/tooling-distribution/spec.md` | "SHALL be laps vX.Y.Z" | Update the version number |
| `.claude/skills/prepare-laps/SKILL.md` | "requires laps CLI vX.Y.Z" | Update the version number |
| `internal/laps/version_test.go` | Tests referencing `MinLapsVersion` | No change needed — reads the constant |
| `install.sh` | Laps install | No change needed — fetches latest release dynamically |

## Steps

1. Update `MinLapsVersion` in `internal/release/release.go`.
2. Update the `@v` version in `.github/workflows/test.yml` to match.
3. Search for the old version string across `AGENTS.md`, `README.md`, `openspec/specs/tooling-distribution/spec.md`, and `.claude/skills/prepare-laps/SKILL.md` and update each occurrence.
4. Run `go test -count=1 ./internal/laps/... ./internal/release/...` to verify version checks still pass.
5. Run `go vet ./...` and `gofmt -l .` to verify no regressions.
6. Commit with message `chore: bump MinLapsVersion to X.Y.Z`.
