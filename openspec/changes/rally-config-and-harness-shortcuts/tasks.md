## 1. Schema extension

- [x] 1.1 Extend `internal/config/config_v2.go` (or split as `v3.go` if the diff is large) with `[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]` sections and a top-level `schema_version` int
- [x] 1.2 Move per-harness default model fields under `[defaults]`: add `claude_model`, `codex_model`, `gemini_model`, `opencode_model` to the `[defaults]` struct alongside `iterations` (int) and `mix` (string)
- [x] 1.3 Keep root-level workspace runtime fields untouched: `data_dir`, `run_hooks_on_autocommit`, `laps_instructions`, plus the new `schema_version`
- [x] 1.4 Backwards-compat: continue to load root-level `claude_model` / `codex_model` / `gemini_model` / `opencode_model` if present (v0.2.x location); `[defaults]` takes precedence on conflict; emit a one-line deprecation note when a model value resolves from a root-level field; on every config write, emit only the new shape (no round-trip of root-level model fields)
- [x] 1.5 Define struct types for each new section; each field is optional with a sensible zero value; `[harness.<name>]` is a map keyed by harness name with `models` (string→string), `command` ([]string, optional), `model_flag` (*string, optional — pointer to distinguish unset from empty-string-positional), `output_strategy` (string, optional), `output_lines` (int, optional), `tail_stream` (string, optional, one of `stdout`/`stderr`/`combined`, default `combined`)
- [x] 1.6 Unit tests: minimal v0.2.x file (root-level model fields only) still loads with deprecation note; file with `[defaults]` model fields loads cleanly; conflict between root and `[defaults]` resolves to `[defaults]` with deprecation note; `model_flag = ""` (empty string) and `model_flag` absent are distinguishable in the parsed struct; written config has model fields under `[defaults]` only

## 2. Harness/model resolution

- [x] 2.1 Validate every `[harness.<name>]` entry at load: harness name matches `^[A-Za-z][A-Za-z0-9_-]*$`; if a built-in name (`cc`/`cx`/`ge`/`op`/`claude`/`codex`/`gemini`/`opencode`), reject `command`, `model_flag`, `output_strategy`, and `tail_stream` fields with a clear error
- [x] 2.2 Validate every `[harness.<name>.models]` entry: model name matches `^[A-Za-z][A-Za-z0-9_-]*$` (non-numeric); model string is non-empty
- [x] 2.3 Reject any `command` array element containing the literal `$MODEL` at config load with a clear error directing operators to `model_flag` instead
- [x] 2.4 Add `ResolveAgent(spec string) (harness, model string, err error)` to the config layer; accepts bare aliases, `harness:weight` (digits — preserved as weight metadata), `harness:model-name`, and raw `harness:model-string`; errors on unresolved model names with `did-you-mean` suggestions (Levenshtein-ranked, top 3, scoped to the same harness)
- [x] 2.5 Unit tests: model name resolution, bare-alias passthrough, weight passthrough, did-you-mean suggestions on typo, numeric-only model name rejected, unknown harness rejected, built-in harness rejecting `command`/`model_flag`/`output_strategy`/`tail_stream`, `$MODEL` in `command` rejected

## 3. User-defined harness executor

- [x] 3.1 Add a generic harness executor next to the built-in executors in `internal/agent/`; share existing prompt-building, stream-capture, and run-bookkeeping helpers
- [x] 3.2 Implement `$PROMPT` substitution: each command element is replaced verbatim if it equals `$PROMPT`; partial matches (`prefix-$PROMPT`, `--prompt=$PROMPT`) are also substituted; substitution is positional, not shell
- [x] 3.3 If `$PROMPT` does not appear anywhere in `command`, pipe the prompt on stdin
- [x] 3.4 Implement `model_flag` model injection: when `model_flag` is set non-empty and a model is resolved, append `[model_flag, model]` to the command; when `model_flag` is set to empty string and a model is resolved, append `[model]`; when `model_flag` is unset, never append; when no model is resolved, never append regardless of `model_flag`
- [x] 3.5 When `model_flag` is unset AND a non-empty model is resolved, log a one-line informational note that the model could not be passed
- [x] 3.6 Implement `output_strategy = "tail"` parser: capture the stream selected by `tail_stream` (default `combined`), surface the last `output_lines` (default 40) as the run output; reject any other `output_strategy` value at config load
- [x] 3.7 Wire the executor selection: a harness whose `[harness.<name>]` declares `command` dispatches through this generic path; built-ins continue to dispatch through their existing executors
- [x] 3.8 Unit tests: `$PROMPT` substitution and stdin fallback; `model_flag` non-empty appends flag-and-value; `model_flag = ""` appends positional; `model_flag` unset omits model and logs note; no resolved model omits model regardless; tail parser on long output; tail parser on short output; `tail_stream = "stderr"` captures stderr only; built-in harness still uses built-in executor

## 4. AgentMix type and parser

`AgentMix.Cycle` flips from `[]string` (harness aliases) to `[]ResolvedAgent` (typed `(harness, model)` records). Every callsite that reads or writes the cycle is updated. Land phases 4–9 in one commit so intermediate states don't compile-but-misbehave.

- [x] 4.1 In `internal/relay/mix.go`, define `type ResolvedAgent struct { Harness, Model string }`
- [x] 4.2 Change `AgentMix.Cycle` from `[]string` to `[]ResolvedAgent`
- [x] 4.3 Decide whether `AgentMix.Weights map[string]int` and `AgentMix.Order []string` stay keyed by harness alias or become richer. Recommendation: keep both keyed by harness alias for v0.5.0 — they describe weighting structure which is per-harness, not per-(harness,model). Document the choice in `mix.go`.
- [x] 4.4 Update `AgentMix.Label` builder: existing format `"cc:1 cx:2"` is round-trippable through `ParseAgentMix`. The new label MUST also round-trip the typed cycle (e.g. `"cc cc op:z op:gk"` — repeat tokens for weight, named models inline). Decide and document the format.
- [x] 4.5 Rewrite `ParseAgentMix(specs []string, resolver Resolver) (AgentMix, error)` — adds a resolver parameter so it can convert `harness:model-name` and bare aliases to `ResolvedAgent` records; existing weighted form still works (digits-after-colon → weight)
- [x] 4.6 Change `AgentForRun(runIndex int, mix AgentMix) ResolvedAgent` — return type flips from `string` to `ResolvedAgent`
- [x] 4.7 If `AgentForRun` is no longer used externally (verify with grep), consider deleting it — `SelectActiveAgent` is the actual call site

## 5. AgentMix selector and runner setup

- [x] 5.1 Change `SelectActiveAgent(mix AgentMix, runIndex int) (string, int, bool, error)` signature to return `ResolvedAgent` instead of `string` (4-tuple becomes `(ResolvedAgent, int, bool, error)`)
- [x] 5.2 Inside `SelectActiveAgent` ([resilience.go:62-105](internal/relay/resilience.go#L62-L105)): the `uniqueAgents map[string]struct{}` and `r.getState(a)` calls work on a "freezable unit." Decide: does freezing apply per-harness (across all models) or per-(harness, model) tuple? Recommendation: per-harness for v0.5.0 (matches today's semantics; if claude is rate-limited, no model under it is going to succeed). Use `a.Harness` as the state key.
- [x] 5.3 Update `r.getState(a)` calls in resilience.go (lines 75, 91) — pass `a.Harness`, not the whole record
- [x] 5.4 Update `PauseAgent` / `UnpauseAgent` callers in `runner.go` (lines 223, 233) to pass `agent.Harness`
- [x] 5.5 Line 74 `var mix AgentMix` — type unchanged but cycle contents flip
- [x] 5.6 Lines 79, 89, 96 `ParseAgentMix(...)` calls — add the new `resolver` argument
- [x] 5.7 Lines 83, 100, 116 use `mix.Label` — verify the new label format round-trips through `ParseAgentMix(strings.Fields(relay.AgentMix), resolver)` at line 89

## 6. AgentMix runner completion

- [x] 6.1 Line 154 `agentType, ... := resilience.SelectActiveAgent(mix, runIndex)` — rename to `agent` (or `picked`), change type to `ResolvedAgent`
- [x] 6.2 Lines 207, 223, 233 use `agentType` for pause/unpause and rotation logic — pass `agent.Harness`
- [x] 6.3 Line 308 `timeUntilNextRetry(resilience *Resilience, mix AgentMix)` — loop variable `a` is now `ResolvedAgent`; pass `a.Harness` to `resilience.getState`
- [x] 6.4 Line 642 `executeTry(ctx, agentType, opts)` — extend to take `ResolvedAgent` (or `harness, model` separately); plumb `model` into `agent.RunOptions`
- [x] 6.5 Line 662 `autoCommit(runIndex, agentType, attempt)` — pass `agent.Harness`

## 7. AgentMix executor interface and store

- [x] 7.1 Add `Model string` to `RunOptions` ([executor.go:5-19](internal/agent/executor.go#L5-L19)) so each run can carry its resolved model into the executor
- [x] 7.2 Update built-in executors (claude, codex, gemini, opencode) to read `opts.Model` when set, falling back to their construction-time per-harness default when empty (preserves v0.2.x behaviour for bare aliases)
- [x] 7.3 The generic user-harness executor reads `opts.Model` for the `model_flag` injection logic in 3.4
- [x] 7.4 `Relay.AgentMix string` ([records.go:39](internal/store/records.go#L39)) stays a string, but the format must round-trip the typed cycle. Confirm `mix.Label` (as updated in 4.4) can be re-parsed by `ParseAgentMix` at runner.go:89 to reconstruct the same typed cycle. No schema migration of the JSONL store is required since the field stays `string`
- [x] 7.5 Verify resume path: a relay started with named-model mix is correctly resumed after a restart — the stored label re-parses through the same resolver

## 8. AgentMix tests

- [x] 8.1 `internal/relay/runner_test.go` — ~17 callsites construct `AgentMixSpecs: []string{"cc:1"}`. Inputs stay strings; assertions on `mix.Cycle[i] == "claude"` flip to `mix.Cycle[i].Harness == "claude"` (and possibly `.Model` too)
- [x] 8.2 `internal/relay/runner_test.go:957, 992` — direct `ParseAgentMix(...)` calls; pass a test resolver
- [x] 8.3 `internal/relay/runner_test.go:962, 997` — `SelectActiveAgent` return-value assertions update from `agent string` to `agent ResolvedAgent`
- [x] 8.4 Add new test cases: cycle with named models (`op:z`, `cc:opus`); cycle with mixed weighted/named/raw forms; resume from a stored label that includes named models
- [x] 8.5 Add new test case: resilience pauses harness `claude` and a cycle with `cc:opus` and `cc:sonnet` correctly skips both (because pause is per-harness)

## 9. AgentMix final-check sweep

After phases 4–8 land, grep for residual references to the old shape. Each pattern below should return zero results in non-test, non-spec files (test files may legitimately reference the new typed shape):

- [x] 9.1 `grep -rn 'AgentForRun' --include='*.go' .` — uses must reference the new `ResolvedAgent` return
- [x] 9.2 `grep -rn 'mix\.Cycle\[' --include='*.go' .` — every access should treat the result as a `ResolvedAgent`, not a string
- [x] 9.3 `grep -rn 'range mix\.Cycle' --include='*.go' .` — loop variables should be records, not strings
- [x] 9.4 `grep -rn 'Cycle:\s*\[\]string' --include='*.go' .` — should return zero hits
- [x] 9.5 `grep -rn 'Cycle:\s*cycle' --include='*.go' .` followed by checking `cycle` is `[]ResolvedAgent`
- [x] 9.6 `grep -rn 'agentType\s*string' --include='*.go' .` — surviving uses are fine if they take just the harness name (e.g. resilience pause); flag any that should now take a full `ResolvedAgent`
- [x] 9.7 Build the binary with `go build ./...` and run the full test suite — type errors surface any remaining callsite

## 10. Mix parsing extension

- [x] 10.1 Update the relay-runner's mix parser to call `ResolveAgent` for every comma- or space-separated entry
- [x] 10.2 Confirm the existing `cc:2 cx:1` weighted form still parses correctly (digits-after-colon → weight)
- [x] 10.3 Allow mix entries to combine bare aliases, weighted aliases, and named-model entries in the same string
- [x] 10.4 Reject `cc:opus:2` (third colon segment) with a clear error in v0.5.0; reserved for v0.6.0
- [x] 10.5 Unit tests: each combination of bare/weighted/named; mixed forms in one mix; error cases for unresolved names and third colon segment

## 11. Defaults loading and init config

- [x] 11.1 At relay startup, read `[defaults].iterations` and `.mix` from config when the corresponding CLI flag is absent
- [x] 11.2 Bare-alias resolution for built-in harnesses uses `[defaults].<harness>_model` first; falls back to root-level `<harness>_model` (with the deprecation note from 1.4); falls back to the harness's hard-coded internal default if neither is set
- [x] 11.3 Validate that `[defaults].mix` parses cleanly through the resolver at config load (so a typo errors at startup, not at run-time)
- [x] 11.4 Unit tests: each default applied; CLI flag overrides; malformed default errors at startup; bare-alias resolution prefers `[defaults]` over root-level; bare-alias resolution falls through cleanly when nothing is set
- [x] 11.5 Update `runInit` ([cmd/rally/main.go:236](cmd/rally/main.go#L236)) so the example `.rally/config.toml` it writes uses the new shape: `schema_version = 2`, a populated `[defaults]` section with `iterations` and the four `<harness>_model` keys, and root-level runtime fields (`data_dir`, `run_hooks_on_autocommit`, `laps_instructions`)
- [x] 11.6 Existing init tests at [cmd/rally/main_test.go:25,58](cmd/rally/main_test.go#L25-L58) reference the v0.2.x flat shape — update assertions to expect the new `[defaults]` shape
- [x] 11.7 Confirm the existing "do not overwrite an existing config" behaviour is preserved (the new template only writes when no config exists)
- [x] 11.8 Unit test: `rally init` in a fresh workspace writes a config with `[defaults]` populated and `schema_version = 2`

## 12. Content sources

- [x] 12.1 In microbeads-backed mode, source instruction content from `[microbeads].instructions_file` when configured and readable; fall back to the built-in default otherwise
- [x] 12.2 Log a warning on first use (not at config load) if the configured path doesn't exist or isn't readable
- [x] 12.3 No instructions toggle (per v0.4.0 alignment) — injection is unconditional in microbeads-backed mode
- [x] 12.4 Unit tests: configured file used when present, built-in default used when absent or unreadable, warning emitted on first use with missing path
- [x] 12.5 Add fallback-injection logic to the prompt-building path: when in no-backend mode AND no ready bead exists, substitute `[fallback].instructions_file` content (or built-in default) for the bead body
- [x] 12.6 In microbeads-backed mode, fallback file SHALL have no effect even if configured
- [x] 12.7 Unit tests: no-backend + no-bead path uses fallback, microbeads-backed path ignores fallback, missing/unreadable file falls back to built-in default

## 13. Schema version handshake

- [x] 13.1 Recognise top-level `schema_version` int; expected value `2`
- [x] 13.2 Absent → treat as version 1, accept silently
- [x] 13.3 Mismatch → log a one-line warning naming the expected version, proceed with load
- [x] 13.4 Every config write SHALL emit `schema_version = 2` at the root
- [x] 13.5 Unit tests: absent, matching, mismatched cases

## 14. Documentation

- [x] 14.1 Update README's config section with the new sections and example `[harness.<name>.models]` entries
- [x] 14.2 Add a README example for a user-defined harness (`droid`) showing `command` + `model_flag`, `tail_stream`, the three `model_flag` modes (set / empty / unset), and a callout that substitution is **positional, not shell** (no shell interpolation; metacharacters in `$PROMPT` are safe)
- [x] 14.3 v0.5.0 release notes: harnesses+models structure, `[defaults]`/`[microbeads]`/`[fallback]` sections, user-defined harnesses with templated commands and `tail_stream`, `AgentMix.Cycle` re-typed (callout for any external code that imports the package), no migration of progress YAML
- [x] 14.4 Cross-link to v0.4.0 release notes for the `Beads` field removal — no rename, the field is gone

## 15. Verification: execution paths

- [x] 15.1 End-to-end: workspace with full new-config sections — relay reads iterations/mix from `[defaults]`, resolves named models in mix, picks up fallback prompt in no-backend + no-bead case
- [x] 15.2 End-to-end: workspace with a user-defined `droid` harness (`model_flag = "--model"`) — relay invokes `command` with model appended and `$PROMPT` piped; tail parser surfaces last 40 lines from the configured stream
- [x] 15.3 End-to-end: same workspace with a bare `droid` alias (no model resolved) — model not appended, harness uses its own default
- [x] 15.4 End-to-end: harness with `model_flag = ""` (positional) — model appended without a flag
- [x] 15.5 End-to-end: harness with `model_flag` unset and a model resolved — model not appended, log shows the informational note
- [x] 15.6 Backwards-compat: workspace with v0.2.x root-level `claude_model` (no `[defaults]`) — loads, bare alias `cc` resolves through the root-level field, deprecation note logged once
- [x] 15.7 New shape: workspace with `[defaults].claude_model` only — loads with no deprecation note, bare alias `cc` resolves through `[defaults]`

## 16. Verification: edge cases and errors

- [ ] 16.1 Conflict: workspace with both root-level `claude_model = "X"` and `[defaults].claude_model = "Y"` — `Y` wins, deprecation note logged
- [ ] 16.2 `rally init` writes a fresh config with `[defaults]` populated and `schema_version = 2`
- [ ] 16.3 Resume: a relay started with `--mix "claude,op:z"` is killed and resumed; the stored label re-parses through the resolver and produces an identical typed cycle
- [ ] 16.4 Did-you-mean: typo in a `harness:model-name` reference produces an error with closest matches scoped to that harness
- [ ] 16.5 Numeric-only model name produces a clear error at config load
- [ ] 16.6 Built-in harness with `command` / `model_flag` / `output_strategy` / `tail_stream` field produces a clear error at config load
- [ ] 16.7 `$MODEL` literal in any `command` array produces a clear error at config load
