## ADDED Requirements

### Requirement: Microbeads mode detection
The system SHALL determine its bead-tracker mode at startup by checking for `.beads/mb.json` discoverable from the workspace root per the microbeads SPEC §Storage. The result is "microbeads-backed" when the file exists and "no-backend" otherwise. The presence of the `mb` binary on PATH or a bare `.beads/` directory SHALL NOT be sufficient to declare microbeads-backed mode.

#### Scenario: mb.json exists
- **WHEN** rally starts in a workspace where `.beads/mb.json` is discoverable from cwd
- **THEN** rally SHALL operate in microbeads-backed mode

#### Scenario: bare .beads/ directory without mb.json
- **WHEN** rally starts in a workspace where `.beads/` exists but `.beads/mb.json` does not
- **THEN** rally SHALL operate in no-backend mode (the directory may belong to `beads`, `beads_rust`, or another fork)

#### Scenario: mb on PATH but no .beads/
- **WHEN** rally starts in a workspace where `mb` is on PATH but no `.beads/` directory exists
- **THEN** rally SHALL operate in no-backend mode

### Requirement: Bead head-pull adapter
The system SHALL retrieve the next ready bead in microbeads-backed mode by invoking `mb get head` and parsing the JSON output. The adapter SHALL surface the bead's `id`, `title`, `description`, and `assignee` fields to the relay runner.

#### Scenario: Head bead returned
- **WHEN** the relay runner requests the next task in microbeads-backed mode and `mb get head` returns a task
- **THEN** the adapter SHALL parse the JSON output and return a bead struct containing `id`, `title`, `description`, and `assignee` (the latter possibly empty)

#### Scenario: Empty queue
- **WHEN** `mb get head` returns the literal string `no head task` and exits non-zero
- **THEN** the adapter SHALL return a no-bead sentinel without raising an error; the relay runner uses the configured fallback prompt

### Requirement: Hook installer
The system SHALL maintain rally-owned entries in `.beads/mb-hooks.json` in microbeads-backed mode. The installer SHALL identify rally entries by a `rally:` prefix in the hook `name` field, SHALL preserve any user-edited hooks for the same `(command, when)` pairs, and SHALL be idempotent across re-runs.

#### Scenario: First-time installation
- **WHEN** rally runs in microbeads-backed mode and `.beads/mb-hooks.json` lacks rally-keyed entries
- **THEN** the installer SHALL append the three rally-keyed hook entries (`rally:mb-done`, `rally:mb-wrapup`, `rally:mb-handoff`) without modifying any user entries

#### Scenario: Re-run without changes
- **WHEN** rally runs again in a workspace where its hook entries already exist with current contents
- **THEN** the installer SHALL leave the file unchanged (idempotency)

#### Scenario: Coexistence with user hooks
- **WHEN** the user has their own hook entry on `mb done` after-hook with a non-rally `name`
- **THEN** the installer SHALL leave the user entry intact and append rally's entry alongside

### Requirement: Microbeads instruction injection is unconditional in microbeads-backed mode
The system SHALL inject microbeads-related instructions into the agent prompt whenever microbeads-backed mode is detected, with no toggle to disable injection. The legacy field `Beads string` (with values `"true"|"false"|"auto"`) SHALL be removed outright — not renamed, not preserved under a new key.

#### Scenario: Microbeads-backed mode always injects
- **WHEN** rally is operating in microbeads-backed mode
- **THEN** the prompt SHALL contain microbeads instructions regardless of any workspace `CLAUDE.md` / `AGENTS.md` content

#### Scenario: Legacy Beads field absent
- **WHEN** rally loads `.rally/config.toml`
- **THEN** the loader SHALL NOT recognise a top-level `Beads` field; an unknown-field warning is acceptable but no behaviour SHALL be wired to it
