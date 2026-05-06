## 1. Schema extension

- [x] 1.1 Extend the v0.5.0 config schema with a top-level `[routes]` table; each entry is a string array
- [x] 1.2 Validate at config load that route keys are strings of any case, that no two keys differ only in case, and that `default` is treated as a reserved key
- [x] 1.3 Validate role names referenced inside `[routes]` entries are NOT permitted (role refs are `--agent`-only)
- [x] 1.4 Unit tests: empty routes section, only-default, only-non-default, duplicate-by-case rejected, role-name-as-entry rejected

## 2. Quota-syntax parser

- [x] 2.1 In `internal/routing/parse.go`, implement positional `:`-split with last-segment-as-quota detection (`^\d+$` or `^\d+-\d+$`)
- [x] 2.2 Resolve the agent identifier portion (1 segment → shortcut, 2 segments → harness:model, 3+ → error)
- [x] 2.3 Validate quota bounds (`min <= max`, `min > 0`); error on out-of-bounds with the offending entry named
- [x] 2.4 Confirm models with embedded digits (`gpt-4`, `claude-4.5-sonnet`) parse correctly because they contain non-digits
- [x] 2.5 Unit tests: every entry shape (1/2/3-segment), every quota form (none/single/range/invalid), shortcut + quota combinations

## 3. Scheduler — core

- [x] 3.1 Add `internal/routing/scheduler.go` with state per route entry: position, consecutive-runs counter, exhausted/frozen flag, range-quota progress
- [x] 3.2 Implement `Next() Entry` per the quota rules: no-quota = until-failure, `:N` = rotate after N, `:N-M` = prefer N, allow up to M when others exhausted
- [x] 3.3 Implement `OnAgentFailed(entry, reason)` and `OnAgentRecovered(entry)` to mark/clear exhausted/frozen flags
- [x] 3.4 Implement cycle wrap: when end of list is reached, advance to head; clear exhausted flags (v0.6.0 behaviour; v0.7.0 may refine)
- [x] 3.5 Implement force-wait when all entries exhausted and no entry has remaining range-quota
- [x] 3.6 Unit tests: each canonical scenario (1–7) from the proposal, plus edge cases (single-entry route, all-quota route, all-no-quota route)

## 4. Routing layer

- [x] 4.1 Add `internal/routing/select.go` with `ActiveRoute(bead, override) Route` selecting per priority: `--agent` override > bead assignee match > default
- [x] 4.2 Case-insensitive matching of `assignee` against `[routes]` keys
- [x] 4.3 Per-iteration warning when a non-default role has no match and falls back to default
- [x] 4.4 Per-iteration error exit when no role match and no `default` exists
- [x] 4.5 In no-backend mode, route is always `default`
- [x] 4.6 Unit tests: each priority level, fallback to default with warning, exit on no-match-no-default, no-backend collapse

## 5. Role-instruction loader

- [x] 5.1 Add `internal/prompt/roleloader/loader.go` with case-insensitive directory scan of `.rally/agents/` (sorted, deterministic)
- [x] 5.2 Return file content as opaque string (no parsing, no front-matter)
- [x] 5.3 Return empty string on missing file (silent, not an error)
- [x] 5.4 Wire into prompt-building path: insert content between base instructions and bead body
- [x] 5.5 Skip entirely in no-backend mode
- [x] 5.6 Unit tests: exact match, case-variant match, multiple variants on disk → deterministic pick, missing file silent, no-backend skip

## 6. `--agent` flag and override roster

- [x] 6.1 Add `--agent` flag to `rally relay`; parse value as space-separated entries via the quota-syntax parser
- [x] 6.2 Allow role-name references as entries; inline the named route's entries; trailing quota advances role's internal cursor by N per visit
- [x] 6.3 Reject role-name references when `--agent` is unsupplied (i.e. inside `[routes]`)
- [x] 6.4 When both `--agent` and legacy `--mix` are present, `--agent` wins with a warning
- [x] 6.5 Unit tests: each combination of harness:model / shortcut / role-ref, with and without quotas; the canonical scenarios 5 and 6

## 7. `rally routes check` validator

- [x] 7.1 Add `internal/cli/routes_check.go` cobra subcommand
- [x] 7.2 Parse routes, resolve shortcuts, validate quotas, list unreachable routes
- [x] 7.3 Did-you-mean suggestions on unresolved shortcut keys (Levenshtein top 3)
- [x] 7.4 Exit zero on clean config, non-zero on parse/resolution/quota errors; warnings (unreachable routes, missing default) do not by themselves cause non-zero exit
- [x] 7.5 Unit tests: clean config, each error category, unreachable-route info output

## 8. Startup-time validation

- [x] 8.1 Run the same validator at `rally relay` startup before any iteration begins
- [x] 8.2 Hard errors (quota out of bounds, duplicate-by-case, role-ref in routes) → exit non-zero
- [x] 8.3 Partial-failure cases (some routes valid, some broken) → warn and prompt y/N; on `n` or stdin EOF exit non-zero
- [x] 8.4 Missing default + non-default routes + empty bead queue → warn-and-exit (no prompt, no relay started)
- [x] 8.5 Unit tests: each gate, prompt behaviour with stdin EOF, empty-queue early exit

## 9. Relay-runner integration

- [x] 9.1 Replace v0.5.0 round-robin with the routing+scheduler path: select route per iteration, ask scheduler for next entry, invoke executor
- [x] 9.2 Wire executor failure/recovery signals into `OnAgentFailed`/`OnAgentRecovered`
- [x] 9.3 Append role-instruction content to assembled prompt when assignee is set
- [x] 9.4 Preserve v0.5.0 fallback-prompt injection in no-backend mode (default route still applies)
- [x] 9.5 Integration test: long relay across multiple roles with quotas, simulated freezes, and role-instruction files

## 10. Documentation

- [x] 10.1 Update README's config section with `[routes]` and example role-aware setup
- [x] 10.2 Document `--agent` syntax with role references
- [x] 10.3 Document `rally routes check` as a CI/Makefile validator
- [x] 10.4 v0.6.0 release notes: routing model, quota syntax (positional `:`-split, last-segment-as-quota, numeric-only key prohibition), per-role instruction file contract (loader-only — file contents authored separately)
- [x] 10.5 Cross-link to microbeads SPEC `assignee` field documentation

## 11. Verification

- [ ] 11.1 Each canonical scenario (1–7) passes integration test exactly per the proposal's documented outcome
- [ ] 11.2 No-backend mode collapses to `default` route correctly; non-default routes loaded but never selected
- [ ] 11.3 Case-insensitive role matching works on Linux ext4 and macOS APFS
- [ ] 11.4 `rally routes check` exits zero on clean config, non-zero on errors, with did-you-mean output for unresolved shortcuts
- [ ] 11.5 `--agent` overrides bead `assignee` for entire relay duration
- [ ] 11.6 Role-instruction file injected when present, silent when absent; deterministic pick when multiple case variants exist
