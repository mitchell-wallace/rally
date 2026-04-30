## ADDED Requirements

### Requirement: `--agent` flag override
The system SHALL accept a `--agent` flag on `rally relay` whose value is a space-separated list of agent entries forming an override roster. When present, the override roster SHALL replace per-bead `assignee` routing entirely — bead `assignee` values SHALL be ignored for the duration of the relay. The override is treated as a route by the scheduler (subject to the same quota rules and cycling).

#### Scenario: `--agent` overrides assignee
- **WHEN** `rally relay --agent "claude:opus-4.7"` is invoked and beads carry various `assignee` values
- **THEN** the scheduler SHALL use the single-entry override roster for every iteration regardless of `assignee`

#### Scenario: `--agent` accepts shortcuts
- **WHEN** `--agent "op:z:4 op:gk"` is supplied and `op:z`, `op:gk` are defined in `[providers]`
- **THEN** the parser SHALL resolve each shortcut to its `(harness, model)` tuple, attach the trailing quota where present, and produce a two-entry override roster

### Requirement: Role-name references in `--agent`
The system SHALL accept role-name references as entries in the `--agent` roster. A role-name reference (e.g. `DEFAULT`, `SENIOR`) SHALL inline the named route's full entry list into the override roster at that position. An optional trailing quota on the role reference (e.g. `DEFAULT:1`) SHALL advance the role's internal cursor by that many entries each visit. Role-name references SHALL be valid only inside `--agent` — they SHALL NOT be valid as entries within `[routes]` itself.

#### Scenario: Role reference inlines the named route
- **WHEN** `--agent "claude:opus-4.7 SENIOR"` is supplied and `[routes].SENIOR = ["codex:gpt-5.5", "claude:opus-4.7"]`
- **THEN** the override roster SHALL contain three entries in order: `claude:opus-4.7`, `codex:gpt-5.5`, `claude:opus-4.7` (the SENIOR route inlined)

#### Scenario: Role reference with quota
- **WHEN** `--agent "fancy-model DEFAULT:1"` is supplied and `[routes].default = ["claude:opus-4.7", "codex:gpt-5.5"]`
- **THEN** the override roster has two entries: `fancy-model` (no quota, until failure) and `DEFAULT:1` (consume one entry per visit). On each visit to `DEFAULT:1` the scheduler advances `default`'s internal cursor by one and returns the next entry from `default`

#### Scenario: Role reference inside `[routes]` is invalid
- **WHEN** `[routes].SENIOR = ["claude:opus-4.7", "JUNIOR"]` (a role name appears as an entry)
- **THEN** config load SHALL exit non-zero with an error indicating role-name references are valid only in `--agent`

### Requirement: Invalid `--agent` syntax exits at startup
The system SHALL parse the `--agent` flag at startup, before relay execution begins. Any syntax error SHALL cause rally to exit non-zero with a message naming the offending entry.

#### Scenario: Malformed entry
- **WHEN** `--agent "claude::opus"` (empty middle segment) or `--agent "op:z:abc"` (non-numeric trailing segment with three segments total) is supplied
- **THEN** rally SHALL exit non-zero with a syntax error naming the offending entry

#### Scenario: Unresolved shortcut in `--agent`
- **WHEN** `--agent` references a shortcut key not defined in `[providers]`
- **THEN** rally SHALL exit non-zero with a `did-you-mean` message listing the closest matching defined keys
