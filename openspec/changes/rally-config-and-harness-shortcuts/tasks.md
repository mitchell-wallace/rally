## 1. Schema extension

- [ ] 1.1 Extend `internal/config/config_v2.go` (or split as `v3.go` if the diff is large) with `[defaults]`, `[microbeads]`, `[fallback]`, `[harness.*]` sections and a top-level `schema_version` int
- [ ] 1.2 Preserve all existing v0.2.x flat fields at the file root (`claude_model`, `codex_model`, `gemini_model`, `opencode_model`, `data_dir`, `run_hooks_on_autocommit`)
- [ ] 1.3 Define struct types for each new section; each field is optional with a sensible zero value; `[harness.<name>]` is a map keyed by harness name with `models` (string→string), `command` ([]string, optional), `model_flag` (*string, optional — pointer to distinguish unset from empty-string-positional), `output_strategy` (string, optional), `output_lines` (int, optional), `tail_stream` (string, optional, one of `stdout`/`stderr`/`combined`, default `combined`)
- [ ] 1.4 Unit tests: minimal v0.2.x file still loads; file with new sections loads; missing sections default cleanly; `model_flag = ""` (empty string) and `model_flag` absent are distinguishable in the parsed struct

## 2. Harness/model resolution

- [ ] 2.1 Validate every `[harness.<name>]` entry at load: harness name matches `^[A-Za-z][A-Za-z0-9_-]*$`; if a built-in name (`cc`/`cx`/`ge`/`op`/`claude`/`codex`/`gemini`/`opencode`), reject `command`, `model_flag`, `output_strategy`, and `tail_stream` fields with a clear error
- [ ] 2.2 Validate every `[harness.<name>.models]` entry: model name matches `^[A-Za-z][A-Za-z0-9_-]*$` (non-numeric); model string is non-empty
- [ ] 2.3 Reject any `command` array element containing the literal `$MODEL` at config load with a clear error directing operators to `model_flag` instead
- [ ] 2.4 Add `ResolveAgent(spec string) (harness, model string, err error)` to the config layer; accepts bare aliases, `harness:weight` (digits — preserved as weight metadata), `harness:model-name`, and raw `harness:model-string`; errors on unresolved model names with `did-you-mean` suggestions (Levenshtein-ranked, top 3, scoped to the same harness)
- [ ] 2.5 Unit tests: model name resolution, bare-alias passthrough, weight passthrough, did-you-mean suggestions on typo, numeric-only model name rejected, unknown harness rejected, built-in harness rejecting `command`/`model_flag`/`output_strategy`/`tail_stream`, `$MODEL` in `command` rejected

## 3. User-defined harness executor (in `internal/agent/`)

- [ ] 3.1 Add a generic harness executor next to the built-in executors in `internal/agent/`; share existing prompt-building, stream-capture, and run-bookkeeping helpers
- [ ] 3.2 Implement `$PROMPT` substitution: each command element is replaced verbatim if it equals `$PROMPT`; partial matches (`prefix-$PROMPT`, `--prompt=$PROMPT`) are also substituted; substitution is positional, not shell
- [ ] 3.3 If `$PROMPT` does not appear anywhere in `command`, pipe the prompt on stdin
- [ ] 3.4 Implement `model_flag` model injection: when `model_flag` is set non-empty and a model is resolved, append `[model_flag, model]` to the command; when `model_flag` is set to empty string and a model is resolved, append `[model]`; when `model_flag` is unset, never append; when no model is resolved, never append regardless of `model_flag`
- [ ] 3.5 When `model_flag` is unset AND a non-empty model is resolved, log a one-line informational note that the model could not be passed
- [ ] 3.6 Implement `output_strategy = "tail"` parser: capture the stream selected by `tail_stream` (default `combined`), surface the last `output_lines` (default 40) as the run output; reject any other `output_strategy` value at config load
- [ ] 3.7 Wire the executor selection: a harness whose `[harness.<name>]` declares `command` dispatches through this generic path; built-ins continue to dispatch through their existing executors
- [ ] 3.8 Unit tests: `$PROMPT` substitution and stdin fallback; `model_flag` non-empty appends flag-and-value; `model_flag = ""` appends positional; `model_flag` unset omits model and logs note; no resolved model omits model regardless; tail parser on long output; tail parser on short output; `tail_stream = "stderr"` captures stderr only; built-in harness still uses built-in executor

## 4. AgentMix re-typing

This phase is a structural change: `AgentMix.Cycle` flips from `[]string` (harness aliases) to `[]ResolvedAgent` (typed `(harness, model)` records). Every callsite that reads or writes the cycle is updated. Land this whole phase in one commit so intermediate states don't compile-but-misbehave.

### 4.1 Define the type

- [ ] 4.1.1 In `internal/relay/mix.go`, define `type ResolvedAgent struct { Harness, Model string }`
- [ ] 4.1.2 Change `AgentMix.Cycle` from `[]string` to `[]ResolvedAgent`
- [ ] 4.1.3 Decide whether `AgentMix.Weights map[string]int` and `AgentMix.Order []string` stay keyed by harness alias or become richer. Recommendation: keep both keyed by harness alias for v0.5.0 — they describe weighting structure which is per-harness, not per-(harness,model). Document the choice in `mix.go`.
- [ ] 4.1.4 Update `AgentMix.Label` builder: existing format `"cc:1 cx:2"` is round-trippable through `ParseAgentMix`. The new label MUST also round-trip the typed cycle (e.g. `"cc cc op:z op:gk"` — repeat tokens for weight, named models inline). Decide and document the format.

### 4.2 Update the parser and selector (`internal/relay/mix.go`)

- [ ] 4.2.1 Rewrite `ParseAgentMix(specs []string, resolver Resolver) (AgentMix, error)` — adds a resolver parameter so it can convert `harness:model-name` and bare aliases to `ResolvedAgent` records; existing weighted form still works (digits-after-colon → weight)
- [ ] 4.2.2 Change `AgentForRun(runIndex int, mix AgentMix) ResolvedAgent` — return type flips from `string` to `ResolvedAgent`
- [ ] 4.2.3 If `AgentForRun` is no longer used externally (verify with grep), consider deleting it — `SelectActiveAgent` is the actual call site

### 4.3 Update the selector (`internal/relay/resilience.go`)

- [ ] 4.3.1 Change `SelectActiveAgent(mix AgentMix, runIndex int) (string, int, bool, error)` signature to return `ResolvedAgent` instead of `string` (4-tuple becomes `(ResolvedAgent, int, bool, error)`)
- [ ] 4.3.2 Inside `SelectActiveAgent` ([resilience.go:62-105](internal/relay/resilience.go#L62-L105)): the `uniqueAgents map[string]struct{}` and `r.getState(a)` calls work on a "freezable unit." Decide: does freezing apply per-harness (across all models) or per-(harness, model) tuple? Recommendation: per-harness for v0.5.0 (matches today's semantics; if claude is rate-limited, no model under it is going to succeed). Use `a.Harness` as the state key.
- [ ] 4.3.3 Update `r.getState(a)` calls in resilience.go (lines 75, 91) — pass `a.Harness`, not the whole record
- [ ] 4.3.4 Update `PauseAgent` / `UnpauseAgent` callers in `runner.go` (lines 223, 233) to pass `agent.Harness`

### 4.4 Update the runner (`internal/relay/runner.go`)

- [ ] 4.4.1 Line 74 `var mix AgentMix` — type unchanged but cycle contents flip
- [ ] 4.4.2 Lines 79, 89, 96 `ParseAgentMix(...)` calls — add the new `resolver` argument
- [ ] 4.4.3 Lines 83, 100, 116 use `mix.Label` — verify the new label format round-trips through `ParseAgentMix(strings.Fields(relay.AgentMix), resolver)` at line 89
- [ ] 4.4.4 Line 154 `agentType, ... := resilience.SelectActiveAgent(mix, runIndex)` — rename to `agent` (or `picked`), change type to `ResolvedAgent`
- [ ] 4.4.5 Lines 207, 223, 233 use `agentType` for pause/unpause and rotation logic — pass `agent.Harness`
- [ ] 4.4.6 Line 308 `timeUntilNextRetry(resilience *Resilience, mix AgentMix)` — loop variable `a` is now `ResolvedAgent`; pass `a.Harness` to `resilience.getState`
- [ ] 4.4.7 Line 642 `executeTry(ctx, agentType, opts)` — extend to take `ResolvedAgent` (or `harness, model` separately); plumb `model` into `agent.RunOptions`
- [ ] 4.4.8 Line 662 `autoCommit(runIndex, agentType, attempt)` — pass `agent.Harness`

### 4.5 Update the executor interface (`internal/agent/executor.go`)

- [ ] 4.5.1 Add `Model string` to `RunOptions` ([executor.go:5-19](internal/agent/executor.go#L5-L19)) so each run can carry its resolved model into the executor
- [ ] 4.5.2 Update built-in executors (claude, codex, gemini, opencode) to read `opts.Model` when set, falling back to their construction-time per-harness default when empty (preserves v0.2.x behaviour for bare aliases)
- [ ] 4.5.3 The generic user-harness executor reads `opts.Model` for the `model_flag` injection logic in 3.4

### 4.6 Update the persistent record (`internal/store/records.go`)

- [ ] 4.6.1 `Relay.AgentMix string` ([records.go:39](internal/store/records.go#L39)) stays a string, but the format must round-trip the typed cycle. Confirm `mix.Label` (as updated in 4.1.4) can be re-parsed by `ParseAgentMix` at runner.go:89 to reconstruct the same typed cycle. No schema migration of the JSONL store is required since the field stays `string`
- [ ] 4.6.2 Verify resume path: a relay started with named-model mix is correctly resumed after a restart — the stored label re-parses through the same resolver

### 4.7 Update tests

- [ ] 4.7.1 `internal/relay/runner_test.go` — ~17 callsites construct `AgentMixSpecs: []string{"cc:1"}`. Inputs stay strings; assertions on `mix.Cycle[i] == "claude"` flip to `mix.Cycle[i].Harness == "claude"` (and possibly `.Model` too)
- [ ] 4.7.2 `internal/relay/runner_test.go:957, 992` — direct `ParseAgentMix(...)` calls; pass a test resolver
- [ ] 4.7.3 `internal/relay/runner_test.go:962, 997` — `SelectActiveAgent` return-value assertions update from `agent string` to `agent ResolvedAgent`
- [ ] 4.7.4 Add new test cases: cycle with named models (`op:z`, `cc:opus`); cycle with mixed weighted/named/raw forms; resume from a stored label that includes named models
- [ ] 4.7.5 Add new test case: resilience pauses harness `claude` and a cycle with `cc:opus` and `cc:sonnet` correctly skips both (because pause is per-harness)

### 4.8 Final-check sweep

After 4.1–4.7 land, grep for residual references to the old shape. Each pattern below should return zero results in non-test, non-spec files (test files may legitimately reference the new typed shape):

- [ ] 4.8.1 `grep -rn 'AgentForRun' --include='*.go' .` — uses must reference the new `ResolvedAgent` return
- [ ] 4.8.2 `grep -rn 'mix\.Cycle\[' --include='*.go' .` — every access should treat the result as a `ResolvedAgent`, not a string
- [ ] 4.8.3 `grep -rn 'range mix\.Cycle' --include='*.go' .` — loop variables should be records, not strings
- [ ] 4.8.4 `grep -rn 'Cycle:\s*\[\]string' --include='*.go' .` — should return zero hits
- [ ] 4.8.5 `grep -rn 'Cycle:\s*cycle' --include='*.go' .` followed by checking `cycle` is `[]ResolvedAgent`
- [ ] 4.8.6 `grep -rn 'agentType\s*string' --include='*.go' .` — surviving uses are fine if they take just the harness name (e.g. resilience pause); flag any that should now take a full `ResolvedAgent`
- [ ] 4.8.7 Build the binary with `go build ./...` and run the full test suite — type errors surface any remaining callsite

## 5. Mix parsing extension

- [ ] 5.1 Update the relay-runner's mix parser to call `ResolveAgent` for every comma- or space-separated entry
- [ ] 5.2 Confirm the existing `cc:2 cx:1` weighted form still parses correctly (digits-after-colon → weight)
- [ ] 5.3 Allow mix entries to combine bare aliases, weighted aliases, and named-model entries in the same string
- [ ] 5.4 Reject `cc:opus:2` (third colon segment) with a clear error in v0.5.0; reserved for v0.6.0
- [ ] 5.5 Unit tests: each combination of bare/weighted/named; mixed forms in one mix; error cases for unresolved names and third colon segment

## 6. Defaults loading

- [ ] 6.1 At relay startup, read `[defaults].iterations` and `.mix` from config when the corresponding CLI flag is absent
- [ ] 6.2 Validate that `[defaults].mix` parses cleanly through the resolver at config load (so a typo errors at startup, not at run-time)
- [ ] 6.3 Unit tests: each default applied, each CLI flag overrides, malformed default errors at startup

## 7. Microbeads instructions content source

- [ ] 7.1 In microbeads-backed mode, source instruction content from `[microbeads].instructions_file` when configured and readable; fall back to the built-in default otherwise
- [ ] 7.2 Log a warning on first use (not at config load) if the configured path doesn't exist or isn't readable
- [ ] 7.3 No instructions toggle (per v0.4.0 alignment) — injection is unconditional in microbeads-backed mode
- [ ] 7.4 Unit tests: configured file used when present, built-in default used when absent or unreadable, warning emitted on first use with missing path

## 8. Fallback prompt content source

- [ ] 8.1 Add fallback-injection logic to the prompt-building path: when in no-backend mode AND no ready bead exists, substitute `[fallback].instructions_file` content (or built-in default) for the bead body
- [ ] 8.2 In microbeads-backed mode, fallback file SHALL have no effect even if configured
- [ ] 8.3 Unit tests: no-backend + no-bead path uses fallback, microbeads-backed path ignores fallback, missing/unreadable file falls back to built-in default

## 9. Schema version handshake

- [ ] 9.1 Recognise top-level `schema_version` int; expected value `2`
- [ ] 9.2 Absent → treat as version 1, accept silently
- [ ] 9.3 Mismatch → log a one-line warning naming the expected version, proceed with load
- [ ] 9.4 Every config write SHALL emit `schema_version = 2` at the root
- [ ] 9.5 Unit tests: absent, matching, mismatched cases

## 10. Documentation

- [ ] 10.1 Update README's config section with the new sections and example `[harness.<name>.models]` entries
- [ ] 10.2 Add a README example for a user-defined harness (`droid`) showing `command` + `model_flag`, `tail_stream`, the three `model_flag` modes (set / empty / unset), and a callout that substitution is **positional, not shell** (no shell interpolation; metacharacters in `$PROMPT` are safe)
- [ ] 10.3 v0.5.0 release notes: harnesses+models structure, `[defaults]`/`[microbeads]`/`[fallback]` sections, user-defined harnesses with templated commands and `tail_stream`, `AgentMix.Cycle` re-typed (callout for any external code that imports the package), no migration of progress YAML
- [ ] 10.4 Cross-link to v0.4.0 release notes for the `Beads` field removal — no rename, the field is gone

## 11. Verification

- [ ] 11.1 End-to-end: workspace with full new-config sections — relay reads iterations/mix from `[defaults]`, resolves named models in mix, picks up fallback prompt in no-backend + no-bead case
- [ ] 11.2 End-to-end: workspace with a user-defined `droid` harness (`model_flag = "--model"`) — relay invokes `command` with model appended and `$PROMPT` piped; tail parser surfaces last 40 lines from the configured stream
- [ ] 11.3 End-to-end: same workspace with a bare `droid` alias (no model resolved) — model not appended, harness uses its own default
- [ ] 11.4 End-to-end: harness with `model_flag = ""` (positional) — model appended without a flag
- [ ] 11.5 End-to-end: harness with `model_flag` unset and a model resolved — model not appended, log shows the informational note
- [ ] 11.6 Backwards-compat: workspace with only v0.2.x flat fields — loads cleanly with no warnings; bare alias `cc` in mix uses `claude_model` flat field as the model
- [ ] 11.7 Resume: a relay started with `--mix "claude,op:z"` is killed and resumed; the stored label re-parses through the resolver and produces an identical typed cycle
- [ ] 11.8 Did-you-mean: typo in a `harness:model-name` reference produces an error with closest matches scoped to that harness
- [ ] 11.9 Numeric-only model name produces a clear error at config load
- [ ] 11.10 Built-in harness with `command` / `model_flag` / `output_strategy` / `tail_stream` field produces a clear error at config load
- [ ] 11.11 `$MODEL` literal in any `command` array produces a clear error at config load
