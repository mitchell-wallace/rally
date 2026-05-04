## ADDED Requirements

### Requirement: Laps mode detection
The system SHALL determine whether laps is enabled at startup by checking two conditions: (1) `.laps/laps.json` is discoverable from the workspace root per the laps SPEC §Storage, AND (2) the `laps` binary is available on PATH. Both conditions MUST be true for laps to be enabled. If either condition is false, laps is disabled and rally operates in no-backend mode.

#### Scenario: Both laps.json and laps binary present
- **WHEN** rally starts in a workspace where `.laps/laps.json` is discoverable from cwd AND `laps` is on PATH
- **THEN** rally SHALL operate with laps enabled

#### Scenario: laps.json exists but laps binary not on PATH
- **WHEN** rally starts in a workspace where `.laps/laps.json` exists but `laps` is not on PATH
- **THEN** rally SHALL operate with laps disabled (no-backend mode)

#### Scenario: laps on PATH but no laps.json
- **WHEN** rally starts in a workspace where `laps` is on PATH but `.laps/laps.json` does not exist
- **THEN** rally SHALL operate with laps disabled (no-backend mode)

#### Scenario: bare .laps/ directory without laps.json
- **WHEN** rally starts in a workspace where `.laps/` exists but `.laps/laps.json` does not
- **THEN** rally SHALL operate with laps disabled regardless of whether `laps` is on PATH

### Requirement: Lap head-pull adapter
The system SHALL retrieve the next ready lap when laps is enabled by invoking `laps get head` and parsing the command output. The adapter SHALL surface the lap's `id`, `title`, `description`, and `assignee` fields to the relay runner.

#### Scenario: Head lap returned
- **WHEN** the relay runner requests the next task with laps enabled and `laps get head` returns a task
- **THEN** the adapter SHALL parse the output and return a Lap struct containing `id`, `title`, `description`, and `assignee` (the latter possibly empty)

#### Scenario: Empty queue
- **WHEN** `laps get head` exits non-zero (including the "no head task" case)
- **THEN** the adapter SHALL return a no-lap sentinel without raising an error; the relay runner uses the configured fallback prompt

### Requirement: Hook installer
The system SHALL maintain rally-owned entries in `.laps/hooks.json` when laps is enabled. The installer SHALL identify rally entries by a `rally:` prefix in the hook `name` field, SHALL preserve any user-edited hooks for the same `(command, when)` pairs, and SHALL be idempotent across re-runs. Hook scripts SHALL be embedded in the rally binary via `//go:embed` and written to `.laps/hooks/rally/` in the workspace.

#### Scenario: First-time installation
- **WHEN** rally runs with laps enabled and `.laps/hooks.json` lacks rally-keyed entries
- **THEN** the installer SHALL append the rally-keyed hook entries (`rally:laps-done`, `rally:laps-wrapup`, `rally:laps-handoff`) without modifying any user entries, and SHALL write hook scripts to `.laps/hooks/rally/`

#### Scenario: Re-run without changes
- **WHEN** rally runs again in a workspace where its hook entries already exist with current contents
- **THEN** the installer SHALL leave the file unchanged (idempotency)

#### Scenario: Coexistence with user hooks
- **WHEN** the user has their own hook entry on `laps done` after-hook with a non-rally `name`
- **THEN** the installer SHALL leave the user entry intact and append rally's entry alongside

### Requirement: Laps instruction injection is unconditional when enabled
The system SHALL inject laps-related instructions into the agent prompt whenever laps is enabled, with no toggle to disable injection. The legacy field `Beads string` (with values `"true"|"false"|"auto"`) SHALL be removed outright — not renamed, not preserved under a new key.

#### Scenario: Laps enabled always injects
- **WHEN** rally is operating with laps enabled
- **THEN** the prompt SHALL contain laps instructions regardless of any workspace `CLAUDE.md` / `AGENTS.md` content

#### Scenario: Legacy Beads field absent
- **WHEN** rally loads `.rally/config.toml`
- **THEN** the loader SHALL NOT recognise a top-level `Beads` field; an unknown-field warning is acceptable but no behaviour SHALL be wired to it
