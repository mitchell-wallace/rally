## Context

`.rally/config.toml` exists today (`internal/config/config_v2.go`) but its schema is narrow: per-harness model strings, `data_dir`, `run_hooks_on_autocommit`. Every other knob the operator might want to set — default iteration count, default mix, fallback prompt content, named model variants — lives only as a CLI flag, hard-coded constant, or per-relay re-typed string. The harness/model coupling is also opaque: to use `opencode` routed through `zai-coding-plan/glm-5.1`, the operator types the full string in every mix, and rally has no stable handle for "the GLM model under opencode."

This blocks two downstream releases:
- v0.6.0 role routing needs route entries that reference named models instead of full harness:model strings.
- v0.7.0 provider rotation needs to refer to alternatives by name when in-route advancing.

A separate concern: the four built-in harnesses (`cc`/`cx`/`ge`/`op`) are the only options today. Adding a new CLI agent (e.g. `droid`) requires a code change. Operators with their own internal CLIs have no path in.

## Goals / Non-Goals

**Goals:**
- A clean, namespaced way to declare named models per harness, referenceable from mix syntax, route entries (v0.6.0), and rotation configs (v0.7.0).
- A repo-local home for defaults that today only live as CLI flags or hard-coded constants.
- A configured source for fallback-prompt content (used in no-backend mode).
- A schema-level path for user-added harnesses with a generic output parser, so adding a new CLI agent does not require a recompile.
- Preserve all existing v0.2.x flat fields at the file root for backwards-compat of in-the-wild configs.

**Non-Goals:**
- Migrating the progress log to TOML — that file stays YAML per the v0.4.0 deferred decision.
- Reintroducing a microbeads-instruction toggle — v0.4.0 decided injection is unconditional in microbeads-backed mode.
- A full schema-version migration framework — `schema_version` is recorded for future use but v0.5.0 only emits a warning on mismatch.
- Multiple output strategies for user-defined harnesses — v0.5.0 ships only `tail`. Block-and-report style parsers (as used by built-in claude) stay hard-coded for built-ins; user harnesses get one knob.
- Overriding built-in harness behaviour. Built-ins keep their hard-coded executor; `[harness.cc]` can declare model names but cannot supply a `command`.
- Exposing every internal knob as config — only the four sections below; everything else stays code-default.

## Decisions

### Harnesses are top-level; named models live under them
**Chosen**: `[harness.<name>]` is the unit of harness configuration. Named models live under `[harness.<name>.models]` as a string→string map (model-name → model-string). A mix entry of the form `<harness>:<model-name>` resolves by looking up the model name in that harness's table.

**Alternative considered (a)**: A flat `[providers]` table mapping arbitrary keys (`op:z`, `op:gk`) to (harness, model) pairs.
**Alternative considered (b)**: Inline shortcuts as a flat string-valued map.

**Why**: Model strings are already harness-namespaced in the wild (opencode prefixes with `provider/`, claude uses `claude-X-Y` slugs). Anchoring the name under its harness reflects the underlying reality. It also collapses the special-case "shortcut key" concept into the existing harness/model surface — there are no shortcut keys; there are just harnesses and the models they know about. Model names are scoped, so `cc:opus` and `op:opus` can coexist without ambiguity.

### Right-of-colon disambiguation: digits → weight, identifier → model name
**Chosen**: For a mix token of the form `<alias>:<right>`:
- if `<right>` is all digits → it is a weight on the bare harness (existing v0.2.x quota syntax).
- if `<right>` is an identifier (`^[A-Za-z][A-Za-z0-9_-]*$`) → it is a model name under that harness.

Model names SHALL NOT be numeric-only. Weights SHALL be applied only to bare aliases in v0.5.0; model-named entries are not weighted (`cc:opus:2` is a parse error in v0.5.0).

**Alternative considered**: Allow any string as a model name and disambiguate via a different separator (`@`, `/`).

**Why**: The colon is already the separator. The digit-vs-identifier rule is a pure syntactic check on the right-hand side, with no lookup required to decide which arm to take. It coexists cleanly with the v0.2.x quota syntax — `cc:2` means exactly what it always meant — and side-steps the v0.6.0 quota-on-models question by reserving `cc:opus:2` as a future syntax. Forbidding numeric-only model names is the same constraint as before, expressed cleanly.

### User-defined harnesses via templated `command`
**Chosen**: A `[harness.<name>]` entry that declares a `command` array registers a new harness. The command is shell-tokenized as written (no shell interpolation): each element is passed verbatim to `exec`, with two placeholders substituted at run time:
- `$MODEL` → the resolved model string for this run.
- `$PROMPT` → the prompt body for this run. If `$PROMPT` does not appear anywhere in `command`, the prompt is piped to the harness on stdin instead.

Output is captured via the `output_strategy` field. v0.5.0 ships one strategy: `"tail"`, which keeps the last `output_lines` lines of combined stdout+stderr (default 40) and surfaces them as the run's output.

Built-in harnesses (`cc`/`cx`/`ge`/`op`) SHALL NOT declare `command` or `output_strategy`. Their behaviour remains hard-coded; the loader rejects either field on a built-in entry.

**Alternative considered (a)**: A single command string with shell interpolation.
**Alternative considered (b)**: Multiple output strategies (block-and-report, last-n, json-extract) selectable for any harness, including built-ins.

**Why**: Array-of-strings avoids shell-quoting hazards and makes the substitution rule trivial. `$MODEL`/`$PROMPT` are the minimum viable templating surface — anything more (env injection, working-dir, timeout) can land additively in a follow-up without breaking the schema. Restricting v0.5.0 to one parser keeps the code path narrow; built-ins keep their bespoke parsers because each was tuned to a specific CLI's output format and re-expressing them as schemas would be a bigger change than this one is scoped for.

### Resolve harness/model at config load, not at use
**Chosen**: When `config.toml` parses, every `[harness.<name>.models]` entry is validated, and every later reference (in a mix, route, or rotation) is resolved at parse time against the resolved table. Unresolved model names produce a `did-you-mean` error referencing the closest-matching defined names *within the same harness*.

**Alternative considered**: Lazy resolution — resolve when the agent is first selected.

**Why**: Lazy resolution means a typo in a route entry doesn't surface until run N when that entry is reached, which can be hours into a relay. Up-front validation moves the failure to startup. Scoping `did-you-mean` per-harness keeps suggestions on-target — `op:gp` should suggest `op:gk` and `op:z`, not `cc:opus`.

### Drop the microbeads-instruction toggle (alignment with v0.4.0)
**Chosen**: `[microbeads]` contains only `instructions_file = "..."` (a path to the content rally injects when in microbeads-backed mode). There is no `instructions = "auto"|"include"|"skip"` toggle — injection is unconditional in microbeads-backed mode, omitted in no-backend mode.

**Alternative considered**: Keep the `instructions` toggle from the original v0.5.0 draft.

**Why**: v0.4.0 already decided injection is unconditional in microbeads-backed mode. Carrying a toggle in v0.5.0 would re-introduce the very surface v0.4.0 just removed. The only configurable piece is *what content* gets injected — that's `instructions_file`.

### `[fallback].instructions_file` only used in no-backend mode
**Chosen**: When rally is in no-backend mode AND no ready bead exists, rally injects the contents of `[fallback].instructions_file` as the prompt. A built-in default fallback ships with rally for workspaces that don't configure one. In microbeads-backed mode the fallback file has no effect.

**Alternative considered**: Fallback file used always, with bead body appended when present.

**Why**: The fallback exists specifically because there is no bead. Injecting it alongside a real bead body would dilute the bead's instructions and confuse the agent. Scoping fallback to "no bead" preserves the bead's primacy when one exists.

### Preserve flat fields at root; new sections are purely additive
**Chosen**: Existing fields (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`) stay at the file root. They continue to act as the *unnamed default model* for each built-in harness — referenced by a bare alias in a mix (`cc` with no `:model-name` suffix). New sections (`[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]`) live alongside them. CLI flags continue to override config values.

**Alternative considered**: Move all flat fields under `[harness.cc] default_model = "..."` shape.

**Why**: Existing `.rally/config.toml` files in the wild would all break, costing every user a manual migration for a purely cosmetic gain. The flat fields work fine; new sections expand the surface without disturbing the established part of it.

### Add a `schema_version` field but only warn on mismatch in v0.5.0
**Chosen**: The TOML root gains a `schema_version = 2` field. v0.5.0 reads it; if absent, treat as version 1 and accept the file silently. On mismatch with what rally expects, log a warning but proceed. v0.6.0+ may use this to block load.

**Alternative considered (a)**: No version field, evolve schema implicitly.
**Alternative considered (b)**: Hard error on mismatch from v0.5.0.

**Why**: Implicit evolution becomes painful by v0.6.0. Hard error from v0.5.0 surprises users who didn't write the field. Warn-then-load gives us a soft migration runway: existing configs load, get auto-bumped on next write, and v0.6.0+ can tighten if needed.

### No legacy `.rally/config` env-style loader to remove
**Chosen**: Section omitted from this change. Verification confirmed the env-style loader is not in tree; the only config loader is `config_v2.go` (TOML).

**Why**: The original v0.5.0 draft included a "delete legacy loader" section. Investigation found nothing to delete — the file format was apparently never implemented, or was removed in an earlier release. The change reduces to a one-line verification task.

## Risks / Trade-offs

- **Two harness:model representations coexist (raw string and named model)** → Mitigation: the parser is the single resolution point; downstream code only sees the resolved `(harness, model)` tuple. Tests cover both forms producing identical resolved values.
- **`did-you-mean` suggestions are noisy if model names are numerous** → Mitigation: cap at 3 suggestions ranked by Levenshtein distance, scoped to the same harness; if none are within a small threshold, just list valid names.
- **User-defined harness `command` is a code-execution surface** → Mitigation: command is run with the same privileges as rally itself; the operator authored `.rally/config.toml`, so this is no broader than what they could do with a CLI flag. Templating is positional substitution, not shell interpolation, so `$PROMPT` containing shell metacharacters is safe.
- **`schema_version` warn-only is easy to ignore** → Mitigation: the warning prints a one-line "schema mismatch — please update or run `rally config check`" message; it's not a slap, but it's visible. v0.6.0+ tightens behaviour as the format stabilises.
- **Fallback file path could resolve to a missing file** → Mitigation: when the fallback path is needed and missing, rally falls back to the built-in default content. No hard error — a missing fallback is weak signal for a no-bead session, not a startup blocker. The "missing file" warning is emitted at first-use, not at config load (avoids noise for workspaces that never enter no-backend + no-bead).
- **Tail-only output strategy may be inadequate for some user harnesses** → Mitigation: 40 lines covers most short-form output; operators can raise `output_lines`. Richer parsers can land additively in a later release without breaking the schema.

## Migration Plan

1. **Schema additions**: extend `internal/config/config_v2.go` (or split as `v3.go` if the diff is sizeable) with `[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]` sections and `schema_version`. Existing fields untouched. New fields default to zero values when absent.
2. **Resolution layer**: add `ResolveAgent(spec string) (harness, model string, err error)` to the config layer. Mix parsing, route parsing (v0.6.0), and rotation parsing (v0.7.0) all funnel through this single resolver.
3. **Mix parsing extension**: update the relay-runner's mix parser to call the new resolver. Existing `cc:2 cx:1` weighted form continues to work; `harness:model-name` entries resolve via `[harness.<harness>.models]`.
4. **User-harness executor**: add a generic harness path that runs `command` with `$MODEL`/`$PROMPT` substitution and the tail-N output parser. Wire it into the executor selection so a `harness.<name>` with a `command` field dispatches there instead of the built-in path.
5. **Fallback wiring**: extend the prompt-building path so that no-backend mode + no-ready-bead substitutes `[fallback].instructions_file` content (or built-in default) for the bead body.
6. **Defaults wiring**: read `[defaults].iterations` and `.mix` at relay startup; CLI flags continue to override.
7. **Schema version handshake**: emit `schema_version = 2` on every write; warn on read-time mismatch.
8. **Verification**: confirm no legacy env-style `.rally/config` loader exists in tree (one-line grep check).

Rollback: revert v0.5.0. Existing `.rally/config.toml` files keep working since the new sections were additive. Workspaces that adopted named models or user-defined harnesses would need to expand them back to raw strings or lose access to those harnesses respectively — the release notes call this out.

## Open Questions

- Whether `[harness.<name>]` should support per-harness env-var injection (e.g. `OPENCODE_API_KEY` overrides). For v0.5.0, env handling stays exactly as it is today (set in the shell or via systemd). Revisit if multi-key workflows surface.
- Whether `[defaults].mix` should accept a "named mix" (e.g. `mix = "balanced"` looking up a named list elsewhere). Out of scope for v0.5.0; named mixes can land in v0.6.0 alongside roles or later.
- Whether v0.6.0 should re-introduce a `cc:opus:2` weight-on-named-model syntax. Reserved for that release; v0.5.0 errors on the third colon segment.
