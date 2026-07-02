## MODIFIED Requirements

### Requirement: Presentation-neutral relay-start seam

Relay startup SHALL be reachable through a reusable app-layer function
`app.StartRelay(ctx, app.RelayStartOptions) error` that turns already-resolved
inputs into a configured `runner.Runner` and runs it. `internal/app` SHALL NOT
import `internal/user_prompt` or `internal/laps`: all interactive start-of-run
decisions (resume-vs-new, keep-vs-overwrite mix, and interactive route
validation) SHALL be resolved in `internal/cli` and passed to `app.StartRelay` as
concrete values, and laps hook installation SHALL remain CLI-side before
`app.StartRelay`. The app layer MAY write progress to caller-supplied `Out`/`Err`
writers but SHALL NOT read input to branch on it. A non-mutating
`app.InspectResume(workspaceDir) (app.ResumeInfo, error)` SHALL expose the
unfinished-relay summary the CLI needs to prompt without itself opening the store
for runtime mutation; it SHALL use the same store initialization/layout migration
path as startup so legacy state is visible before the prompt.

#### Scenario: App seam does not depend on interactive prompting

- **WHEN** `go list -deps ./internal/app` is inspected after the change
- **THEN** the direct imports of `internal/app` include neither
  `internal/user_prompt` nor `internal/laps`, `go list -deps ./internal/app`
  completes without an import cycle, and `app.StartRelay` performs no stdin
  prompting

#### Scenario: Interactive decisions resolved CLI-side

- **WHEN** the `start` command runs with an unfinished relay present and no
  `--resume`/`--new` flag
- **THEN** the resume-vs-new (and any keep-vs-overwrite-mix) prompt is issued from
  `internal/cli`, and `app.StartRelay` receives the resolved decision as a field
  on `RelayStartOptions`, with identical observable behaviour to the pre-change
  `runRelay`

#### Scenario: Resume inspection uses existing store migration

- **WHEN** `app.InspectResume` checks a workspace that still has legacy top-level
  `.rally/relays.jsonl` state
- **THEN** it uses the same store initialization/migration path as relay startup,
  sees the unfinished relay, and returns the prompt summary without completing the
  relay or resetting agent status

#### Scenario: Laps hooks stay outside the app seam

- **WHEN** a laps-backed `start` command runs and hook files need installing
- **THEN** `internal/cli` performs the existing `laps.InstallHooks` and setup-file
  auto-commit before calling `app.StartRelay`, while `app.StartRelay` only
  receives the already-resolved `LapsEnabled` value for runner configuration

#### Scenario: New-start decisions preserve current reset semantics

- **WHEN** the user passes `--new`
- **THEN** the unfinished relay is completed and agent status is reset as before
- **WHEN** the user chooses "Start new" from the interactive unfinished-relay
  prompt
- **THEN** the unfinished relay is completed but agent status is not reset,
  matching the pre-change `runRelay` behavior

#### Scenario: Executor registry feeds the runner from the composition root

- **WHEN** the built-in and generic executors are constructed
- **THEN** they are built by
  `app.BuildExecutors(cfg) map[string]harnessapi.Executor`, via the thin
  `config.V2Config → harness.Config → harness.BuildExecutors` mapper, and passed
  to `runner.NewRunner`, with the same harness set and configuration as the
  pre-change inline map
