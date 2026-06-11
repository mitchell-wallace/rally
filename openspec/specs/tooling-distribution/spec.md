# tooling-distribution Specification

## Purpose
TBD - created by archiving change tidy-rally-runtime-data-storage. Update Purpose after archive.
## Requirements
### Requirement: Laps bundled alongside rally
The installer SHALL install the `laps` binary alongside `rally` so that laps is available as a first-class companion. Laps SHALL remain independently usable, and `.laps/` SHALL stay a separate top-level directory; rally SHALL NOT relocate `.laps/` into `.rally/`, and laps SHALL NOT read rally state from `.rally/`.

#### Scenario: Installer provisions laps
- **WHEN** a user runs the rally installer
- **THEN** the installer SHALL place a compatible `laps` binary next to the `rally` binary

#### Scenario: Laps stays decoupled
- **WHEN** laps is invoked standalone
- **THEN** it SHALL operate against `.laps/` without requiring or reading `.rally/`

### Requirement: rally update command
The system SHALL provide a `rally update` command that upgrades both the `rally` and `laps` binaries.

#### Scenario: Update upgrades both binaries
- **WHEN** a user runs `rally update`
- **THEN** the command SHALL upgrade both `rally` and `laps` to their latest compatible versions

### Requirement: Minimum laps version check
On startup the system SHALL check the installed laps version against the minimum required by Rally's companion contract, including installed hooks and checked-in agent workflows, and SHALL warn (without hard-failing) when laps is too old. The minimum supported release for current Rally source SHALL be laps v0.8.1.

#### Scenario: Outdated laps warns
- **WHEN** rally starts and detects a laps version below the minimum
- **THEN** rally SHALL print a warning advising `rally update` and SHALL continue running

#### Scenario: Compatible laps is silent
- **WHEN** the installed laps version meets the minimum
- **THEN** rally SHALL not emit a version warning

### Requirement: Track laps.json for handoff
The repository SHALL track `.laps/laps.json` so the work queue travels between containers via git. The stray manually-committed `.laps/.gitignore` that excluded laps files SHALL be removed, and rally SHALL NOT generate a `.gitignore` under `.laps/`.

#### Scenario: laps.json is committed
- **WHEN** the `.laps/` directory is committed to git
- **THEN** `laps.json` SHALL be included so the queue is shareable across containers

#### Scenario: rally never writes .laps/.gitignore
- **WHEN** rally initializes or installs laps hooks
- **THEN** it SHALL NOT create a `.gitignore` inside `.laps/`
