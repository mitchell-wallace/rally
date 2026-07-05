# Tasks: Roles v2

Sized for crew-chief delegation (one codex session per lap; chief owns
review + commit). Gates for every lap: `go build ./...`,
`go test ./...`, `gofmt -l .` clean, `go vet ./...`, `go run ./tools/archguard`.

## Lap A — roles catalog package (senior-shaped)

- [x] A1. Add `internal/roles`: `Spec`, `Mode`, `WritePolicy`, the eight
      built-in specs (design D2, descriptions from baseline §19), ui
      tombstone (`GeneratedByDefault: false`), `Lookup(name)` with
      case-insensitive match, `reviewer → review` alias, zero-Spec fallback
      (`Mode=implement`) for unknown names, near-miss suggestion helper.
- [x] A2. Unit tests: built-in list content, ui not generated, alias,
      unknown-role fallback, near-miss suggestions, catalog is not a
      validation gate.

## Lap B — policy wiring (senior-shaped, behaviour-parity critical)

- [x] B1. `harnessapi/prompt.go`: replace `isVerifyRole` with WritePolicy
      awareness (design D3/D6); architect plan-only finalize variant;
      required-skill block rendering (D5).
- [x] B2. `run_attempt_classify.go` stall recovery: exclude all
      non-implementation write policies.
- [x] B3. Recovery branches → `Mode == recover`
      (run_attempt_record.go, handoff_only.go).
- [x] B4. `cli/config.go` role list ← catalog.
- [x] B5. Parity tests: junior/senior/verify/recovery behave byte-identically
      in prompt + lifecycle golden tests except where design says otherwise.

## Lap C — role prompts + bootstrap + migration (junior-shaped)

- [x] C1. Rewrite/add `internal/agent_prompt/roles/*.md` for all eight roles
      (baseline §20 bodies, Rally house style, ≤~25 lines, laps handoff
      references kept); delete `ui.md`.
- [x] C2. Regenerate managed hashes (append-only) via
      `scripts/gen_managed_role_hashes.sh`.
- [x] C3. `init_roles.go`: bootstraps for the eight roles (routes per D8),
      drop ui bootstrap, managed builtin/ui.md cleanup in `syncRoleFolders`,
      `[routes].ui` custom-role advisory at relay start.
- [x] C4. Tests: fresh init generates eight roles (no ui), legacy managed
      ui.md removed, user ui.md preserved, advisory fires once.

## Lap D — planner + docs (intern/junior-shaped)

- [ ] D1. `prepare-laps` skill: §5 catalog + §6.1 decision tree; remove
      JUNIOR/SENIOR/UI/VERIFY framing; keep OpenSpec coupling rules intact.
- [ ] D2. README + `.rally/README.md` template: roles/routes/skills split,
      eight-role table, ui→skills migration note.
- [ ] D3. Sweep stray role-name references (`grep -ri '\bui\b' docs
      README internal/cli` etc.) — prose only, no behaviour.

## Lap E — chief-owned landing

- [ ] E1. Add `intern`/`qa` routes + reasoning entries to this machine's
      user config; `rally routes check`.
- [ ] E2. End-to-end: `rally init all` in a scratch repo; run a 2-lap relay
      (one junior, one verify) on cheap drivers; confirm prompts and
      builtin/ regeneration.
- [ ] E3. Fold roles-v2 terminology into AGENTS.md role bullet list.
- [ ] E4. Land to dev per pipeline; update crew-chief queue laps 2–4 states.
