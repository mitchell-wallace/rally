## ADDED Requirements

### Requirement: `[routes]` section in `.rally/config.toml`
The system SHALL accept a top-level `[routes]` table in `.rally/config.toml` (alongside the v0.5.0 sections `[defaults]`, `[laps]`, `[fallback]`, `[providers]`). Each entry's value SHALL be a string array of agent specs (raw `harness:model[:quota]`, shortcut keys with optional quota). The `default` key SHALL be reserved for the no-role / no-match case. Other keys SHALL be role names matched case-insensitively against lap `assignee` values.

#### Scenario: Routes loaded alongside other sections
- **WHEN** `.rally/config.toml` contains `[providers]`, `[defaults]`, `[laps]`, `[fallback]`, AND `[routes]`
- **THEN** the loader SHALL parse all sections successfully; `[routes]` SHALL be parsed via the agent-spec resolver and SHALL be available to the routing layer

#### Scenario: Routes section absent
- **WHEN** `.rally/config.toml` has no `[routes]` table
- **THEN** rally SHALL operate in the v0.5.0 single-mix model: `--mix` (or `--agent`) supplies the entire roster, no per-lap routing applies
