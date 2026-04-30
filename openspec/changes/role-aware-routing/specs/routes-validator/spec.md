## ADDED Requirements

### Requirement: `rally routes check` subcommand
The system SHALL provide a `rally routes check` subcommand that parses `[routes]` from `.rally/config.toml`, resolves all shortcut keys via `[providers]`, verifies quota syntax (positive integers, `min <= max`), and reports the result. The subcommand SHALL exit zero on a clean config and non-zero on any parse, resolution, or validation error. It SHALL list declared routes that no current bead's `assignee` references as informational output (not errors).

#### Scenario: Clean config validates
- **WHEN** `[routes]` parses cleanly, all shortcut references resolve, all quotas are well-formed, and all roles referenced by current beads have matching routes
- **THEN** `rally routes check` SHALL exit zero and print a summary of declared routes and their entry counts

#### Scenario: Unresolved shortcut surfaces with did-you-mean
- **WHEN** `[routes].SENIOR = ["op:typo"]` and `op:typo` is not defined in `[providers]`
- **THEN** `rally routes check` SHALL exit non-zero with an error naming the offending entry and listing closest-matching defined shortcut keys

#### Scenario: Bad quota
- **WHEN** an entry's quota is `5-3` (min > max), `0`, or negative
- **THEN** `rally routes check` SHALL exit non-zero with a clear error message naming the offending entry

#### Scenario: Unreachable routes flagged as info
- **WHEN** `[routes]` declares a `MARKETING` role but no current bead carries `assignee: MARKETING`
- **THEN** `rally routes check` SHALL emit an info-level message indicating the route is declared but not currently referenced by any bead; this SHALL NOT cause non-zero exit

#### Scenario: Missing default with non-default routes warned
- **WHEN** `[routes]` declares non-default routes but no `default` route
- **THEN** `rally routes check` SHALL emit a warning indicating that beads with no matching role would cause exit at run-time; this SHALL NOT by itself cause non-zero exit (it's a config style warning, not an error)

### Requirement: Startup-time route validation
The system SHALL run the same validation logic during `rally relay` startup. Validation outcomes SHALL be:

- **Invalid `--agent` syntax** → error, exit
- **Quota out of bounds** anywhere in `[routes]` or `--agent` → error, exit
- **Numeric-only shortcut key in `[providers]`** → error, exit (already enforced by v0.5.0)
- **Duplicate `[routes]` keys differing only in case** → error, exit
- **Invalid syntax in some `[routes]` entries while others parse cleanly** → warn and prompt the operator (`Continue anyway? Invalid roles will fall back to DEFAULT (y/N)`); on `y` proceed with valid routes only; on `n` or stdin EOF exit non-zero
- **`default` route invalid or missing AND `[routes]` is otherwise non-empty** → same warn-and-prompt as above; if no beads exist in queue, warn-and-exit instead of prompting

#### Scenario: Quota out of bounds at startup
- **WHEN** `rally relay` starts with `[routes].SENIOR = ["claude:opus-4.7:5-3"]`
- **THEN** rally SHALL exit non-zero before starting any iteration, with an error naming the offending entry

#### Scenario: Partial-failure prompt with confirm
- **WHEN** `rally relay` starts and `[routes]` has one valid role and one syntactically-broken role; `default` is valid
- **THEN** rally SHALL warn naming the broken role, prompt y/N; on `y` proceed with the broken role silently falling back to `default`; on `n` or EOF exit non-zero

#### Scenario: No default and no beads
- **WHEN** `rally relay` starts with `[routes]` declaring only non-default roles (no `default`) and the bead queue is empty
- **THEN** rally SHALL warn-and-exit (no prompt, no relay started) since there is nothing to run anyway
