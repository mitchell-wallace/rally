## 1. Schema extension

- [ ] 1.1 Extend `internal/config/config_v2.go` (or split as `v3.go` if the diff is large) with `[defaults]`, `[microbeads]`, `[fallback]`, `[providers]` sections and a top-level `schema_version` int
- [ ] 1.2 Preserve all existing v0.2.x flat fields at the file root (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`)
- [ ] 1.3 Define struct types for each new section; each field is optional with a sensible zero value
- [ ] 1.4 Unit tests: minimal v0.2.x file still loads; file with new sections loads; missing sections default cleanly

## 2. Provider shortcuts

- [ ] 2.1 Validate every `[providers]` entry at load: `harness` is in the supported list, `model` is non-empty, key is non-numeric and matches `^[A-Za-z][A-Za-z0-9:_-]*$`
- [ ] 2.2 Reject numeric-only keys with a clear error message naming the offending key
- [ ] 2.3 Add `ResolveAgent(spec string) (harness, model string, err error)` to the config layer; resolves shortcut keys, accepts raw `harness:model` strings, errors on unresolved keys with `did-you-mean` suggestions (Levenshtein-ranked, top 3)
- [ ] 2.4 Unit tests: shortcut resolution, raw form passthrough, did-you-mean suggestions on typo, numeric-only key rejection, unknown harness rejection

## 3. Mix parsing extension

- [ ] 3.1 Update the relay-runner's mix parser to call `ResolveAgent` for every comma-separated entry
- [ ] 3.2 Confirm the existing `cc:2 cx:1` weighted form still parses correctly (resolve each weighted item, then expand to the cycle)
- [ ] 3.3 Allow mix entries to combine raw and shortcut forms in the same string
- [ ] 3.4 Unit tests: each combination of raw, shortcut, and weighted; error cases for unresolved keys

## 4. Defaults loading

- [ ] 4.1 At relay startup, read `[defaults].iterations`, `.mix`, `.verbose` from config when the corresponding CLI flag is absent
- [ ] 4.2 Validate that `[defaults].mix` parses cleanly through the resolver at config load (so a typo errors at startup, not at run-time)
- [ ] 4.3 Unit tests: each default applied, each CLI flag overrides, malformed default errors at startup

## 5. Microbeads instructions content source

- [ ] 5.1 In microbeads-backed mode, source instruction content from `[microbeads].instructions_file` when configured and readable; fall back to the built-in default otherwise
- [ ] 5.2 Log a warning at config load if the configured path doesn't exist or isn't readable
- [ ] 5.3 No instructions toggle (per v0.4.0 alignment) — injection is unconditional in microbeads-backed mode
- [ ] 5.4 Unit tests: configured file used when present, built-in default used when absent or unreadable, warning emitted on missing path

## 6. Fallback prompt content source

- [ ] 6.1 Add fallback-injection logic to the prompt-building path: when in no-backend mode AND no ready bead exists, substitute `[fallback].instructions_file` content (or built-in default) for the bead body
- [ ] 6.2 In microbeads-backed mode, fallback file SHALL have no effect even if configured
- [ ] 6.3 Unit tests: no-backend + no-bead path uses fallback, microbeads-backed path ignores fallback, missing/unreadable file falls back to built-in default

## 7. Schema version handshake

- [ ] 7.1 Recognise top-level `schema_version` int; expected value `2`
- [ ] 7.2 Absent → treat as version 1, accept silently
- [ ] 7.3 Mismatch → log a one-line warning naming the expected version, proceed with load
- [ ] 7.4 Every config write SHALL emit `schema_version = 2` at the root
- [ ] 7.5 Unit tests: absent, matching, mismatched cases

## 8. Legacy loader removal

- [ ] 8.1 Delete the `.rally/config` env-style loader (and any tests referencing it)
- [ ] 8.2 Verify no remaining callers of the removed loader (grep + build check)
- [ ] 8.3 Confirm `RALLY_DATA_DIR` env variable still works via direct OS-environment read
- [ ] 8.4 Confirm `data_dir` field in `config.toml` still functions as in v0.2.x

## 9. Documentation

- [ ] 9.1 Update README's config section with the four new sections and example shortcut entries
- [ ] 9.2 v0.5.0 release notes: legacy `.rally/config` removed, `[providers]` shortcut introduced, defaults moved into config, no migration of progress YAML
- [ ] 9.3 Cross-link to v0.4.0 release notes for the `Beads` field removal — no rename, the field is gone

## 10. Verification

- [ ] 10.1 End-to-end: workspace with full new-config sections — relay reads iterations/mix from `[defaults]`, resolves shortcuts in mix, picks up fallback prompt in no-backend + no-bead case
- [ ] 10.2 Backwards-compat: workspace with only v0.2.x flat fields — loads cleanly with no warnings
- [ ] 10.3 Did-you-mean: typo in a `[providers]` shortcut reference produces an error with closest matches listed
- [ ] 10.4 Numeric-only shortcut key produces a clear error at config load
- [ ] 10.5 `grep -r "RALLY_DATA_DIR" --include="*.go"` confirms env-variable read path is preserved while legacy file loader is gone
