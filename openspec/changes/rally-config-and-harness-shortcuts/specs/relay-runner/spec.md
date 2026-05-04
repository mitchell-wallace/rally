## MODIFIED Requirements

### Requirement: Agent mix cycling
The system SHALL cycle through agents in a deterministic rotation based on the configured agent mix. The mix SHALL accept three entry forms: weighted harness shortcuts as in v0.2.x (e.g. `cc:2 cx:1`), raw `harness:model` strings, and shortcut keys defined in `[providers]`. All three forms MAY be combined in the same mix.

#### Scenario: Deterministic agent selection
- **WHEN** a run is about to start within a relay
- **THEN** the system SHALL select the agent using `cycle[(runIndex) % len(cycle)]`

#### Scenario: Agent mix parsed from spec
- **WHEN** agent specs like `"cc:2 cx:1"` are provided
- **THEN** the system SHALL parse them into weighted cycles preserving declaration order

#### Scenario: Mix accepts shortcut keys
- **WHEN** `--mix "claude,op:z,op:gk"` is provided and `op:z`, `op:gk` are defined in `[providers]`
- **THEN** the parser SHALL resolve each shortcut to its `(harness, model)` tuple via the config-layer resolver and SHALL produce a cycle whose entries are the resolved tuples

#### Scenario: Mix mixes raw and shortcut forms
- **WHEN** `--mix "claude,op:z,opencode:opencode-go/kimi-k2.6"` is provided
- **THEN** the parser SHALL accept the mix, resolving `op:z` via shortcut and using the raw form verbatim, producing a three-entry cycle

## ADDED Requirements

### Requirement: Defaults sourced from `[defaults]` config section
The system SHALL read `iterations`, `mix`, and `verbose` from the `[defaults]` section of `.rally/config.toml` when the corresponding CLI flag is not supplied. CLI flags SHALL continue to take precedence when present.

#### Scenario: Iterations from config
- **WHEN** `[defaults].iterations = 25` is set and `rally relay` is invoked without `--iterations`
- **THEN** the relay SHALL use 25 iterations

#### Scenario: Mix from config
- **WHEN** `[defaults].mix = "claude,op:z"` is set and `rally relay` is invoked without `--mix`
- **THEN** the relay SHALL use the configured mix (after shortcut resolution)

#### Scenario: CLI flag wins over config default
- **WHEN** both `[defaults].iterations = 25` is set and `rally relay --iterations 5` is invoked
- **THEN** the relay SHALL use 5 iterations

### Requirement: Fallback prompt injection in no-backend mode
The system SHALL inject the `[fallback].instructions_file` content (or a built-in default if unconfigured/missing) as the prompt body when (a) rally is in no-backend mode and (b) no ready bead exists for the current iteration. The injection SHALL replace the bead-body slot in the prompt template; other prompt sections (persona, retry context, etc.) SHALL be unaffected.

#### Scenario: No-backend, no ready bead
- **WHEN** rally is in no-backend mode and no ready bead exists for the iteration
- **THEN** the prompt body SHALL be the contents of `[fallback].instructions_file` if configured and readable, otherwise the built-in default fallback content

#### Scenario: No-backend with ready bead
- **WHEN** rally is in no-backend mode and a ready bead exists (e.g. via the configured fallback file describing a task that the relay then "claims")
- **THEN** the bead body SHALL be used as the prompt body and the fallback file SHALL NOT be substituted
