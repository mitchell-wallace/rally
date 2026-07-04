## ADDED Requirements

### Requirement: Presentation-neutral runtime contract package

The runtime event and operator-control contract SHALL live in a dedicated
stdlib-only child package `internal/relay/runner/runtimeevent` containing the
event model (typed data-only payloads behind a small `Event` surface), the
synchronous `Sink` interface (`Emit(context.Context, Event)`), the
operator-control vocabulary (`OperatorAction`, `Press`, arm/act message
helpers, the confirm window), and the `ControlSource` interface
(`Start`/`Stop` per control session plus `WaitResume` for the pause path).
Event payloads SHALL carry data only — no ANSI escape sequences and no
`lipgloss`-styled strings. `runtimeevent` SHALL import only the Go standard
library and SHALL NOT import `internal/relay/runner` or any other internal
package.

#### Scenario: Contract package is a stdlib-only leaf

- **WHEN** `go list -deps ./internal/relay/runner/runtimeevent` is inspected
- **THEN** it contains no `internal/*` package, and the build has no import
  cycle between `runner` and `runtimeevent`

#### Scenario: Events carry data, not styling

- **WHEN** the `runtimeevent` payload types are reviewed
- **THEN** no payload field contains pre-rendered ANSI or styled text; message
  strings are plain operator vocabulary (e.g. arm/act messages), and all
  styling lives in presentation adapters

### Requirement: Runner emits events instead of rendering

`internal/relay/runner` SHALL emit `runtimeevent` events synchronously, in the
emitting goroutine, at the seams where it renders operator-facing output at
baseline (run header, retry/terminal/cancelled/handoff footers, wait countdown
lifecycle, route and task-file warnings, rate-limit wait notice, pause prompt,
operator action armed/applied, try-start status snapshot and shortcut hint,
relay summary). After this change, runner production files SHALL NOT import
`internal/style` or `internal/keyboard`, SHALL NOT print operator-facing
output directly to `os.Stdout`/`os.Stderr` (no `fmt.Print*`/`Fprint*`
header, footer, warning, countdown, prompt, or summary writes) — the sole
permitted direct-stream use being the documented monitor residual
`mon.Start(os.Stdout)` — and SHALL NOT read `os.Stdin`. A nil `EventSink` SHALL behave as a no-op sink and a nil
`Controls` SHALL mean no operator input, without affecting orchestration. The
live monitor status line (`internal/monitor` usage, including
`mon.Start(os.Stdout)`) SHALL remain runner-driven as a documented residual,
and relay-log lines SHALL remain log-only (not events).

#### Scenario: Runner import set loses presentation packages

- **WHEN** the direct internal imports of `internal/relay/runner` production
  files are inspected after the change
- **THEN** `internal/style` and `internal/keyboard` are absent,
  `internal/relay/runner/runtimeevent` is present, and `internal/monitor`
  remains (status-line residual)

#### Scenario: Event order matches lifecycle on core paths

- **WHEN** a recording sink captures a simple success run, a retry-then-fail
  run, an operator cancellation, a stall, and an all-paused wait
- **THEN** the emitted event sequence matches the pre-change print order for
  that path (header before status snapshot, armed before applied, wait started
  before ticks before finished, footer events at attempt completion, summary
  last), and the terminal sink renders operator armed/applied events only in
  the wait loop — during an active try it no-ops them, because that feedback
  remains monitor-indicator-driven (the documented residual)

#### Scenario: Operator semantics preserved through ControlSource

- **WHEN** the action loop is driven through a `ControlSource` with the
  baseline shortcut semantics (arm then confirm within the confirm window,
  skip/pause/stop/quit, second quit during drain)
- **THEN** flag mutations, cancellation sources, drain behaviour including
  force-kill escalation with late PID delivery, and pause resume-on-Enter
  behave exactly as at baseline, with
  `go test -race -shuffle=on -count=1 ./internal/relay/...` green

### Requirement: Terminal adapter renders byte-identical CLI output

The CLI presentation adapter SHALL live in `internal/presentation/terminal`,
providing a `runtimeevent.Sink` that renders events with `internal/style` to
the writers it is constructed with, and a `runtimeevent.ControlSource` that
wraps `internal/keyboard` (raw-mode session per control phase, press
translation, resume-on-Enter). For identical relay flows, the adapter's output
SHALL be byte-identical to the pre-change runner output, including the
in-place retry-footer redraw, raw-mode `\r\n` countdown behaviour, and
stderr-vs-stdout targeting. The pre-change byte-level output tests SHALL be
relocated to this package and pass unchanged in substance. Production files in
`internal/presentation/terminal` SHALL limit their internal imports to
`internal/relay/runner/runtimeevent`, `internal/style`, and
`internal/keyboard`.

#### Scenario: Byte parity held by relocated tests

- **WHEN** the relocated footer/countdown/hint output tests run against the
  terminal sink
- **THEN** they pass with the same expected bytes as before the change

#### Scenario: Adapter confinement

- **WHEN** the direct internal imports of `internal/presentation/terminal`
  production files are inspected
- **THEN** they are a subset of `internal/relay/runner/runtimeevent`,
  `internal/style`, `internal/keyboard` — never `internal/relay/runner`,
  `internal/harness*`, `internal/config`, `internal/store`, or
  `internal/telemetry`

### Requirement: Presentation wiring flows through the composition root

`runner.Config` SHALL gain `EventSink runtimeevent.Sink` and
`Controls runtimeevent.ControlSource`; `app.RelayStartOptions` SHALL carry the
same two contract values opaquely; and `internal/cli` SHALL construct the
terminal adapter over the process streams and pass it down. `internal/app`
SHALL import `internal/relay/runner/runtimeevent` for the contract types only
and SHALL NOT import `internal/presentation/*`.

#### Scenario: CLI injects, app forwards, runner consumes

- **WHEN** `rally` starts a relay
- **THEN** the terminal sink and control source are constructed in
  `internal/cli`, forwarded by `app.StartRelay` without inspection, and used
  by the runner for all operator-facing output and control input

#### Scenario: App stays presentation-neutral

- **WHEN** `go list -deps ./internal/app` is inspected
- **THEN** it contains `internal/relay/runner/runtimeevent` but no
  `internal/presentation/*` package, `internal/cli`, or `internal/user_prompt`

### Requirement: Presentation boundaries enforced by the guardrail

The `tools/archguard` policy tables SHALL be updated so that:
`internal/relay/runner` may import `relay/runner/runtimeevent` but not
`style`, `keyboard`, or any `presentation/*` package; `internal/app` may
import `relay/runner/runtimeevent` but no `presentation/*` package;
`internal/presentation/terminal` is limited to `relay/runner/runtimeevent`,
`style`, and `keyboard`; and `internal/harness*`, `internal/config`, and
`internal/store` remain unable to import any presentation package. Violations
SHALL hard-fail with an architectural reason. These are policy-table updates
consistent with the existing `architecture-guardrails` spec; that spec is not
modified.

#### Scenario: Runner importing a concrete presenter hard-fails

- **WHEN** a production file in `internal/relay/runner` imports
  `internal/presentation/terminal` (or `internal/style`/`internal/keyboard`)
- **THEN** `archguard --ci` exits non-zero explaining the runner emits
  presentation-neutral events and must not depend on concrete presentation

#### Scenario: Clean post-change graph passes

- **WHEN** `go run ./tools/archguard --ci` runs after the change
- **THEN** it exits 0 with the updated allow-lists and no new grandfather
  entry

### Requirement: Behaviour-preserving boundary introduction

This change SHALL be behaviour-preserving: for identical inputs and operator
actions, CLI output bytes, keyboard shortcut semantics and timings (including
the confirm window and second-quit force-kill during drain), cancellation
ordering, run-budget/try-timeout semantics, telemetry events/fields, store
shape, laps semantics, and agent-authored commit messages SHALL be unchanged.
The change SHALL NOT bump `internal/buildinfo/VERSION` and SHALL NOT require a
release.

#### Scenario: Full suite green with relocations only

- **WHEN** `go test -count=1 ./...` and `go test -race -shuffle=on -count=1
  ./internal/relay/... ./internal/presentation/...` run after the change
- **THEN** both pass; behavioural assertions are unchanged apart from
  relocation and the new recording-sink/event-order coverage

#### Scenario: No behaviour-surface edits

- **WHEN** the diff of the change is reviewed
- **THEN** it contains no telemetry-field, store-shape, laps-semantic,
  config-schema, or commit-message edit and leaves
  `internal/buildinfo/VERSION` untouched
