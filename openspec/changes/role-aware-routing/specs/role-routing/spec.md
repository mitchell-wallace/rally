## ADDED Requirements

### Requirement: `[routes]` table in `.rally/config.toml`
The system SHALL accept a top-level `[routes]` table in `.rally/config.toml` whose entries are string arrays. Each route key is a role name (case-insensitive); the special key `default` is reserved for the no-role and no-match cases. Each entry in a route's array is an agent spec parseable by the `quota-scheduler` capability (raw `harness:model[:quota]`, shortcut key with optional quota, etc.).

```toml
[routes]
default = ["claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"]
SENIOR  = ["codex:gpt-5.5", "claude:opus-4.7"]
JUNIOR  = ["op:z:4", "op:gk:2", "gemini-pro"]
```

#### Scenario: Routes loaded from config
- **WHEN** `.rally/config.toml` declares `[routes]` with `default` and other role keys
- **THEN** the loader SHALL parse each route's entries through the agent-spec resolver and SHALL register the routes for selection

#### Scenario: Duplicate keys differing only in case
- **WHEN** `[routes]` declares both `SENIOR` and `senior` (or any other case variants of the same name)
- **THEN** config load SHALL exit non-zero with an error naming the conflicting keys

### Requirement: Route selection by bead `assignee`
The system SHALL select the active route for each iteration in this priority order:
1. The `--agent` override roster, if supplied (per `agent-override` capability)
2. The `[routes]` entry whose key matches the bead's `assignee` field case-insensitively
3. The `default` route, if step 2 produced no match (or the bead has no `assignee`)

A non-default role SHALL NOT silently fall back to another non-default role. If a role is undefined and `default` exists, rally SHALL fall back to `default` with a per-iteration warning. If neither the role nor `default` exists, rally SHALL exit non-zero on the first such iteration with an error naming the missing role.

#### Scenario: Bead with matching assignee
- **WHEN** the active bead has `assignee: SENIOR` and `[routes].SENIOR` is defined
- **THEN** the scheduler SHALL use the SENIOR route for this iteration

#### Scenario: Bead with assignee that has no matching route, default exists
- **WHEN** the active bead has `assignee: ROLEX` (no matching `[routes]` key) and `[routes].default` is defined
- **THEN** the scheduler SHALL use `default` for this iteration and rally SHALL log a one-line warning naming `ROLEX` as unmatched

#### Scenario: Bead with assignee that has no matching route, no default
- **WHEN** the active bead has `assignee: ROLEX` and neither `[routes].ROLEX` nor `[routes].default` is defined
- **THEN** rally SHALL exit non-zero with an error naming `ROLEX` as unmatched and `default` as missing

#### Scenario: Bead with no assignee
- **WHEN** the active bead has no `assignee` field (or it is empty)
- **THEN** the scheduler SHALL use the `default` route

#### Scenario: Case-insensitive matching
- **WHEN** the active bead has `assignee: Senior` and `[routes].SENIOR` is defined (different case)
- **THEN** the scheduler SHALL match and use the SENIOR route

### Requirement: No-backend mode collapses to default
The system SHALL behave consistently in no-backend mode (`.beads/mb.json` absent): there is no bead and no `assignee`, so routing SHALL collapse to the `default` route on every iteration. The scheduler still applies, quotas still work, `--agent` overrides still apply. Per-role instruction-file loading SHALL be skipped (no role to look up). `[routes]` entries other than `default` SHALL be loaded but never selected; `rally routes check` SHALL flag them as unreachable in this mode.

#### Scenario: No-backend with default route
- **WHEN** rally is in no-backend mode and `[routes].default` is defined
- **THEN** every iteration SHALL use the `default` route with the configured quotas

#### Scenario: No-backend with non-default routes
- **WHEN** rally is in no-backend mode and `[routes]` contains non-default keys (e.g. `SENIOR`, `JUNIOR`)
- **THEN** those routes SHALL be loaded and validated but never selected for an iteration; `rally routes check` SHALL list them as unreachable in this mode
