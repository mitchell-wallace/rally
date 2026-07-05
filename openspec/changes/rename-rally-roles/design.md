# Design: Roles v2 reconciliation (current tree, 0.13.0-dev)

`roles-v2-design.md` is the behavioural baseline. It was written against
pre-refactor v0.12.0 and deliberately avoids code locations; this document
pins its decisions to the current tree and records the deltas we adopt.
Where the two disagree, this file wins.

## Current-tree inventory (verified 2026-07-05)

Role knowledge today lives in exactly these places:

| Surface | Location | Contents |
| --- | --- | --- |
| Embedded role prompts | `internal/agent_prompt/roles/{junior,senior,ui,verify,recovery}.md` | 10–22-line role snippets composed into the session prompt |
| Managed-content hashes | `internal/agent_prompt/managed_hashes.go` (regen: `scripts/gen_managed_role_hashes.sh`) | SHA-256 of every shipped role body; classifies legacy files as managed vs user-customized |
| Bootstrap defaults | `internal/cli/init_roles.go` `defaultRoleBootstraps` | role → default route + legacy inline instruction bodies (kept only for managed-content classification) |
| Role folder sync | `internal/cli/init_roles.go` `syncRoleFolders` | regenerates `.rally/agents/builtin/<role>.md` from embedded defaults each outing; preserves `.rally/agents/user/` |
| Known-role list | `internal/cli/config.go:203` | interactive config's route-name list |
| Verify prompt gate | `internal/harnessapi/prompt.go` `isVerifyRole` | verify gets gate-style finalize guidance |
| Verify stall-recovery exclusion | `internal/relay/runner/run_attempt_classify.go` `applyStallRecovery` | a trivial commit is not verification evidence |
| Recovery classification | `internal/relay/runner/run_attempt_record.go`, `handoff_only.go` (`ResolvedRoute == "recovery"`), `internal/store/recovery.go` `RecoveryRouteName` | recovery classification plumbing (continue/discard/…/needs_user) |
| Planner guidance | `.claude/skills/prepare-laps/` | role catalog + assignment rules used to decompose work |
| Docs | `README.md` roles/routes sections, `.rally/README.md` template | user-facing role table |

## Decisions

### D1. Role set

Built-ins: `intern, junior, senior, architect, review, verify, qa, recovery`.
`ui` is dropped from generated built-ins. Canonical one-line descriptions come
from baseline §19. Alias: `reviewer → review` only.

### D2. `internal/roles` catalog package (new)

Single source of truth, consumed by everything in the inventory table:

```go
type Mode string        // implement | plan | review | verify | qa | recover
type WritePolicy string // implementation | plan_only | read_only_gate | reconcile

type Spec struct {
    Name            string
    Mode            Mode
    WritePolicy     WritePolicy
    Summary         string   // planner-catalog line (baseline §19)
    DefaultRoute    []string // bootstrap route for init
    RequiredSkills  []string // e.g. review → auto-code-review
    EscalationTargets []string
    GeneratedByDefault bool  // ui: false (spec retained for migration only)
}
```

Custom roles stay legal: unknown assignees resolve to a zero Spec with
`Mode=implement`, plus the existing route-fallback behaviour. A near-miss
warning (levenshtein against built-ins) fires once per relay, non-fatal.
The catalog is NOT a validation gate.

### D3. Name checks become policy checks

- `isVerifyRole` (harnessapi/prompt.go) → `WritePolicy == read_only_gate`
  (now covers verify, qa, review, architect finalize framing; architect is
  `plan_only` but shares the "your commit is your report, not the feature"
  finalize shape — see D6).
- `applyStallRecovery` verify exclusion → excluded for every role whose
  WritePolicy is not `implementation` (a trivial commit is not evidence for
  any gate/plan role).
- Recovery branches (`ResolvedRoute == "recovery"`) → `Mode == recover`.
  `store.RecoveryRouteName` stays (route name, not role semantics).
- `cli/config.go` known-role list → derived from the catalog.

Behavioural parity is required for junior/senior/verify/recovery paths; the
policy refactor must not change what those roles do today.

### D4. Role prompt files

Baseline §20 texts are the starting bodies, edited for Rally house style:
keep each ≤ ~25 lines, reference the laps handoff flow the way the current
`junior.md`/`senior.md` do (`laps done` / `laps handoff`), never duplicate
`general/` finalize/headless content. All eight get embedded files under
`internal/agent_prompt/roles/`. `ui.md` is deleted from the embed; its hashes
REMAIN in managed_hashes.go so legacy builtin/ui.md files are recognized and
cleaned up by migration (D7). Regenerate hashes via the script; historical
hashes are append-only.

### D5. Review ⇢ auto-code-review skill

`review.RequiredSkills = ["auto-code-review"]`. The prompt composer renders a
required-skill block: instruct the driver to load/follow the named skill and
to fail loudly (handoff, not improvisation) if it is unavailable. Rally does
not install skills and does not verify their presence — prompt-level contract
only, documented as such. No skill content is inlined.

### D6. Prompt-composition seams

`internal/harnessapi/prompt.go` BuildPrompt gains role-policy awareness via a
small interface (accept the resolved `roles.Spec`), not via more name
helpers. Architect renders a plan-only finalize variant: it commits planning
artifacts only; instruct that source/test edits are out of scope.

### D7. ui migration

- Embedded ui role and bootstrap entry removed; catalog keeps a
  `GeneratedByDefault: false` tombstone spec so migration can classify.
- `syncRoleFolders`: if `.rally/agents/builtin/ui.md` exists and its content
  matches a managed hash → delete it (managed cleanup). User files under
  `agents/user/` are never touched.
- A `[routes].ui` entry keeps routing (custom role); relay start prints a
  one-line advisory: `ui is no longer a built-in role; treating it as a
  custom role (UI/branding guidance now belongs in repo skills)`.
- README documents the skills migration for UI/branding.

### D8. Default bootstrap routes (new-user defaults, not this machine's)

```
intern    → opencode        junior → opencode      senior → claude
architect → claude          review → codex         verify → codex
qa        → opencode        recovery → claude
```

This machine's `~/.config/rally/config.toml` already carries the real
review/architect routes + `[reasoning]` efforts; add `intern`/`qa` routes
there when this lands (intern: cheap/fast drivers; qa: mid-tier).

### D9. Planner catalog

`prepare-laps` gets the §5 compact catalog + §6.1 decision tree verbatim
(shortened), replacing its JUNIOR/SENIOR/UI/VERIFY guidance. The catalog
lives in the skill, not in rally binaries — rally's only planner-facing
surface is lap assignee strings.

## Risks / invariants

- Custom-role compatibility is the sharp edge: the catalog must never become
  a closed enum (baseline §10). Tests must cover unknown-role fallback.
- Managed-hash history is append-only; deleting old hashes breaks legacy
  migration classification.
- `verify` behaviour today (gate finalize + stall-recovery exclusion +
  head-of-queue followups) must survive the policy refactor unchanged.
- OpenSpec references stay out of role prompts (AGENTS.md tool boundaries).
