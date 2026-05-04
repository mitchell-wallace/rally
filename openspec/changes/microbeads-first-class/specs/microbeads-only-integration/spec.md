## ADDED Requirements

### Requirement: Microbeads mode detection
The system SHALL determine whether microbeads is enabled at startup by checking two conditions: (1) `.beads/mb.json` is discoverable from the workspace root per the microbeads SPEC §Storage, AND (2) the `mb` binary is available on PATH. Both conditions MUST be true for microbeads to be enabled. If either condition is false, microbeads is disabled and rally operates in no-backend mode.

#### Scenario: Both mb.json and mb binary present
- **WHEN** rally starts in a workspace where `.beads/mb.json` is discoverable from cwd AND `mb` is on PATH
- **THEN** rally SHALL operate with microbeads enabled

#### Scenario: mb.json exists but mb binary not on PATH
- **WHEN** rally starts in a workspace where `.beads/mb.json` exists but `mb` is not on PATH
- **THEN** rally SHALL operate with microbeads disabled (no-backend mode)

#### Scenario: mb on PATH but no mb.json
- **WHEN** rally starts in a workspace where `mb` is on PATH but `.beads/mb.json` does not exist
- **THEN** rally SHALL operate with microbeads disabled (no-backend mode)

#### Scenario: bare .beads/ directory without mb.json
- **WHEN** rally starts in a workspace where `.beads/` exists but `.beads/mb.json` does not
- **THEN** rally SHALL operate with microbeads disabled regardless of whether `mb` is on PATH

### Requirement: Bead head-pull adapter
The system SHALL retrieve the next ready microbead when microbeads is enabled by invoking `mb get head` and parsing the command output. The adapter SHALL surface the microbead's `id`, `title`, `description`, and `assignee` fields to the relay runner.

#### Scenario: Head microbead returned
- **WHEN** the relay runner requests the next task with microbeads enabled and `mb get head` returns a task
- **THEN** the adapter SHALL parse the output and return a Microbead struct containing `id`, `title`, `description`, and `assignee` (the latter possibly empty)

#### Scenario: Empty queue
- **WHEN** `mb get head` exits non-zero (including the "no head task" case)
- **THEN** the adapter SHALL return a no-microbead sentinel without raising an error; the relay runner uses the configured fallback prompt

### Requirement: Hook installer
The system SHALL maintain rally-owned entries in `.beads/mb-hooks.json` when microbeads is enabled. The installer SHALL identify rally entries by a `rally:` prefix in the hook `name` field, SHALL preserve any user-edited hooks for the same `(command, when)` pairs, and SHALL be idempotent across re-runs. Hook scripts SHALL be embedded in the rally binary via `//go:embed` and written to `.beads/hooks/rally/` in the workspace.

#### Scenario: First-time installation
- **WHEN** rally runs with microbeads enabled and `.beads/mb-hooks.json` lacks rally-keyed entries
- **THEN** the installer SHALL append the rally-keyed hook entries (`rally:mb-done`, `rally:mb-wrapup`, `rally:mb-handoff`) without modifying any user entries, and SHALL write hook scripts to `.beads/hooks/rally/`

#### Scenario: Re-run without changes
- **WHEN** rally runs again in a workspace where its hook entries already exist with current contents
- **THEN** the installer SHALL leave the file unchanged (idempotency)

#### Scenario: Coexistence with user hooks
- **WHEN** the user has their own hook entry on `mb done` after-hook with a non-rally `name`
- **THEN** the installer SHALL leave the user entry intact and append rally's entry alongside

### Requirement: Microbeads instruction injection is unconditional when enabled
The system SHALL inject microbeads-related instructions into the agent prompt whenever microbeads is enabled, with no toggle to disable injection. The legacy field `Beads string` (with values `"true"|"false"|"auto"`) SHALL be removed outright — not renamed, not preserved under a new key.

#### Scenario: Microbeads enabled always injects
- **WHEN** rally is operating with microbeads enabled
- **THEN** the prompt SHALL contain microbeads instructions regardless of any workspace `CLAUDE.md` / `AGENTS.md` content

#### Scenario: Legacy Beads field absent
- **WHEN** rally loads `.rally/config.toml`
- **THEN** the loader SHALL NOT recognise a top-level `Beads` field; an unknown-field warning is acceptable but no behaviour SHALL be wired to it
