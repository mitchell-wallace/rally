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
- Strongly-typed `(harness, model)` records flowing through the relay-runner cycle, replacing today's stringly-typed harness-only cycle.

**Non-Goals:**
- Migrating the progress log to TOML — that file stays YAML per the v0.4.0 deferred decision.
- Reintroducing a microbeads-instruction toggle — v0.4.0 decided injection is unconditional in microbeads-backed mode.
- A full schema-version migration framework — `schema_version` is recorded for future use but v0.5.0 only emits a warning on mismatch.
- Multiple output strategies for user-defined harnesses — v0.5.0 ships only `tail`. Block-and-report style parsers (as used by built-in claude) stay hard-coded for built-ins; user harnesses get one knob, expanded if and when the need surfaces.
- Overriding built-in harness behaviour. Built-ins keep their hard-coded executor; `[harness.cc]` can declare model names but cannot supply `command` / `output_strategy` / `tail_stream`.
- Exposing every internal knob as config — only the four sections below; everything else stays code-default.

## Decisions

### Harnesses are top-level; named models live under them
**Chosen**: `[harness.<name>]` is the unit of harness configuration. Named models live under `[harness.<name>.models]` as a string→string map (model-name → model-string). A mix entry of the form `<harness>:<model-name>` resolves by looking up the model name in that harness's table.

**Alternative considered (a)**: A flat `[providers]` table mapping arbitrary keys (`op:z`, `op:gk`) to (harness, model) pairs.
**Alternative considered (b)**: Inline shortcuts as a flat string-valued map.

**Why**: Model strings are already harness-namespaced in the wild (opencode prefixes with `provider/`, claude uses `claude-X-Y` slugs). Anchoring the name under its harness reflects the underlying reality. It also collapses the special-case "shortcut key" concept into the existing harness/model surface — there are no shortcut keys; there are just harnesses and the models they know about. Model names are scoped per harness, so `cc:opus` and `op:opus` can coexist without ambiguity.

### Right-of-colon disambiguation: digits → weight, identifier → model name
**Chosen**: For a mix token of the form `<alias>:<right>`:
- if `<right>` is all digits → it is a weight on the bare harness (existing v0.2.x quota syntax).
- if `<right>` is an identifier (`^[A-Za-z][A-Za-z0-9_-]*$`) → it is a model name under that harness.

Model names SHALL NOT be numeric-only. Weights SHALL be applied only to bare aliases in v0.5.0; model-named entries are not weighted (`cc:opus:2` is a parse error in v0.5.0).

**Alternative considered**: Allow any string as a model name and disambiguate via a different separator (`@`, `/`).

**Why**: The colon is already the separator. The digit-vs-identifier rule is a pure syntactic check on the right-hand side, with no lookup required to decide which arm to take. It coexists cleanly with the v0.2.x quota syntax — `cc:2` means exactly what it always meant — and side-steps the v0.6.0 quota-on-models question by reserving `cc:opus:2` as a future syntax. Forbidding numeric-only model names is the same constraint as before, expressed cleanly.

### `AgentMix.Cycle` carries typed `(harness, model)` records
**Chosen**: The relay-runner's `AgentMix.Cycle` becomes a slice of a small typed struct (e.g. `[]ResolvedAgent` where `ResolvedAgent struct { Harness, Model string }`), replacing today's `[]string` of harness aliases. `AgentForRun` returns the resolved record. Every caller is updated.

**Alternative considered**: Keep `Cycle []string` and pack `(harness, model)` into a single delimited string per entry, splitting at use.

**Why**: Soft typing is risky — packing/splitting a delimited string at every use is exactly the kind of fragile pattern this change is trying to remove. A typed record is the same complexity at parse time and removes a class of bugs from every downstream caller.

### User-defined harnesses via `command` + declarative `model_flag`
**Chosen**: A `[harness.<name>]` entry that declares a `command` array registers a new harness. The command is shell-tokenized as written (no shell interpolation) and passed verbatim to `exec`, with one placeholder substituted at run time:
- `$PROMPT` → the prompt body for this run. If `$PROMPT` does not appear in `command`, the prompt is piped to the harness on stdin.

The model is **not** templated into `command`. Instead, the harness declares `model_flag`, and rally appends the model declaratively when one is resolved:
- `model_flag = "--model"` (or any non-empty string) → rally appends `[model_flag, resolved_model]` to the command when a model is resolved; appends nothing if no model is resolved.
- `model_flag = ""` (explicit empty string) → rally appends `[resolved_model]` (positional, no flag) when a model is resolved; appends nothing otherwise.
- `model_flag` omitted from config → rally never appends a model. The harness uses its own internal default. This is the natural path for "bare alias with no flat-field default."

`$MODEL` is **not a recognised placeholder** in `command`. If an operator includes `$MODEL` in `command`, the loader rejects with a clear error directing them to `model_flag` (likely a port from earlier draft documentation).

**Alternative considered (a)**: Keep the `$MODEL` placeholder with a heuristic drop rule (drop the element + preceding `-`-prefixed element when no model resolves).
**Alternative considered (b)**: A single command string with shell interpolation.
**Alternative considered (c)**: Multiple output strategies (block-and-report, last-n, json-extract) selectable for any harness, including built-ins.

**Why**: The heuristic drop rule was magical — fine for `["--model", "$MODEL"]` but surprising for less common shapes (e.g. dropping a `--` separator). The declarative `model_flag` is more predictable: rally knows exactly what to append and in what shape, and there is exactly one way to express each case. It also generalises cleanly to built-in harnesses' mental model — built-ins already use `--model` flags internally; the schema just makes that surface visible. Array-of-strings avoids shell-quoting hazards. `$PROMPT` stays as a placeholder because prompts have no analogous declarative shape — they vary per CLI in ways that need positional control. Restricting v0.5.0 to one output parser keeps the code path narrow; built-ins keep their bespoke parsers because each was tuned to a specific CLI's output format. Additional `output_strategy` values and richer schemas can land additively.

Output is captured via the `output_strategy` field. v0.5.0 ships one strategy: `"tail"`, which keeps the last `output_lines` lines (default 40) of the stream selected by `tail_stream`. `tail_stream` accepts `"stdout"`, `"stderr"`, or `"combined"` (default).

The generic executor lives next to the built-in executors in `internal/agent/` (rather than a new top-level package) to share the existing prompt-building, stream-capture, and run-bookkeeping helpers.

Built-in harnesses (`cc`/`cx`/`ge`/`op`) SHALL NOT declare `command`, `model_flag`, `output_strategy`, or `tail_stream`. Their behaviour remains hard-coded; the loader rejects any of those fields on a built-in entry.

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

**Why**: The original v0.5.0 draft included a "delete legacy loader" section. Investigation found nothing to delete — the file format was apparently never implemented, or was removed in an earlier release. The change reduces to a one-line verification noted in the proposal Impact section.

## Risks / Trade-offs

- **Two harness:model representations coexist (raw string and named model)** → Mitigation: the parser is the single resolution point; downstream code only sees the resolved `(harness, model)` record. Tests cover both forms producing identical resolved values.
- **`did-you-mean` suggestions are noisy if model names are numerous** → Mitigation: cap at 3 suggestions ranked by Levenshtein distance, scoped to the same harness; if none are within a small threshold, just list valid names.
- **User-defined harness `command` is a code-execution surface** → Mitigation: command is run with the same privileges as rally itself; the operator authored `.rally/config.toml`, so this is no broader than what they could do with a CLI flag. Templating is positional substitution, not shell interpolation, so `$PROMPT` containing shell metacharacters is safe.
- **`model_flag` is silent when omitted** → An operator who omits `model_flag` and is surprised that the harness doesn't see their named model has no obvious diagnostic. Mitigation: when a run resolves to a non-empty model AND the dispatched harness has `model_flag` unset, log a one-line note ("model `X` resolved but harness `Y` has no `model_flag` configured — passing model not supported, harness default will be used"). Documented in README.
- **`schema_version` warn-only is easy to ignore** → Mitigation: the warning prints a one-line "schema mismatch — please update or run `rally config check`" message; it's not a slap, but it's visible. v0.6.0+ tightens behaviour as the format stabilises.
- **Fallback file path could resolve to a missing file** → Mitigation: when the fallback path is needed and missing, rally falls back to the built-in default content. No hard error — a missing fallback is weak signal for a no-bead session, not a startup blocker. The "missing file" warning is emitted at first-use, not at config load (avoids noise for workspaces that never enter no-backend + no-bead).
- **Tail-only output strategy may be inadequate for some user harnesses** → Mitigation: `tail_stream` lets operators pick the right stream (some CLIs spam progress on stderr and emit the answer on stdout); `output_lines` is configurable. Richer parsers can land additively in a later release without breaking the schema.

## Migration Plan

1. **Schema additions**: extend `internal/config/config_v2.go` (or split as `v3.go` if the diff is sizeable) with `[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]` sections and `schema_version`. Existing fields untouched. New fields default to zero values when absent.
2. **Resolution layer**: add `ResolveAgent(spec string) (harness, model string, err error)` to the config layer. Mix parsing, route parsing (v0.6.0), and rotation parsing (v0.7.0) all funnel through this single resolver.
3. **AgentMix re-typing**: replace `AgentMix.Cycle []string` with a slice of resolved-agent records. Update `AgentForRun` and every caller. Land this in the same change as the resolver wiring.
4. **Mix parsing extension**: update the relay-runner's mix parser to call the new resolver. Existing `cc:2 cx:1` weighted form continues to work; `harness:model-name` entries resolve via `[harness.<harness>.models]`.
5. **User-harness executor**: add a generic harness executor in `internal/agent/` that runs `command` with `$PROMPT` substitution (or stdin), appends `[model_flag, model]` (or `[model]` if `model_flag = ""`) when a model is resolved, and applies the tail-N output parser using `tail_stream`. Wire it into the executor selection so a `harness.<name>` with a `command` field dispatches there instead of the built-in path.
6. **Fallback wiring**: extend the prompt-building path so that no-backend mode + no-ready-bead substitutes `[fallback].instructions_file` content (or built-in default) for the bead body.
7. **Defaults wiring**: read `[defaults].iterations` and `.mix` at relay startup; CLI flags continue to override.
8. **Schema version handshake**: emit `schema_version = 2` on every write; warn on read-time mismatch.

Rollback: revert v0.5.0. Existing `.rally/config.toml` files keep working since the new sections were additive. Workspaces that adopted named models or user-defined harnesses would need to expand them back to raw strings or lose access to those harnesses respectively — the release notes call this out.

## Open Questions

- Whether `[harness.<name>]` should support per-harness env-var injection (e.g. `OPENCODE_API_KEY` overrides). For v0.5.0, env handling stays exactly as it is today (set in the shell or via systemd). Revisit if multi-key workflows surface.
- Whether `[defaults].mix` should accept a "named mix" (e.g. `mix = "balanced"` looking up a named list elsewhere). Out of scope for v0.5.0; named mixes can land in v0.6.0 alongside roles or later.
- Whether v0.6.0 should re-introduce a `cc:opus:2` weight-on-named-model syntax. Reserved for that release; v0.5.0 errors on the third colon segment.
