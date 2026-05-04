## MODIFIED Requirements

### Requirement: Agent mix cycling
The system SHALL cycle through agents in a deterministic rotation based on the configured agent mix. The mix SHALL accept four entry forms in any combination: bare alias (`claude`), weighted alias (`cc:2 cx:1`, digits-after-colon → weight), named-model entry (`harness:model-name`), and raw harness:model string (`opencode:opencode-go/kimi-k2.6`). A third colon-separated segment (`cc:opus:2`) SHALL be rejected in v0.5.0. The cycle SHALL be a slice of typed `(harness, model)` records, not a slice of strings; `AgentForRun` SHALL return that record.

#### Scenario: Deterministic agent selection
- **WHEN** a run is about to start within a relay
- **THEN** the system SHALL select the agent using `cycle[(runIndex) % len(cycle)]` and SHALL surface its `harness` and `model` as separate fields to the executor

#### Scenario: Weighted alias parsed (v0.2.x form)
- **WHEN** agent specs like `"cc:2 cx:1"` are provided
- **THEN** the system SHALL parse them into weighted cycles preserving declaration order, with each entry resolving to `(harness, default-model-from-flat-field)`

#### Scenario: Mix accepts named-model entries
- **WHEN** `--mix "claude,op:z,op:gk"` is provided and `op:z`, `op:gk` are defined under `[harness.op.models]`
- **THEN** the parser SHALL resolve each named entry to its `(harness, model)` record via the config-layer resolver and SHALL produce a cycle whose entries are the resolved records

#### Scenario: Mix mixes weighted, named, and raw forms
- **WHEN** `--mix "cc:2,op:z,opencode:opencode-go/kimi-k2.6"` is provided
- **THEN** the parser SHALL accept the mix, expanding `cc:2` into two `(claude, default-model)` cycle entries, resolving `op:z` via the named-model table, and using the raw form verbatim

#### Scenario: Third colon segment rejected
- **WHEN** a mix contains `cc:opus:2`
- **THEN** parsing SHALL exit non-zero with an error explaining that weight-on-named-model is not supported in v0.5.0

## ADDED Requirements

### Requirement: Defaults sourced from `[defaults]` config section
The system SHALL read `iterations` and `mix` from the `[defaults]` section of `.rally/config.toml` when the corresponding CLI flag is not supplied. CLI flags SHALL continue to take precedence when present.

#### Scenario: Iterations from config
- **WHEN** `[defaults].iterations = 25` is set and `rally relay` is invoked without `--iterations`
- **THEN** the relay SHALL use 25 iterations

#### Scenario: Mix from config
- **WHEN** `[defaults].mix = "claude,op:z"` is set and `rally relay` is invoked without `--mix`
- **THEN** the relay SHALL use the configured mix (after named-model resolution)

#### Scenario: CLI flag wins over config default
- **WHEN** both `[defaults].iterations = 25` is set and `rally relay --iterations 5` is invoked
- **THEN** the relay SHALL use 5 iterations

### Requirement: Fallback prompt injection in no-backend mode
The system SHALL inject the `[fallback].instructions_file` content (or a built-in default if unconfigured/missing) as the prompt body when (a) rally is in no-backend mode and (b) no ready bead exists for the current iteration. The injection SHALL replace the bead-body slot in the prompt template; other prompt sections (persona, retry context, etc.) SHALL be unaffected.

#### Scenario: No-backend, no ready bead
- **WHEN** rally is in no-backend mode and no ready bead exists for the iteration
- **THEN** the prompt body SHALL be the contents of `[fallback].instructions_file` if configured and readable, otherwise the built-in default fallback content

#### Scenario: No-backend with ready bead
- **WHEN** rally is in no-backend mode and a ready bead exists
- **THEN** the bead body SHALL be used as the prompt body and the fallback file SHALL NOT be substituted

### Requirement: User-defined harness dispatch
The system SHALL dispatch runs whose resolved harness has a `[harness.<name>]` entry with a `command` field through a generic executor that templates `$MODEL` and `$PROMPT` and applies the configured output strategy. Runs whose resolved harness is built-in SHALL continue to dispatch through the built-in executors.

#### Scenario: User harness dispatched generically
- **WHEN** a run resolves to a harness with a `command` field declared
- **THEN** the relay-runner SHALL invoke the generic executor, passing the resolved model and prompt body for templating

#### Scenario: Built-in harness dispatched conventionally
- **WHEN** a run resolves to a built-in harness (`cc`/`cx`/`ge`/`op`)
- **THEN** the relay-runner SHALL invoke the built-in executor for that harness, regardless of whether `[harness.<name>.models]` was declared
