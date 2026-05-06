## ADDED Requirements

### Requirement: Per-role instruction file lookup and injection
The system SHALL look for a per-role instruction file in `.rally/agents/` whose basename (without extension) matches the active lap's `assignee` value case-insensitively. The first hit on a sorted directory scan SHALL be selected (deterministic across filesystems). The file's contents SHALL be appended to the prompt template after the base rally instructions and before the lap body. The file content SHALL be treated as opaque text — no front-matter parsing, no template expansion. A missing file SHALL NOT be an error; the prompt is built without role-specific instructions.

#### Scenario: Role file found and injected
- **WHEN** the active lap has `assignee: SENIOR` and `.rally/agents/SENIOR.md` exists
- **THEN** the prompt SHALL include the file's contents in the role-instruction slot

#### Scenario: Case-insensitive match across filesystems
- **WHEN** the active lap has `assignee: Senior` and the only matching file is `.rally/agents/senior.md`
- **THEN** the loader SHALL find and inject `.rally/agents/senior.md`'s contents

#### Scenario: Multiple case variants on disk
- **WHEN** `.rally/agents/` contains both `Senior.md` and `senior.md` (possible on case-sensitive filesystems)
- **THEN** the loader SHALL pick the first hit on a deterministic sorted scan; the same file SHALL be picked on every run

#### Scenario: No matching file
- **WHEN** the active lap has `assignee: ROLEX` and no file in `.rally/agents/` matches `ROLEX` case-insensitively
- **THEN** the prompt SHALL be built without role-specific instructions; no error or warning SHALL be emitted

#### Scenario: No assignee
- **WHEN** the active lap has no `assignee` field
- **THEN** the role-instruction-loader SHALL be skipped entirely (no scan, no injection)

#### Scenario: No-backend mode skips loading
- **WHEN** rally is in no-backend mode (no lap, no assignee)
- **THEN** the role-instruction-loader SHALL be skipped entirely
