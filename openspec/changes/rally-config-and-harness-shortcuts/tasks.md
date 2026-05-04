## 1. Schema extension

- [ ] 1.1 Extend `internal/config/config_v2.go` (or split as `v3.go` if the diff is large) with `[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]` sections and a top-level `schema_version` int
- [ ] 1.2 Preserve all existing v0.2.x flat fields at the file root (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`)
- [ ] 1.3 Define struct types for each new section; each field is optional with a sensible zero value; `[harness.<name>]` is a map keyed by harness name with `models` (string→string), `command` ([]string, optional), `output_strategy` (string, optional), `output_lines` (int, optional)
- [ ] 1.4 Unit tests: minimal v0.2.x file still loads; file with new sections loads; missing sections default cleanly

## 2. Harness/model resolution

- [ ] 2.1 Validate every `[harness.<name>]` entry at load: harness name matches `^[A-Za-z][A-Za-z0-9_-]*$`; if a built-in name (`cc`/`cx`/`ge`/`op`/`claude`/`codex`/`gemini`/`opencode`), reject `command` and `output_strategy` fields with a clear error
- [ ] 2.2 Validate every `[harness.<name>.models]` entry: model name matches `^[A-Za-z][A-Za-z0-9_-]*$` (non-numeric); model string is non-empty
- [ ] 2.3 Add `ResolveAgent(spec string) (harness, model string, err error)` to the config layer; accepts bare aliases, `harness:weight` (digits — preserved as weight metadata), `harness:model-name`, and raw `harness:model-string`; errors on unresolved model names with `did-you-mean` suggestions (Levenshtein-ranked, top 3, scoped to the same harness)
- [ ] 2.4 Unit tests: model name resolution, bare-alias passthrough, weight passthrough, did-you-mean suggestions on typo, numeric-only model name rejected, unknown harness rejected, built-in harness rejecting `command` field

## 3. User-defined harness executor

- [ ] 3.1 Add a generic harness executor that runs `command` with `$MODEL` and `$PROMPT` substitution; if `$PROMPT` does not appear in `command`, pipe the prompt on stdin
- [ ] 3.2 Substitution is positional (no shell interpolation): each command element is replaced verbatim if it equals `$MODEL` or `$PROMPT`; partial matches (`prefix-$MODEL`) are also substituted
- [ ] 3.3 Implement `output_strategy = "tail"` parser: capture combined stdout+stderr, surface the last `output_lines` (default 40) as the run output; reject any other `output_strategy` value at config load
- [ ] 3.4 Wire the executor selection: a harness whose `[harness.<name>]` declares `command` dispatches through this generic path; built-ins continue to dispatch through their existing executors
- [ ] 3.5 Unit tests: `$MODEL`/`$PROMPT` substitution; stdin fallback when `$PROMPT` absent; tail parser on long output; tail parser on short output; built-in harness still uses built-in executor

## 4. Mix parsing extension

- [ ] 4.1 Update the relay-runner's mix parser to call `ResolveAgent` for every comma- or space-separated entry
- [ ] 4.2 Confirm the existing `cc:2 cx:1` weighted form still parses correctly (digits-after-colon → weight)
- [ ] 4.3 Allow mix entries to combine bare aliases, weighted aliases, and named-model entries in the same string
- [ ] 4.4 Reject `cc:opus:2` (third colon segment) with a clear error in v0.5.0; reserved for v0.6.0
- [ ] 4.5 Update `AgentMix` to carry resolved `(harness, model)` tuples through the cycle (today the cycle is `[]string` of harness aliases — extend or replace as needed)
- [ ] 4.6 Unit tests: each combination of bare/weighted/named; mixed forms in one mix; error cases for unresolved names, third colon segment

## 5. Defaults loading

- [ ] 5.1 At relay startup, read `[defaults].iterations` and `.mix` from config when the corresponding CLI flag is absent
- [ ] 5.2 Validate that `[defaults].mix` parses cleanly through the resolver at config load (so a typo errors at startup, not at run-time)
- [ ] 5.3 Unit tests: each default applied, each CLI flag overrides, malformed default errors at startup

## 6. Microbeads instructions content source

- [ ] 6.1 In microbeads-backed mode, source instruction content from `[microbeads].instructions_file` when configured and readable; fall back to the built-in default otherwise
- [ ] 6.2 Log a warning on first use (not at config load) if the configured path doesn't exist or isn't readable
- [ ] 6.3 No instructions toggle (per v0.4.0 alignment) — injection is unconditional in microbeads-backed mode
- [ ] 6.4 Unit tests: configured file used when present, built-in default used when absent or unreadable, warning emitted on first use with missing path

## 7. Fallback prompt content source

- [ ] 7.1 Add fallback-injection logic to the prompt-building path: when in no-backend mode AND no ready bead exists, substitute `[fallback].instructions_file` content (or built-in default) for the bead body
- [ ] 7.2 In microbeads-backed mode, fallback file SHALL have no effect even if configured
- [ ] 7.3 Unit tests: no-backend + no-bead path uses fallback, microbeads-backed path ignores fallback, missing/unreadable file falls back to built-in default

## 8. Schema version handshake

- [ ] 8.1 Recognise top-level `schema_version` int; expected value `2`
- [ ] 8.2 Absent → treat as version 1, accept silently
- [ ] 8.3 Mismatch → log a one-line warning naming the expected version, proceed with load
- [ ] 8.4 Every config write SHALL emit `schema_version = 2` at the root
- [ ] 8.5 Unit tests: absent, matching, mismatched cases

## 9. Legacy loader verification

- [ ] 9.1 `grep -r '\.rally/config"' /path/to/rally --include="*.go"` confirms no env-style loader exists; document the result in the change notes (no work to do; verification only)

## 10. Documentation

- [ ] 10.1 Update README's config section with the new sections and example `[harness.<name>.models]` entries
- [ ] 10.2 Add a README example for a user-defined harness (`droid`) showing `command` templating and the tail parser
- [ ] 10.3 v0.5.0 release notes: harnesses+models structure, `[defaults]`/`[microbeads]`/`[fallback]` sections, user-defined harnesses with templated commands, no migration of progress YAML
- [ ] 10.4 Cross-link to v0.4.0 release notes for the `Beads` field removal — no rename, the field is gone

## 11. Verification

- [ ] 11.1 End-to-end: workspace with full new-config sections — relay reads iterations/mix from `[defaults]`, resolves named models in mix, picks up fallback prompt in no-backend + no-bead case
- [ ] 11.2 End-to-end: workspace with a user-defined `droid` harness — relay invokes `command` with `$MODEL`/`$PROMPT` substitution, tail parser surfaces last 40 lines
- [ ] 11.3 Backwards-compat: workspace with only v0.2.x flat fields — loads cleanly with no warnings; bare alias `cc` in mix uses `claude_model` flat field as the model
- [ ] 11.4 Did-you-mean: typo in a `harness:model-name` reference produces an error with closest matches scoped to that harness
- [ ] 11.5 Numeric-only model name produces a clear error at config load
- [ ] 11.6 Built-in harness with `command` field produces a clear error at config load
