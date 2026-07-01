## 1. Baseline & regeneration

- [ ] 1.1 Confirm the working tree is green before adding the gate: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go test -count=1 ./...`. If red, STOP — do not fold unrelated fixes into this tooling change.
- [ ] 1.2 Regenerate the file-size baseline against HEAD (do **not** trust the 2026-07-01 figures in `design.md`): list every production `.go` over 800 lines and every `_test.go` over 1,000 lines (excluding `// Code generated` files, `testdata`, `vendor`, `.git`/`.rally`/`.laps`). This set becomes the grandfather map.
- [ ] 1.3 Regenerate the production internal import graph against HEAD and confirm it still matches the rule tables in `design.md` (Decision 4). Use the checker/reporting implementation or `go list -json ./internal/... ./cmd/...` with `.Imports` only for this production graph; do not mix in `.TestImports`/`.XTestImports`, because v1 internal boundary rules are production-only. If #1/#2 follow-up commits changed any production edge, update the rules to match the **current** graph before encoding them.
- [ ] 1.4 Regenerate the third-party dependency scopes against HEAD for New Relic, `go-toml`, Cobra, huh, and lipgloss; confirm they still match Decision 5 (note `cobra` in `internal/progress`).

## 2. Phase 1 — the checker (`tools/archguard`), advisory

- [ ] 2.1 Create `tools/archguard` as `package main` in the existing module, standard-library-only (for example `go/parser`, `go/token`, `go/ast`, `os`, `path/filepath`, `bufio`, `strings`, plus normal CLI/reporting stdlib packages such as `flag`, `fmt`, and `sort`). Confirm `go mod tidy` produces no diff (no new dependency).
- [ ] 2.2 Implement the repo walk: skip `// Code generated` files, `testdata`, `vendor`, build output, and hidden dirs (`.git`, `.rally`, `.laps`). Parse imports with `parser.ImportsOnly`; count physical lines (newline count, matching `wc -l`).
- [ ] 2.3 Implement the policy engine as a testable unit (exported funcs or a `policy` sub-package): file-size budgets, grandfather map, import-boundary rules, dependency-confinement rules, and `testutil` confinement. Each violation carries a human-readable architectural reason (see `design.md` Diagnostics format).
- [ ] 2.4 Implement run modes: default (warnings + hard, non-zero exit on hard), `--report` (print over-budget files as a paste-ready grandfather map + any import/dep violations), `--ci` (hard-only exit; warnings printed but never fail). Keep `tools/archguard`'s own files under budget.

## 3. Phase 2 — file-size budgets with grandfathered baseline

- [ ] 3.1 Encode budgets: production `.go` warn 500 / hard 800; `_test.go` warn 700 / hard 1,000; generated exempt (require `// Code generated`). A grandfathered file is exempt from the standard hard budget but fails if it exceeds its own recorded cap.
- [ ] 3.2 Paste the grandfather map produced in 1.2 (caps = current actual line counts). Confirm `archguard --ci` exits 0 on the clean tree, and that bumping a grandfathered file by one line in a scratch edit makes it fail (then revert).

## 4. Phase 3 — import-boundary & dependency-confinement rules

- [ ] 4.1 Encode the production-file flagship/composition-root edges: `internal/relay` ↛ `internal/relay/runner`; `relay`/`relay/runner` ↛ `config`/`cli`; `internal/release` ↛ `internal/app`; `internal/app` ↛ {`cli`, `user_prompt`, `laps`}; no `internal/*` imports `internal/cli`.
- [ ] 4.2 Encode the per-package production internal allow-lists from `design.md` Decision 4 (lower packages tight; `cli`/`cmd/rally` are the broad composition layers). Confirm the current tree passes without treating current `_test.go` helper imports as boundary violations.
- [ ] 4.3 Encode dependency confinement (Decision 5): New Relic → `telemetry`; `go-toml` → `config`; Cobra → {`cmd/rally`, `cli`, `progress`}; huh → {`cli`, `user_prompt`}; lipgloss → {`style`, `cli`}. Apply to production and test files alike; do not add broader terminal dependency confinement in this first pass.
- [ ] 4.4 Encode test-helper confinement: non-test files MUST NOT import `internal/testutil`. Confirm the clean tree passes (no production file imports it today).
- [ ] 4.5 Add a deliberate temporary fixture for each rule class (size, import boundary, dep confinement, testutil) under `tools/archguard/testdata`, assert the **diagnostic text**, then ensure no deliberate failure remains in real source.

## 5. Phase 4 — local + CI wiring

- [ ] 5.1 Add a `just arch-check` recipe (`go run ./tools/archguard`) and invoke `just arch-check` from the existing `check: vet` recipe body after the `gofmt -l .` assertion, matching the current `justfile` ordering. `just check` stays green.
- [ ] 5.2 Add an `archguard` step to the `lint` job in `.github/workflows/test.yml` running `go run ./tools/archguard --ci`. No new job, no new required check beyond `lint`; trigger surface (push to `dev`/`main`, PRs) unchanged.

## 6. Verification & docs

- [ ] 6.1 `go test -count=1 ./...` (includes `tools/archguard`), `go vet ./...`, `gofmt -l .` empty, `go run ./tools/archguard --ci` exit 0, `just check` green, `go mod tidy` no diff.
- [ ] 6.2 Document `just arch-check` and the budget/boundary policy briefly in README (or the architecture section), pointing at `tools/archguard` as the source of truth for caps and rules.
- [ ] 6.3 Confirm no Rally runtime file changed, `internal/buildinfo/VERSION` is untouched, and no release is implied by this change.
