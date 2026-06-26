## Draft: Adopt Lint & Fuzz Gates (Tiers 4–5)

Status: drafted 2026-06-23 — intent capture for later formalisation. Sibling of
`harden-ci-correctness-gates`, which covers the mechanical, zero-backlog gates
(race, vet, gofmt, govulncheck, mod-tidy). **Sequence this change after that
one** so a vet/gofmt baseline already exists in CI.

This change is **tooling/CI + test authoring only** — no binary behaviour
change, so no version bump or release is required.

## Why

The correctness gates in the sibling change catch races, known CVEs, and
formatting/module drift — all with ~zero false positives. They do **not** catch
the next class of issue:

- dead code and unused exports,
- unhandled errors and incorrect error wrapping,
- stdlib misuse and subtle logic smells,
- edge-case crashes in parsers and classifiers.

That class needs deeper static analysis (the `staticcheck` family, surfaced via
`golangci-lint`) and property/fuzz testing. Unlike the correctness gates, these
**produce a findings backlog on a mature codebase** and **require new test
authoring**, so they need curation and incremental rollout — which is exactly
why they are split out from the mechanical gates.

Session-grounded motivation:

- The `out` field on `keyboard.Keyboard` went dead when feedback moved to the
  monitor/countdown (0.11.4) — precisely what `unused`/`unparam` flag.
- The codebase has many deliberate `_ = someCall()` sites (intentional ignored
  errors) and shells out heavily to `git` and agent CLIs — so `errcheck` and
  `gosec` would be loud and need careful config, not a blanket enable.
- The per-harness **failure classifiers** (`internal/reliability`), **JSONL
  parsing** (`internal/store`), and **final-snippet truncation**
  (`internal/store/final_snippet.go`) are parser-shaped code over untrusted CLI
  output — the highest-value fuzz targets in the repo.

## Intent

Introduce `golangci-lint` with a **conservative, expanding** linter set and add
**fuzz targets for the highest-risk parsers**, both rolled out incrementally so
the team is never buried under a wall of findings. Land a clean, green baseline
first; tighten over time.

## Candidate work

### A. Add `golangci-lint` with a curated config

- Add `.golangci.yml` with a deliberately small starter set:
  `govet, staticcheck, ineffassign, unused, errorlint`.
- **Defer the noisy ones for now**, given this codebase: `errcheck` (many
  intentional `_ =`), `gosec` (heavy subprocess + file-permission usage from
  shelling out to git/agents). Revisit with excludes once the baseline is
  clean.
- Wire it as its own CI job and a `just lint` recipe. Prefer the official
  `golangci-lint` GitHub Action (own cache) over `go run` for CI speed.

### B. Triage to a clean baseline

- Run once, fix the easy true-positives (e.g. drop the dead `keyboard.Keyboard.out`
  field and its now-unused constructor arg, or document why it stays).
- For intentional patterns, prefer config-level excludes over scattered
  `//nolint` so the intent is centralised.
- Land only when the gate is green, so it stays green and meaningful.

### C. Expand the linter set over time

Once the baseline is clean, add linters incrementally and track each as a
follow-up: `unparam`, stricter `errorlint`, and eventually `errcheck` with a
curated exclude list for the deliberate `_ =` sites. Avoid enabling many at
once.

### D. Decide the formatting standard: `gofmt` vs `gofumpt`

`gofumpt` is a strict superset of `gofmt`. Decide whether to adopt it (and
replace the `gofmt -l` gate from the sibling change with a `gofumpt -l` gate) or
stay on `gofmt`. One-time reformat if adopted. Keeping the decision here means
the formatting standard is owned in one place.

### E. Add fuzz targets for the highest-risk parsers

Author `go test -fuzz` targets (Go-native fuzzing) for:

- the per-harness **failure classifiers** in `internal/reliability` (regex/text
  parsing over arbitrary CLI stderr),
- **JSONL parsing** in `internal/store` (record decode over possibly-corrupt
  on-disk lines),
- **final-snippet truncation** in `internal/store/final_snippet.go` (rune/UTF-8
  boundary handling).

Run a bounded `-fuzztime` in CI (e.g. 30 s per target) or as a scheduled
nightly job, with the seed corpus committed under `testdata/fuzz/`. Decide
which when formalising (see open questions).

### F. (Optional) CodeQL

Add GitHub's CodeQL workflow as an **advisory** SAST scan (free for the repo).
Low-touch; complements `govulncheck` (dependency CVEs) with first-party code
analysis.

### G. Promote `govulncheck` from advisory to blocking

The sibling `harden-ci-correctness-gates` introduces `govulncheck ./...` as an
**advisory** (non-blocking, non-required) job on first rollout, deliberately so
a surprise upstream CVE disclosure cannot wedge an auto-publishing `main` before
the gates have settled. This change owns the **flip to blocking**: once the
lint/fuzz baseline is green and `govulncheck` has run quietly through a
stabilisation window, remove its `continue-on-error: true` and add it to the
required-status-check set on `main`. This is a near-one-line CI change plus a
branch-protection settings update — no structural rework, because harden-ci
built it to be flippable.

The companion branch-protection hardening rides along naturally here: harden-ci
enables protection on `main` with **"Include administrators" off**; flipping it
**on** (so even the maintainer's direct fast-forward must carry green checks) is
the same "tighten once the baseline is stable" step and can land in the same
pass as the govulncheck promotion. Confirm the `rally-release` direct-push flow
still fast-forwards a pre-green `dev` SHA cleanly after both flips.

## Sequencing

1. `golangci-lint` with the conservative set (one-time triage to green) — A, B.
2. Formatting decision — D — alongside or just after A.
3. Fuzz targets — E — incrementally, one parser at a time.
4. Expansion — C — and CodeQL — F — as ongoing follow-ups.
5. Hardening flips — G — `govulncheck` advisory→blocking (and, optionally,
   branch-protection "Include administrators" off→on) once the baseline has
   been stable for a window.

## Open questions

- Starter linter set — is `govet, staticcheck, ineffassign, unused, errorlint`
  the right floor, or include/exclude differently?
- `errcheck`/`gosec`: defer entirely, or enable now with an exclude list?
- `gofumpt` vs `gofmt` — switch the standard or stay?
- Fuzzing: bounded `-fuzztime` in the PR pipeline, a nightly scheduled job, or
  local-only with a committed corpus that CI just re-runs as unit tests?
- `golangci-lint` as a blocking gate from day one, or advisory until the
  baseline settles?

## Out of scope

- The mechanical correctness gates (race, vet, gofmt, govulncheck, mod-tidy) —
  see `harden-ci-correctness-gates`. Note: that change *introduces*
  `govulncheck` (advisory); this change owns only the later *flip to blocking*
  (section G).
- Coverage thresholds / ratchets (a separate later decision).
- Rewriting tests for `t.Parallel()` / parallelism.
- Any change to runtime/binary behaviour.
