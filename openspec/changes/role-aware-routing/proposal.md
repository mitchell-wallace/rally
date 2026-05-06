## Why

Rally today uses flat round-robin over a `--mix` of agents. That's the right primitive for uniform queues, but it can't:

- match heavier work to capable models and mechanical work to cheap ones
- express usage caps for managing rate limits or cost (e.g. "use GLM up to 4 consecutive runs, then rotate")
- route per role declared on the lap

Abstracting over harnesses and providers to manage usage limits, pricing, and capability matching is one of rally's primary value propositions. v0.6.0 makes scheduling explicit and per-role.

This proposal does three things: it adds role-aware route lookup driven by laps' `assignee` field (per `openspec/HANDOFF.md`); it generalises round-robin into a single quota-aware scheduler that subsumes both rotation and fallback semantics in one model; and it loads optional per-role instruction files into the prompt (parsing/loading only — the *contents* of `SENIOR.md` etc. are being authored separately).

## Prerequisites

- v0.4.0 (`laps-first-class`) — lap head-pull surfaces the `assignee` field. Laps already specifies `assignee` as an optional string in `../laps/SPEC.md`, so this is purely a rally-side consumption change.
- v0.5.0 (`rally-config-and-harness-shortcuts`) — `[providers]` shortcut keys usable in route entries.

## What Changes

### Single config file: `[routes]` lives in `.rally/config.toml`
No separate `routes.yml`. Rally has one TOML for all configuration; output data is JSON; laps has its own JSON/JSONL storage. Adding routes:

```toml
[routes]
default = ["claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"]
SENIOR  = ["codex:gpt-5.5", "claude:opus-4.7"]
JUNIOR  = ["op:z:4", "op:gk:2", "gemini-pro"]
UI      = ["gemini-pro:2-5", "claude:sonnet-4.6"]
VERIFY  = ["codex:gpt-5.5", "claude:opus-4.7"]
```

Route keys are matched case-insensitively against the lap's `assignee`. `default` is a reserved key that handles the no-role and no-match cases.

### Single scheduling model
There is **one** scheduler. No `policy: fallback` vs. `policy: rotation` distinction — quota presence drives behaviour:

- **No quota** on an entry → unlimited consecutive use; rally rotates only when the entry fails (rate-limit / hard error / freeze)
- **Single quota `:N`** → rotate after exactly `N` consecutive runs (or sooner on failure)
- **Range quota `:N-M`** → rotate after `N` runs if any other entry is available; if all others are exhausted/frozen, continue up to `M`; then force wait

Quota-bearing and quota-free entries can be mixed in the same route. The list is a cycle: at end-of-list, rally returns to the head.

### Quota syntax — string split on `:`, indexed
Rally splits each entry string on `:` literally. The last segment is treated as a quota if and only if it matches `^\d+$` or `^\d+-\d+$`. Remaining segments form the agent identifier.

Resolution rules for the agent identifier (after the quota is split off):

- **1 segment** — looked up against `[providers]` shortcut keys. If matched, expands to the configured `harness:model`. (In `--agent` context only, also matched against route keys; see below.)
- **2 segments** — `harness:model`. The model string may itself contain `/` (e.g. `opencode-go/kimi-k2.6`) but no `:`.
- **3+ segments** — invalid syntax, error.

**Numerical-only shortcut keys are forbidden** at config load. `[providers."op4"]` is fine, `[providers."4"]` is rejected. This keeps `claude:4` unambiguous: `4` matches the numeric quota pattern, leaving `claude` as a 1-segment ID; rally then looks up `claude` in `[providers]` and errors if it isn't a defined shortcut. Models with a non-digit character (e.g. `4.5`, `4o`) are not numeric-only and parse as model strings.

### Resolution order for each iteration
1. **`--agent` flag** (if supplied) — overrides everything for the run; see "agent override" below
2. **Lap `assignee`** (case-insensitive match against route keys) — if matched, use that route. `assignee` is read from `laps get head`'s JSON output per the v0.4.0 laps adapter.
3. **`default` route** — when `assignee` is unset or no role matched

**Within-list rotation/fallback only.** A non-default role never silently falls back to another non-default role. If a role isn't defined and `default` exists, rally falls back to `default` with a warning. If `default` isn't defined and a lap's role isn't defined either, rally exits with an error (see startup validation below).

### Behaviour in no-backend mode
In v0.4.0's no-backend mode (`.laps/laps.json` absent) there is no lap and no `assignee`. Routing collapses to the `default` route on every iteration — the scheduler still applies, quotas still work, and `--agent` overrides still apply. Per-role instruction file loading is also skipped (no role to look up). `[routes]` non-default entries declared in config are loaded but never selected; `rally routes check` flags them as unreachable in this mode.

### `--agent` override (preserves the manual lever)
`--agent` accepts a space-separated list of entries using the same syntax as a route. Entries can be:

- A `harness:model[:quota]` string
- A shortcut name (with optional quota)
- A **role name** (with optional quota) — this is the only context where role names are valid as entries; the role's full route list is inlined into the override roster, optionally capped by the trailing quota (which applies per-cycle to the role's own internal cursor)

When `--agent` is present, laps' `assignee` values are ignored entirely.

### Per-role instruction files (parsing/loading only)
- Rally looks for `.rally/agents/{ASSIGNEE}.md` using case-insensitive matching against the on-disk file list (so `assignee: Senior` resolves to `SENIOR.md`, `senior.md`, or `Senior.md` — first hit on a sorted scan wins, deterministic)
- If found, file contents are appended to the prompt template after base rally instructions and before the lap body
- If absent, no injection (silent — not an error)
- Rally treats the file content as opaque text — no parsing, no front-matter
- **Out of scope: the *contents* of role files** (`SENIOR.md`, `JUNIOR.md`, `UI.md`, `QA.md`, `VERIFY.md`). Those are authored on a separate track. Rally only ships the loader and the prompt wiring.

### Startup validation
- **Invalid syntax in `--agent`** → warn, exit
- **Invalid syntax in `[routes]`, but at least one role parses cleanly** → warn, prompt `Would you like to continue anyway? Invalid roles will fall back to DEFAULT (y/N)`
- **`default` route is invalid (or missing) and `[routes]` is otherwise present** → warn as above; only laps with valid routes can run; if no laps exist in the queue, warn-and-exit instead of prompting
- **Quota out of bounds** (`min > max`, negative numbers) → warn, exit
- **Numerical-only shortcut key in `[providers]`** → warn, exit
- **Duplicate route keys differing only in case** → warn, exit (case-insensitive matching means they'd collide at lookup)

### Validator
- `rally routes check` — parses `[routes]`, resolves all shortcut keys, verifies quotas (`min ≤ max`, `min ≥ 0`), lists declared routes that no current lap's `assignee` references (info-level), errors on parse/resolution failures

## Canonical scenarios

These define the scheduler's behaviour. The implementation must make all seven exhibit the documented outcome.

1. **Roles absent, `default = ["claude:opus-4.7", "codex:gpt-5.5", "opencode:opencode-go/kimi-k2.6"]`, no `--agent`.** No quotas → all unlimited. Opus runs until failure → GPT until failure → Kimi until failure → loop back to Opus.

2. **Roles absent, `default = ["claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"]`, no `--agent`.** Opus once, GPT thrice, Kimi until failure, loop.

3. **Roles `ROLEA`, `ROLEB` exist on laps. `[routes]` defines `default` and `ROLEA`. No `--agent`.** ROLEA laps use ROLEA route; ROLEB laps have no match → warning, fall back to `default`.

4. **Same as scenario 3 but `[routes]` defines `ROLEA` only (no `default`).** Startup warning + y/N prompt; if user proceeds, ROLEA laps run on ROLEA route, ROLEB laps cause rally to exit; if no laps exist at startup, rally warn-and-exits without prompting.

5. **Roles `ROLEA`, `ROLEB` exist, `[routes]` defines `ROLEA`, `ROLEB`, `default`. `--agent "op:opencode-go/fancy-new-model"`.** Override applies to all runs regardless of lap `assignee`. Single agent, no quota → run until failure forever (with retry budget).

6. **Same routes as scenario 5. `--agent "op:opencode-go/fancy-new-model DEFAULT:1"`.** Two-entry roster: the harness:model string (no quota) and the role-reference `DEFAULT:1`. Run fancy-new-model until failure → consume one entry from `default` (e.g. opus once) → fancy-new-model until failure → next entry from `default` (gpt once) → ... `DEFAULT:1` advances `default`'s internal cursor by one each visit.

7. **Roles absent, `default = ["claude:opus-4.7:3", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6:1-5"]`, no `--agent`.** Opus×3 → GPT×3 → Kimi×1 → Opus (rate-limited after 1 run, freezes) → GPT×3 → Kimi×1 → GPT (rate-limited, exhausted) → Kimi can now run up to 5 since all others frozen → after 4 more (5 total in the burst), wait until Opus or GPT recover.

## Capabilities

### New Capabilities
- `role-routing`: Per-role harness/model lists in `[routes]`, selected by case-insensitive `assignee` match, with within-list cycling and fallback to `default` only
- `quota-scheduler`: Single scheduler with per-entry quota semantics — no quota = until-failure, `:N` = rotate after N, `:N-M` = rotate after N (preferred) or M (forced); rotation responds to observed agent unavailability (retry-budget exhaustion, error-driven cooldowns). v0.7.0 extends this with active freeze detection.
- `role-instruction-loader`: Case-insensitive lookup and injection of `.rally/agents/{ASSIGNEE}.md` content into the prompt template
- `agent-override`: `--agent` flag accepting space-separated entries including role-name references (role refs valid only here)
- `routes-validator`: `rally routes check` validates the `[routes]` table

### Modified Capabilities
- `relay-runner`: Per-iteration agent selection routed through the scheduler; legacy `--mix` becomes a synonym for a single-roster `--agent`; prompt assembly appends role-instruction file content when matched; new scheduler hooks (`onAgentFailed`/`onAgentRecovered`) feed exhaustion state
- `repo-config`: `[routes]` table added to `.rally/config.toml` alongside the v0.5.0 sections

_(Note: `laps-only-integration` already surfaces `assignee` per v0.4.0's lap head-pull adapter requirement; no spec delta is needed for this consumption change — v0.6.0 just reads what v0.4.0 exposes.)_

## Impact

- New package: `internal/routing/` (TOML route loader, scheduler with quota tracker, freeze-aware rotation, agent-string parser shared with CLI)
- New package or sibling: `internal/prompt/roleloader/` for instruction-file lookup (case-insensitive, deterministic)
- New CLI flag: `--agent`; new validator subcommand: `rally routes check`
- laps: zero changes needed — `assignee` field already in SPEC.md
- v0.7.0 dependency: provider-rotation reuses the scheduler's quota tracker and freeze-aware rotation logic
- Risk: scheduler complexity — the seven canonical scenarios become the integration-test backbone; any deviation from those outcomes is a regression
- Risk: `--agent` syntax is space-separated, which gets clumsy when entries themselves are long. Quoted form (`--agent "..."`) is the primary documented usage. Repeated `--agent` flag accumulating entries is an alternative but adds parsing weirdness — going with single-flag-quoted for now
- Risk: case-insensitive file matching is filesystem-dependent — implement explicit dir-scan + lowercase compare rather than relying on FS case-folding
