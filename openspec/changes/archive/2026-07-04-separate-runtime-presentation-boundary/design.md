# Design — separate-runtime-presentation-boundary

Behaviour-preserving introduction of a presentation-neutral runtime boundary in
`internal/relay/runner`. Byte-identical CLI output, identical keyboard/
cancellation semantics; no config, telemetry, store, or laps change; no version
bump; no release. Baselined against commit `f55712c` (2026-07-02) — regenerate
the wiring inventory at implementation time.

## Context

The runner renders and reads the terminal directly:

- `internal/style` imports in `run_one.go` (header, rate-limit notice,
  shortcut hint), `terminal.go` (footers, countdown), `relay_steps.go`
  (summary), `handoff_only.go` (footer); the runner is the only production
  importer of `internal/style`.
- `keyboard.NewKeyboard(os.Stdin, os.Stdout)` constructed in two places:
  `terminal.go:48` (wait countdown) and `run_one.go:497` (active try), each
  with its own raw-mode Start/Stop cycle.
- Direct `os.Stdout`/`os.Stderr` writes in `run_one.go`, `relay_steps.go`,
  `task.go`, `terminal.go`.
- Output seam today: unexported `Runner.out` (`outWriter()` defaults to
  `os.Stdout`); `runner.Config` carries no writer. `app.StartRelay` receives
  `Out`/`Err` (`RelayStartOptions`) but only prints "Relay complete." with it.
- `runActionLoop` (`action_loop.go`) consumes `<-chan keyboard.Press` via
  `actionLoopDeps` and mutates monitor indicators, skip/stop flags, and
  cancellation source; second-quit force-kill happens during drain
  (`drainOperatorCancellation`), with late `pidCh` delivery load-bearing for
  force-kill targeting.
- The monitor is a live status-line subsystem: `mon.Start(os.Stdout)`
  background-renders with cursor movement; `renderRunFooter` interim mode
  parks the cursor and writes no newline; `mon.Stop()` clears reserved lines
  before footers. `waitLoop` writes `\r\n` because raw mode stair-steps bare
  `\n`.
- Pause blocks on `bufio.NewReader(os.Stdin).ReadString('\n')`
  ("Paused — press Enter to resume", `run_one.go:1339`).
- `app.StartRelay` has an independent OS-signal double-Ctrl+C path
  (`relay_start.go:172`) calling `r.RequestStop()` — separate from the
  keyboard path.

No event/observer/`OnStatus` mechanism exists anywhere in production today.

## Goals / Non-Goals

**Goals:**

- A small, presentation-neutral event/control contract the CLI today — and the
  TUI later — can adapt, owned next to the runner.
- Runner emits data; adapters render. `internal/style` and `internal/keyboard`
  leave the runner's import set.
- Byte-identical CLI output and identical operator semantics, held by
  relocated byte-level tests plus new recording-sink tests.
- Guardrails that keep runner ↛ concrete-presentation and
  presentation ↛ harness/config/store one-way.

**Non-Goals:**

- No TUI implementation (`build-new-tui`).
- No shortcut, timing, cancellation, or output-content change.
- No monitor rewrite or `monitor.go` file split (#7); the status-line
  subsystem stays runner-driven (Decision 8).
- No telemetry-through-events (telemetry stays an independent concern).
- No relay-log format change (log lines are not events in this change).

## Decisions

### Decision 1 — one stdlib-only contract child package: `internal/relay/runner/runtimeevent`

Resolves the draft's placement question. Events and operator-control types
live together in one child package (`package runtimeevent`) because they are
two halves of one boundary — a TUI consumes events and produces controls. A
child of `runner` (rather than a top-level `internal/runtimeapi`) keeps
ownership with the orchestrator that emits them, mirroring how
`relay-module-structure` owns runner structure. The package imports **only the
standard library**, so any presentation package may import it without
archguard ceremony and no import cycle is possible (`runtimeevent` never
imports `runner`).

Alternative considered — types directly in `package runner`: rejected; concrete
presentation adapters would then import the whole runner surface, exactly the
coupling this change exists to prevent.

### Decision 2 — event model: data-only structs, one `Sink`, synchronous delivery

```go
// package runtimeevent
type Sink interface {
    Emit(context.Context, Event)
}
```

- `Event` is a small interface (kind discriminator) over concrete typed
  payload structs; payloads carry **data** (names, durations, counts, message
  strings) — never ANSI sequences or `lipgloss` values.
- **Synchronous, unbuffered, same-goroutine** delivery at the exact call sites
  that print today. This is what makes byte-parity achievable: the terminal
  sink writes the same bytes to the same writer in the same order as the
  current inline prints (the cursor-parking and status-line interleaving
  contracts survive because nothing reorders). Sinks must be fast and must not
  block; the contract documents this. Alternatives — buffered channel or
  goroutine fan-out: rejected; both reorder output relative to the monitor's
  background renders and break the footer-parking contract.
- Resolves the draft's `EventSink` vs callback-struct question in favour of
  the single interface (the draft's own lean).
- A nil sink means no event-rendered output: `runner.Config.EventSink == nil`
  → no-op sink. This suppresses only what the sink renders — the monitor
  status line (`mon.Start(os.Stdout)`, Decision 8) remains a direct-stdout
  residual for any caller that reaches an active try, until the TUI/monitor
  follow-up rehomes it. The CLI always injects the terminal sink, so operator
  behaviour is unchanged; the no-op default applies only to direct
  library/test construction (the affected runner tests are updated as part of
  this change).

### Decision 3 — event vocabulary tracks today's operator-facing output

The initial vocabulary is derived from the output inventory (regenerate at
implementation time). Most events map one-to-one to output the runner
currently prints; the exceptions are explicitly marked data-only below (the
terminal sink no-ops them; they exist for alternate presentations):

| Event | Replaces (today) |
|---|---|
| `RelayStarted` / `RelayCompleted` | **no current print** — data-only lifecycle markers for alternate presentations (TUI); the terminal sink SHALL no-op them, and `app.StartRelay` remains the sole owner of the literal `Relay complete.` line (`relay_start.go:201`) |
| `RouteWarning` | route/selection warnings to stderr (`relay_steps.go:57,170`) |
| `TaskFileWarning` | laps-instructions / free-run prompt warnings (`task.go:62,79`) |
| `RunHeaderReady` | `style.RenderHeader` (`run_one.go:456`) |
| `TryStatusSnapshot` + `ShortcutHintReady` | initial `mon.Tick()` print + `style.ShortcutHint()` (`run_one.go:514–524`) |
| `RetryFooterUpdated` | interim `renderRunFooter` (in-place redraw) |
| `AttemptFinished` | terminal pass/fail footer (`run_one.go:1084`) |
| `AttemptCancelled` | cancelled footer (`run_one.go:735`) |
| `HandoffAttemptFinished` | handoff-only footer (`handoff_only.go:217`) |
| `RateLimitWaitStarted` | dim rate-limit notice (`run_one.go:1014`) |
| `WaitStarted` / `WaitTick` / `WaitFinished` | countdown frames + clear (`terminal.go`) |
| `OperatorActionArmed` / `OperatorActionApplied` | **wait-loop armed hint only** (a real print, `terminal.go:87`); during an active try, arm/act feedback flows through the monitor indicators (`mon.SetArmed`/`mon.SetActing` in `action_loop.go`), which stay runner-driven (Decision 8) — the terminal sink SHALL no-op these events for active tries (they exist as data for alternate presentations) |
| `PausePromptShown` | "Paused — press Enter to resume" (`run_one.go:1339`) |
| `RelaySummaryReady` | `style.RenderSummary` (`relay_steps.go:481`) |

Relay-log-only lines (route fallback, benching, attempt results, …) are **not**
events in this change; they already have a durable home in the relay log and a
TUI can be given them later by extending the vocabulary (additive change).

### Decision 4 — operator-control contract: `ControlSource` with per-phase sessions

```go
// package runtimeevent
type OperatorAction int // Quit, Skip, Pause, Stop (None excluded from Press)

type Press struct {
    Action    OperatorAction
    Confirmed bool
}

type ControlSource interface {
    Start(ctx context.Context) (<-chan Press, error) // begin a control session
    Stop()                                           // end it (restore terminal state)
    WaitResume(ctx context.Context) error            // block until operator resumes (pause path)
}
```

- **Per-phase sessions** mirror today's two independent keyboard instances
  (wait countdown; active try): the runner calls `Start`/`Stop` around each
  phase, preserving the raw-mode enter/leave points and the no-leaked-reader
  property the keyboard tests pin.
- `WaitResume` absorbs the pause path's blocking
  `bufio.NewReader(os.Stdin).ReadString('\n')` so the runner stops reading
  stdin directly; the terminal implementation performs exactly today's read
  (after leaving raw mode, as today). Alternative — leave the stdin read in
  the runner: rejected; it is the only remaining direct input read and moving
  it is mechanical.
- The **operator vocabulary** (`OperatorAction`, `ArmMessage(action)`,
  `ActMessage(action)`, `ConfirmWindow = 4s`) moves to `runtimeevent`:
  `runActionLoop` needs arm/act text for monitor indicators and the wait loop
  needs it for armed hints, and after this change the runner may not import
  `keyboard`. `internal/keyboard` keeps raw byte decoding, double-press
  confirmation, and its own `Press` type; the terminal adapter translates
  `keyboard.Press` → `runtimeevent.Press`. Keyboard stays an internal leaf
  package (imports nothing internal), so no archguard row is added for it.
- `runActionLoop` keeps its exact select structure, flag mutations,
  drain-with-second-quit escalation, and late-PID force-kill targeting — only
  the channel element type changes and the monitor indicator text now comes
  from `runtimeevent` message helpers. The action-loop tests switch their fake
  channels to `runtimeevent.Press`.
- The app-level OS-signal path (`relay_start.go:172`) is untouched: it is an
  OS concern, not a presentation one, and already flows through the exported
  `RequestStop` control.

### Decision 5 — terminal adapter package: `internal/presentation/terminal`

One new package owns both CLI-side implementations:

- `terminal.Sink` (implements `runtimeevent.Sink`): renders each event with
  `internal/style` to the out/err writers it is constructed with — the same
  strings, escape sequences (`\r\x1b[2K`, `\r\x1b[J`, `\r\n` in raw mode), and
  writer targets as today, call-for-call.
- `terminal.Controls` (implements `runtimeevent.ControlSource`): constructed
  over injectable streams (`terminal.NewControls(in io.Reader, out io.Writer)`;
  the CLI passes `os.Stdin`/`os.Stdout`), wraps `internal/keyboard`
  construction and raw mode, translates presses, implements `WaitResume` as
  today's Enter read against `in` — pipe-testable without global stdin
  mutation.

Placement rationale: `internal/cli` stays the Cobra command layer;
`internal/presentation/<surface>` is the durable home the draft anticipated
(`internal/presentation/terminal` now, `internal/tui` or
`internal/presentation/tui` later). Alternative — files inside `internal/cli`:
rejected; cli is broad-allowed by archguard, which would leave the new
adapter's dependencies unconstrained, and the TUI would have no sibling
convention to follow.

### Decision 6 — injection through the composition root

- `runner.Config` gains `EventSink runtimeevent.Sink` and
  `Controls runtimeevent.ControlSource` (nil-safe: no-op sink; nil controls =
  no operator input, as in headless tests).
- `app.RelayStartOptions` gains the same two fields as **opaque contract
  values**; `internal/cli` constructs `terminal.Sink`/`terminal.Controls`
  (over the same `os.Stdout`/`os.Stderr` it already passes) and
  `app.StartRelay` forwards them into `runner.Config` untouched. `internal/app`
  imports `runtimeevent` (types only), never `internal/presentation/*` — the
  seam stays presentation-neutral and never reads input.
- The existing `Runner.out`/`outWriter()` seam is subsumed: rendering happens
  in the sink, constructed over the writers. `Runner.out` disappears with the
  last direct print (tests that set it move to the adapter package or use a
  recording sink).

### Decision 7 — guardrail updates (policy tables only; no archguard spec change)

- `relay/runner` allow-list: **add** `relay/runner/runtimeevent`; **remove**
  `style` and `keyboard` (keep `monitor` — Decision 8).
- `app` allow-list: **add** `relay/runner/runtimeevent`.
- New row `presentation/terminal`: may import `relay/runner/runtimeevent`,
  `style`, `keyboard`, and nothing else internal (notably not `relay/runner`,
  `harness*`, `config`, `store`, `telemetry`).
- `runtimeevent` needs no row (stdlib-only; absence = leaf, enforced).
- Denial intent made explicit in diagnostics: the runner must not import
  concrete presentation packages (`presentation/*`, future `tui`), and
  presentation packages must not import runner internals, harness, config, or
  store. `internal/cli` remains broad-allowed and simply imports the adapter.
- Consistent with the existing `architecture-guardrails` spec (tight per-
  package allow-lists); policy-table update only, same precedent as #4/#6.

### Decision 8 — the monitor status line stays runner-driven (documented residual)

The live status line (`mon.Start(os.Stdout)`, background render/clear with
cursor movement) is a self-contained subsystem whose interleaving contract
with footers is the most fragile thing in this area. Routing its frames
through the sink would mean re-plumbing the cursor-reservation protocol for no
current consumer — the CLI renders it fine today and the TUI will replace the
status line wholesale, not adapt it. So: the runner keeps its
`internal/monitor` import and `mon.Start(os.Stdout)`; the monitor's
sampling-vs-rendering *file* split is #7's; routing status frames through the
presentation boundary is deferred to `build-new-tui` (which owns the replace-
don't-adapt decision). The `TryStatusSnapshot` event (initial status print)
still gives sinks the try-start status data.

Second acknowledged residual: relay-log lines (Decision 3). Both residuals and
their triggers are recorded here deliberately so review passes do not
re-litigate them as omissions.

## Risks / Trade-offs

- **[Risk] Byte-parity drift in the sink rendering** → move each print into
  the sink **call-for-call** (one emit per former print site, same format
  strings relocated verbatim); relocate the existing byte-level tests
  (`terminal_test.go`, footer-cadence tests) to
  `internal/presentation/terminal` unchanged; run them against the sink.
- **[Risk] Cancellation-timing regression in the action loop** → the loop's
  select structure, drain semantics, and PID handling are not restructured —
  only the press element type changes; keep
  `go test -race -shuffle=on -count=1 ./internal/relay/...` green per phase;
  the seven action-loop tests plus timeout tests are the gate.
- **[Risk] Raw-mode/session lifecycle leaks with `ControlSource`** (two
  sessions per run today) → per-phase `Start`/`Stop` mirrors current
  construction exactly; keyboard's no-leaked-reader tests keep pinning the
  underlying implementation; add an adapter test for repeated Start/Stop
  cycles.
- **[Risk] Event emission crosses the run-budget channel boundary and resets
  timing semantics** (`setupRunBudget` same-deadline guard, budget channel
  built once before retries) → emissions are inserted at print sites only;
  never move code across `setupRunBudget`/`runBudgetCh` construction.
- **[Risk] `WaitResume` subtly changes pause semantics** (raw-mode exit order
  before the Enter read) → terminal implementation performs the identical
  sequence (leave raw mode via `Stop`, then read line), covered by an adapter
  test.
- **[Trade-off] A silent runner when no sink is injected** — accepted: the CLI
  always injects; library users opt in; tests get the recording sink.
- **[Trade-off] Event vocabulary sized to today's output** — accepted: additive
  extension is cheap; a speculative TUI-complete vocabulary would be guessed
  wrong.

## Migration Plan

Phased, tree green at each step (parallel-emit before cut-over):

1. Add `runtimeevent` (events + control vocabulary + no-op/recording sinks).
2. Plumb `EventSink`/`Controls` through `cli → app.RelayStartOptions →
   runner.Config` (nil-tolerant; no behaviour change yet).
3. Emit events alongside existing prints; add recording-sink order tests for
   success / retry-failure / cancellation / stall / wait paths.
4. Add `internal/presentation/terminal` sink; switch runner prints to
   sink-rendered output print-site-by-print-site; relocate byte-level tests;
   remove `style` import and direct stdout/stderr writes from runner.
5. Introduce `ControlSource`; convert wait-loop and active-try keyboard
   sessions and the pause read; switch action-loop tests to
   `runtimeevent.Press`; remove the runner's `keyboard` import.
6. Update archguard policy tables (Decision 7); `--report`/`--ci` clean.
7. Touch up `build-new-tui` stub to name the boundary it will consume.

Rollback: revert the cut-over commits; the parallel-emit steps are inert.

## Open Questions

_None — the draft's four open questions are resolved: child package
(Decision 1); synchronous delivery (Decision 2); telemetry stays independent
(Non-Goals); parity is held by relocating the existing byte-level tests before
cut-over rather than adding new golden coverage (Risk 1)._
