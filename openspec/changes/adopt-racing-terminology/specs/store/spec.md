## MODIFIED Requirements

### Requirement: Try history outing identity
The system SHALL persist each try with the outing identity it belongs to. New `.rally/tries.jsonl` records SHALL use `outing_id`. Readers SHALL accept legacy `run_id` and populate the outing identity from it. If both keys exist and differ, readers SHALL return a parse error rather than silently breaking correlation.

#### Scenario: New try records use outing_id
- **WHEN** a try record is appended
- **THEN** the written JSON SHALL contain `outing_id`
- **AND** it SHALL NOT write `run_id`

#### Scenario: Legacy try records still load
- **WHEN** a try history line contains `run_id` and no `outing_id`
- **THEN** the system SHALL load the value as the try's outing identity

#### Scenario: Conflicting try identity keys fail
- **WHEN** a try history line contains both `outing_id` and `run_id` with different values
- **THEN** loading SHALL fail with a parse error naming the conflicting keys

### Requirement: Message consumption outing identity
The system SHALL associate run-scoped inbox/message consumption with outings. New message records SHALL use `consumed_by_outing_id`. Readers SHALL accept legacy `consumed_by_run_id` and reject conflicting old/new values.

#### Scenario: New consumed message records use outing key
- **WHEN** a message is consumed by the current outing
- **THEN** the persisted message record SHALL write `consumed_by_outing_id`

#### Scenario: Legacy consumed message records still load
- **WHEN** a message record contains `consumed_by_run_id` and no `consumed_by_outing_id`
- **THEN** the system SHALL load it as the consumed outing ID
