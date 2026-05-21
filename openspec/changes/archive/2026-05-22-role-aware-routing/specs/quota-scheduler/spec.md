## ADDED Requirements

### Requirement: Single scheduler with quota-aware behaviour
The system SHALL provide a single scheduler that consumes an ordered list of agent entries (a "route") and produces a stream of agent selections per iteration. Each entry MAY carry an optional quota suffix:

- **No quota** → unlimited consecutive use of this entry; the scheduler advances only when the entry fails (retry-budget exhaustion, hard error, rate-limit cooldown, or in v0.7.0 freeze detection)
- **Single quota `:N`** (where `N` is a positive integer) → advance after exactly N consecutive runs (or sooner on failure)
- **Range quota `:N-M`** (where `N <= M`, both positive integers) → advance after N runs if any other entry is available; if all other entries are exhausted/frozen, continue up to M runs total; after M, force-wait until any other entry recovers

The scheduler SHALL NOT distinguish between "rotation" and "fallback" policy modes — quota presence and form encode the intent.

#### Scenario: No quota = until failure
- **WHEN** the route is `["claude:opus-4.7", "codex:gpt-5.5", "opencode:opencode-go/kimi-k2.6"]` and no entry carries a quota
- **THEN** the scheduler SHALL select `claude:opus-4.7` for every iteration until that entry observes a failure, then select `codex:gpt-5.5` until *its* failure, then `opencode:...`, then loop back to `claude:opus-4.7`

#### Scenario: Single quota rotates after exactly N
- **WHEN** the route is `["claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"]`
- **THEN** the scheduler SHALL select `claude:opus-4.7` once, then `codex:gpt-5.5` three times, then `opencode:...` until failure, then loop

#### Scenario: Range quota prefers minimum, allows maximum, forces wait beyond
- **WHEN** the route is `["claude:opus-4.7:3", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6:1-5"]` and Opus rate-limit-freezes after iteration 4 (within its quota), GPT rate-limit-freezes after iteration 7
- **THEN** the scheduler SHALL select Opus three times, GPT three times, Kimi once (its preferred N), attempt Opus (frozen, skipped), GPT three times, Kimi once again, attempt Opus (frozen), attempt GPT (frozen), then continue selecting Kimi up to 5 total runs in this burst, then force-wait until Opus or GPT recovers

#### Scenario: End-of-list cycles back to head
- **WHEN** the scheduler reaches the end of the route list
- **THEN** the next selection SHALL be the head of the list, with each entry's per-cycle counters reset for the new pass

#### Scenario: Mixed quota and no-quota entries
- **WHEN** the route mixes entries with and without quotas
- **THEN** quota-bearing entries respect their counters; no-quota entries run until failure; all entries participate in the same cycle order

### Requirement: Quota syntax — positional `:`-split
The system SHALL parse each entry by splitting on `:`. The last segment SHALL be treated as a quota if and only if it matches `^\d+$` or `^\d+-\d+$`. Remaining segments form the agent identifier and resolve as: 1 segment → shortcut-key lookup against `[providers]`; 2 segments → `harness:model`; 3 or more segments → invalid syntax error.

#### Scenario: Single-segment shortcut with quota
- **WHEN** the entry is `op:z:4` and `op:z` is defined in `[providers]`
- **THEN** the parser SHALL split into segments `["op", "z", "4"]`, recognise `4` as the quota, and treat `op:z` as a shortcut key resolving to the configured `(harness, model)`

#### Scenario: Two-segment harness:model with range quota
- **WHEN** the entry is `opencode:opencode-go/kimi-k2.6:1-5`
- **THEN** the parser SHALL split into segments, recognise `1-5` as the range quota, and parse `opencode:opencode-go/kimi-k2.6` as a `harness:model` pair

#### Scenario: Three-segment without quota is invalid
- **WHEN** the entry is `claude:opus:4.7` (three segments, last not numeric)
- **THEN** the parser SHALL exit non-zero with a syntax error naming the offending entry

#### Scenario: Quota out of bounds
- **WHEN** the entry's quota is `5-3` (min > max), `0`, or negative
- **THEN** the parser SHALL exit non-zero with a clear error message

#### Scenario: Models with embedded digits parse correctly
- **WHEN** the entry is `claude:claude-4.5-sonnet` (digits in model, but the model contains non-digits)
- **THEN** the parser SHALL parse `claude-4.5-sonnet` as the model (not as a quota) since the segment is not pure-digit nor `\d+-\d+`

### Requirement: Failure-driven advance
The system SHALL advance the scheduler to the next entry on observed agent failure, regardless of remaining quota. Failure conditions include: retry-budget exhaustion for the current try, harness-reported rate-limit or cooldown signal, and (in v0.7.0) detected freeze. The failed entry is marked exhausted/frozen and SHALL be skipped on subsequent visits within the same cycle until it recovers or the cycle resets.

#### Scenario: Failure short-circuits a quota
- **WHEN** an entry has quota `:5` but fails on the third consecutive run
- **THEN** the scheduler SHALL advance to the next entry immediately, marking the failed entry exhausted for the current cycle

#### Scenario: Exhausted entry skipped within cycle
- **WHEN** the scheduler has marked an entry exhausted and the cycle has not yet reset
- **THEN** subsequent attempts to schedule that entry within the same cycle SHALL be skipped; the scheduler proceeds to the next entry

#### Scenario: Exhaustion clears at cycle boundary
- **WHEN** the scheduler reaches the end of the route list and wraps to the head
- **THEN** the exhausted/frozen flags MAY be cleared (subject to harness recovery signal); v0.6.0 clears on cycle wrap, v0.7.0 may refine

### Requirement: Force-wait when all entries exhausted
The system SHALL block iteration progress (without exiting the relay) when all entries in the active route are exhausted/frozen and no entry's range-quota maximum has been reached such that one is still permitted to run. The relay SHALL display a "waiting for agent recovery" status and resume when any entry's executor signals recovery.

#### Scenario: All entries frozen
- **WHEN** every entry in the active route has been marked exhausted/frozen and no entry retains permitted runs under its range quota
- **THEN** the scheduler SHALL pause iteration, display a wait status, and resume on the first entry that signals recovery

### Requirement: Canonical scheduler scenarios
The system's scheduler implementation SHALL exhibit the documented behaviour for each of the seven canonical scenarios listed in the change proposal. Any deviation from a scenario's documented outcome SHALL be considered a regression.

#### Scenario: Scenario 1 — uniform default route, no quotas
- **WHEN** roles are absent, `default = ["claude:opus-4.7", "codex:gpt-5.5", "opencode:opencode-go/kimi-k2.6"]`, no `--agent`
- **THEN** Opus runs until failure, then GPT until failure, then Kimi until failure, then loops back to Opus

#### Scenario: Scenario 2 — mixed quota and no-quota in default
- **WHEN** roles are absent, `default = ["claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"]`, no `--agent`
- **THEN** Opus runs once, GPT runs three times, Kimi runs until failure, then loops

#### Scenario: Scenario 3 — partial role coverage falls back to default
- **WHEN** laps carry `assignee: ROLEA` or `assignee: ROLEB`; `[routes]` defines `default` and `ROLEA` only
- **THEN** ROLEA laps use the ROLEA route; ROLEB laps have no match and fall back to `default` with a warning

#### Scenario: Scenario 4 — no default route
- **WHEN** laps carry `ROLEA` or `ROLEB`; `[routes]` defines `ROLEA` only (no `default`)
- **THEN** at startup, rally warns and prompts y/N; if confirmed, ROLEA laps run on ROLEA route while ROLEB laps cause exit; if no laps exist at startup, rally warn-and-exits without prompting

#### Scenario: Scenario 5 — single-agent override
- **WHEN** `[routes]` defines `ROLEA`, `ROLEB`, `default`; `--agent "op:opencode-go/fancy-new-model"` is supplied
- **THEN** the override applies to all runs regardless of lap `assignee`; with no quota, the single agent runs until failure forever (within retry budget)

#### Scenario: Scenario 6 — override with role reference
- **WHEN** `[routes]` defines `ROLEA`, `ROLEB`, `default`; `--agent "op:opencode-go/fancy-new-model DEFAULT:1"` is supplied
- **THEN** the override roster is two entries: the harness:model with no quota and the role-reference `DEFAULT:1`. The scheduler runs fancy-new-model until failure, advances one step into `default` (e.g. Opus once), runs fancy-new-model until failure, advances one more step in `default` (GPT once), and so on. Each visit to `DEFAULT:1` advances `default`'s internal cursor by exactly one.

#### Scenario: Scenario 7 — range quota under cascading freezes
- **WHEN** roles are absent; `default = ["claude:opus-4.7:3", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6:1-5"]`; no `--agent`; Opus rate-limit-freezes after iteration 4, GPT after iteration 7
- **THEN** the schedule is: Opus×3 → GPT×3 → Kimi×1 → Opus (frozen, skipped) → GPT×3 → Kimi×1 → GPT (exhausted, skipped) → Kimi continues up to 5 total in the burst → after the 5th Kimi, force-wait until Opus or GPT recovers
