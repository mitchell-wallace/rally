## MODIFIED Requirements

### Requirement: Runtime event vocabulary uses outings
Runtime events SHALL use presentation-neutral outing vocabulary for the middle execution entity. Event and payload names that currently describe run headers, run indexes, or total run counts SHALL be renamed to outing forms. Footer events may continue to use try/attempt wording where they describe a try result or ordinal retry phrase.

#### Scenario: Outing header event emitted
- **WHEN** an outing starts and the runner emits the header event
- **THEN** the event SHALL use an outing-named kind and payload
- **AND** the payload SHALL carry outing index and total outings, not run index and total runs

#### Scenario: Footer events preserve try semantics
- **WHEN** a retry or terminal try footer is emitted
- **THEN** the event SHALL preserve try lifecycle semantics
- **AND** operator ordinal wording MAY remain "attempt N" where it describes display phrasing rather than the entity name

### Requirement: Presentation packages render outing labels
Presentation adapters SHALL render operator-facing labels using "outing" for the middle execution entity, including headers, summaries, replay views, and TUI feed fallbacks. Prototype TUI packages in flux SHALL be re-inventoried before implementation and only surviving packages SHALL receive durable API names.

#### Scenario: Header renders outing counter
- **WHEN** a non-laps header is rendered
- **THEN** it SHALL display `outing: X/Y` rather than `run: X/Y`

#### Scenario: Summary renders outing counts
- **WHEN** a relay summary is rendered
- **THEN** it SHALL count outings rather than runs in operator-facing text
