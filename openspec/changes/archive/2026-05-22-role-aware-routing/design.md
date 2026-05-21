## Context

Rally today executes a flat round-robin over the configured `--mix`. That's a clean primitive when every iteration is interchangeable, but real workloads aren't uniform: a backend refactor benefits from a heavyweight model, a string-rename benefits from a fast cheap one, and a UI tweak wants something with strong visual reasoning. Rally also has no way to express "use Provider X up to N consecutive runs, then rotate" — both useful for managing rate limits and required by some plan-based provider quotas (e.g. Z.ai's coding plan caps).

Laps already specifies an optional `assignee` field on each lap (per `../laps/SPEC.md`). The v0.4.0 head-pull adapter surfaces it. v0.6.0's job is to act on it: route each lap to a per-role agent list, expressed in `.rally/config.toml`, with quota-aware scheduling that subsumes both rotation and fallback semantics in a single model.

A separate concern: the per-role agent prompt fragments (`SENIOR.md`, `JUNIOR.md`, `UI.md`, etc.) are being authored on a separate track — their *contents* are out of scope for this change. v0.6.0 ships only the parsing, lookup, and injection wiring.

## Goals / Non-Goals

**Goals:**
- Per-role harness/model selection driven by the lap's `assignee` field
- A single scheduler that handles "rotate every N", "use until failure", and "preferred up to N, allowed up to M" without separate policy modes
- Stable shortcut-key references in route entries (riding on v0.5.0's `[providers]` shortcuts)
- Role-instruction-file injection (loader only — file contents are authored separately)
- Manual override via `--agent` that preserves the operator's lever to ignore routing
- Up-front validation of `[routes]` and `[providers]` so typos fail at startup, not mid-relay

**Non-Goals:**
- Authoring `SENIOR.md` / `JUNIOR.md` / `UI.md` / `QA.md` / `VERIFY.md` content — explicitly tracked separately
- Cross-role automatic fallback ("ROLEA exhausted → try ROLEB") — within-list cycling and `default`-route fallback only
- Active freeze detection driving rotation — that's v0.7.0; v0.6.0 only rotates on observed agent unavailability (retry-budget exhaustion, hard error, rate-limit cooldown signal from the executor)
- Renaming or restructuring laps' SPEC — `assignee` already exists; rally just consumes it
- Enriching the lap format with rally-specific fields — keep laps rally-agnostic

## Decisions

### Single scheduler model — quota presence drives behaviour
**Chosen**: One scheduler. No `policy: fallback` vs `policy: rotation` distinction. Quota syntax on each entry encodes the intent:
- No quota → unlimited consecutive use; rotate only on observed failure
- `:N` → rotate after exactly N consecutive runs (or sooner on failure)
- `:N-M` → rotate after N runs if any other entry is available; if all others are exhausted/frozen, continue up to M; then force-wait

**Alternative considered**: Two distinct policies declared per route, with their own syntaxes.

**Why**: Two policies double the surface area for users to learn and double the test matrix. Quota syntax is already familiar from rate-limiting tooling and naturally subsumes both behaviours: "use until failure" is just "no quota," "rotate every N" is `:N`, "preferred N, allowed M" is `:N-M`. The seven canonical scenarios in the proposal exercise every combination; the implementation passes or fails them collectively.

### Quota syntax: positional `:`-split, last segment if numeric
**Chosen**: Each entry is `:`-split. The last segment is the quota iff it matches `^\d+$` or `^\d+-\d+$`. Remaining segments form the agent identifier (1 segment = shortcut key, 2 segments = `harness:model`, 3+ = invalid). Pure-numeric shortcut keys are forbidden in `[providers]` (already enforced by v0.5.0) so `claude:4` parses unambiguously as `claude` + quota `4`, then errors if `claude` isn't a defined shortcut.

**Alternative considered (a)**: Use a different separator for quotas (`*4`, `@4`, `#4`).
**Alternative considered (b)**: Force quotas into a JSON-like key-value form per entry.

**Why**: The `:` separator is already established for `harness:model`; introducing a second separator means users mentally context-switch within a single string. Positional parsing is concise (`claude:opus-4.7:3` is shorter than `claude:opus-4.7,quota=3`) and the v0.5.0 numeric-key prohibition makes it unambiguous. Models with embedded digits (`gpt-5`, `claude-4.5-sonnet`) contain non-digits so they don't collide with the quota pattern.

### Resolution order: `--agent` > lap `assignee` > `default`
**Chosen**: Per iteration, in priority order:
1. `--agent` flag overrides everything (if supplied)
2. Lap's `assignee` (case-insensitive match against `[routes]` keys)
3. `default` route (when assignee is unset or didn't match)

**Alternative considered**: Roll lap-`assignee` and `default` into a single fallback chain.

**Why**: The three sources answer different questions ("operator override," "what does this lap need," "what's the catch-all"). Keeping them ordered keeps the mental model simple. `--agent` is the manual lever; `assignee` is the per-lap policy; `default` is the no-policy escape hatch. Operators learn each layer separately.

### Within-list rotation only — no cross-role fallback
**Chosen**: A non-default role never silently falls back to another non-default role. If a role is undefined and `default` exists, rally falls back to `default` with a warning. If neither exists, rally exits with an error at startup validation.

**Alternative considered**: Allow declared "fallback chains" between roles (e.g. `JUNIOR → SENIOR → default`).

**Why**: Cross-role fallback turns route configuration into a directed graph that's hard to reason about and easy to misconfigure (cycles, dead ends). Operators experiencing role exhaustion want explicit failure ("you said all SENIOR providers are out") rather than silent re-routing to a role meant for different work. The two-layer model (role → default) is enough for v0.6.0; if multi-role chains prove necessary, they're additive in a later release.

### Role names are valid in `--agent` only (the documented exception)
**Chosen**: `--agent` accepts a space-separated list whose entries can be `harness:model[:quota]`, shortcut keys, OR role-name references. The role-name reference inlines the role's full route list into the override roster, optionally capped by a trailing quota that advances the role's internal cursor by N each visit. Role names are NOT valid as entries within `[routes]` itself.

**Alternative considered**: Allow role names as entries in `[routes]`.

**Why**: Allowing role names inside `[routes]` opens the cross-role fallback can described above. `--agent` is operator-controlled at run-time and is naturally a "compose your own roster" surface, where the cognitive cost of a bad reference is bounded to that one invocation. Scenario 6 in the proposal captures the canonical use case (override mostly with one model, but pull from `default` once per loop).

### Per-role instruction file lookup is case-insensitive and deterministic
**Chosen**: Rally scans `.rally/agents/` for any file whose basename (without extension) matches the assignee value case-insensitively. The first hit on a sorted scan wins. The file content is appended to the prompt template after base instructions and before the lap body. Missing file is silent (not an error).

**Alternative considered (a)**: Exact-case match only.
**Alternative considered (b)**: Glob match across multiple files.

**Why**: Operators on case-sensitive filesystems (Linux) and case-insensitive ones (macOS APFS default) would otherwise get inconsistent behaviour. A deterministic sorted scan eliminates the FS-dependence: `assignee: Senior` always picks the same file regardless of OS, and the operator can rename to disambiguate. Multi-file globbing complicates the contract without solving a real problem (the role identifier is a single string).

### Startup validation gates run-time errors
**Chosen**: At startup, rally validates `[routes]` and resolves all shortcut keys. Outcomes:
- Invalid `--agent` syntax → error, exit
- Invalid `[routes]` syntax (some valid, some invalid) → warn + y/N prompt; on confirm, invalid roles silently fall back to `default`
- Invalid/missing `default` AND `[routes]` non-empty → warn-and-prompt as above; if no laps exist in queue, warn-and-exit instead
- Quota out of bounds (`min > max`, negative) → error, exit
- Numeric-only shortcut key in `[providers]` → error, exit (already from v0.5.0)
- Duplicate `[routes]` keys differing only in case → error, exit (case-insensitive matching would collide at lookup)

**Alternative considered**: Lazy validation — fail at first use of a bad route.

**Why**: A relay can run for hours; surfacing a route typo on iteration 47 is hostile. Up-front validation moves every recoverable error to a point where the operator can fix and restart without losing run state. The y/N prompt for partial-failure cases is the compromise: in CI/non-interactive contexts the prompt non-zero-exits cleanly via stdin EOF.

### `rally routes check` is a separate validator subcommand
**Chosen**: A `rally routes check` subcommand parses `[routes]`, resolves shortcuts, verifies quotas, and lists declared routes that no current lap's `assignee` references (info-level). Errors on parse/resolution failure with a clear pointer to the offending entry.

**Alternative considered**: Roll validation into a generic `rally config check`.

**Why**: Routes are the most error-prone part of the config (quota syntax, role names, shortcut references all have to align). A dedicated subcommand makes the validation fast and low-friction in a Makefile or CI step. A generic `config check` could be added later that calls into this and other validators.

### Configuration lives in `.rally/config.toml`, not a separate routes file
**Chosen**: `[routes]` is a top-level table in `.rally/config.toml` alongside the v0.5.0 sections. No separate `routes.yml` or `routes.toml`.

**Alternative considered**: Dedicated `routes.yml` file.

**Why**: Rally's storage convention is "one TOML for all configuration; output data is JSON; laps owns its JSON/JSONL." A separate file fragments the operator's mental model and adds a load-order question (what if both exist?). Single-file keeps the surface coherent.

## Risks / Trade-offs

- **Scheduler complexity makes regressions invisible** → Mitigation: the seven canonical scenarios in the proposal become integration-test backbone. Any deviation is a build-breaker.
- **`--agent` syntax is space-separated; long entries become awkward to type** → Mitigation: quoted form (`--agent "..."`) is the documented usage. Repeated `--agent` flag (accumulating entries) is rejected for v0.6.0 to keep parsing predictable; revisit if operators ask.
- **Case-insensitive role matching depends on FS behaviour for the instruction file** → Mitigation: explicit dir-scan + lowercase compare, not relying on FS case-folding. Tested on Linux ext4 and macOS APFS in CI.
- **Y/N prompt behaviour in non-interactive contexts could surprise CI runs** → Mitigation: stdin EOF causes the prompt to default to N and exit non-zero. CI scripts that genuinely want the relay to start with partial config can pre-validate via `rally routes check` and only invoke `rally relay` when validation passes.
- **Quota-aware scheduler interacts subtly with v0.7.0 freeze detection** → Mitigation: v0.7.0 layers freeze detection on the same scheduler; the v0.6.0 scheduler emits hooks (`onAgentFailed`, `onAgentRecovered`) that v0.7.0 consumes. Designed for extension.
- **Operators may expect `--agent` to override `[routes]` only when the role matches** → Mitigation: documentation is clear that `--agent` overrides everything. The proposal scenarios capture this explicitly.

## Migration Plan

1. **Schema addition**: extend the v0.5.0 config schema with a `[routes]` top-level table. Each entry is a string array of agent specs.
2. **Resolver layer**: extend the v0.5.0 `ResolveAgent` to accept entries with optional trailing quotas. Quota parsing returns a `(specWithoutQuota, min, max int, hasQuota bool)` tuple.
3. **Scheduler**: implement `internal/routing/scheduler.go` consuming a route list and producing a stream of agent selections per iteration. Tracks consecutive-use counters, rotation triggers, exhausted/frozen state per entry.
4. **Routing layer**: per-iteration agent selection now goes through `internal/routing/select.go` which checks `--agent` override, then the lap's `assignee`, then `default`.
5. **Instruction loader**: `internal/prompt/roleloader/` scans `.rally/agents/`, returns content for a given assignee or empty string if not found.
6. **Validation**: `rally routes check` and startup-time validation share the same parser/resolver/checker codepath.
7. **CLI surface**: add `--agent` flag to `rally relay`; add `rally routes check` subcommand.

Rollback: revert v0.6.0. Existing `[routes]` tables in `.rally/config.toml` would be ignored after revert (config loader doesn't recognise the section). Operators using `--agent` would need to fall back to `--mix`. No persistent state on disk needs cleaning up.

## Open Questions

- Whether the scheduler's rotation should be deterministic (fixed cycle order) or stochastic (weighted random per iteration). v0.6.0 ships deterministic; revisit if operators want jitter to spread load across providers in a different way.
- Whether `[routes]` should support per-route overrides for `[reliability]` settings (different freeze thresholds for different harnesses). Out of scope for v0.6.0; v0.7.0 may expose if useful.
- The exact "frozen" criterion that the scheduler treats as exhaustion. v0.6.0 considers an entry frozen when it has consumed its retry budget AND the executor has signalled a non-recoverable error (not just "tried 3 times"). v0.7.0 refines this with active detection.
