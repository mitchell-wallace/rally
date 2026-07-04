## Why

`internal/relay/runner` owns both orchestration and terminal presentation.
A 2026-07-02 scan (commit `f55712c`, after **#4 modularize-harness-adapters**)
confirms there is **no** event, observer, or `OnStatus` mechanism anywhere in
production code today, and the coupling is broader than the draft assumed:
`internal/style` is imported by four runner files (`run_one.go`,
`relay_steps.go`, `terminal.go`, `handoff_only.go`), `internal/keyboard` is
constructed directly in two places (`run_one.go`, `terminal.go`), and direct
`os.Stdout`/`os.Stderr` writes appear in `run_one.go`, `relay_steps.go`,
`task.go`, and `terminal.go`. The runner is also the **only** production
importer of `internal/style`.

Rally is expected to grow a terminal UI (`build-new-tui`). If the TUI reaches
into runner internals — or the runner grows TUI imports — the architecture
hardens in the wrong shape. The runner should expose a small, presentation-
neutral event/control boundary; CLI and TUI adapt it to terminal output,
full-screen UI, or tests. This also makes **#6 decompose-run-one** cleaner: it
sequences after this change so the files it splits are orchestration-only.

This is change **#5** in the architecture sequence (`openspec/next-up.md`). It
is **behaviour-preserving**: the CLI's operator-facing output and keyboard
semantics are byte/semantics-identical; no config, telemetry, store, or laps
change; no version bump; no release.

## What Changes

- Add `internal/relay/runner/runtimeevent`: a stdlib-only contract package with
  the runtime event model (data-only payloads — no ANSI, no `lipgloss`), the
  synchronous `Sink` interface, and the operator-control vocabulary
  (`OperatorAction`, `Press`, `ControlSource`, arm/act messages, confirm
  window).
- The runner emits events at its existing step seams (header, footers,
  countdowns, warnings, rate-limit waits, pause, summary, operator
  armed/applied) instead of rendering; emission is synchronous at the exact
  points that print today.
- Add `internal/presentation/terminal`: the CLI adapter — an event sink that
  renders today's output byte-identically via `internal/style`, and a
  `ControlSource` wrapping `internal/keyboard`.
- `internal/cli` constructs the terminal sink and control source and passes
  them through `app.StartRelay` (`RelayStartOptions`) into `runner.Config` as
  opaque interface values; `internal/app` stays presentation-neutral.
- Remove the runner's `internal/style` and `internal/keyboard` imports and its
  direct `os.Stdout`/`os.Stderr` prints (the monitor status-line subsystem
  stays runner-driven for now — see design).
- Relocate the byte-level output tests to the terminal adapter's package; add
  recording-sink tests asserting event order for success/retry/cancel/stall/
  wait paths; action-loop tests drive a fake `ControlSource`.
- Update `tools/archguard` policy tables: runner drops `style`/`keyboard` and
  gains `runtimeevent`; `app` gains `runtimeevent`; new
  `presentation/terminal` allow-list; presentation packages confined away from
  harness/config/store; runner cannot import concrete presentation packages.
- Point the `build-new-tui` stub at the new boundary (consume
  `runtimeevent`, not runner internals).

## Capabilities

### New Capabilities

- `runtime-presentation-boundary`: the presentation-neutral runtime contract —
  the `runtimeevent` package (event model, synchronous sink, operator-control
  vocabulary), the runner's emit-don't-render obligation, the terminal adapter
  with byte-identical CLI output, injection through the composition root, the
  import-boundary rules that keep runner/presentation one-way, and the
  behaviour-preservation / no-version-bump contract.

### Modified Capabilities

- `composition-root-structure`: the "Presentation-neutral relay-start seam"
  requirement is extended — `RelayStartOptions` carries the event sink and
  control source as opaque contract values constructed CLI-side; `internal/app`
  still never imports presentation packages and never reads input.

## Impact

- **Code**: new `internal/relay/runner/runtimeevent` and
  `internal/presentation/terminal` packages; runner files stop rendering
  (style/keyboard imports removed; stdout/stderr writes replaced by emission);
  `internal/cli` wires the adapter; `internal/app` passes opaque values.
  `internal/keyboard` keeps raw-terminal decoding; `internal/monitor` is
  untouched (its file split is #7's).
- **Behaviour**: CLI output byte-identical for the same relay flows (headers,
  footers including in-place retry redraw, countdown frames, warnings,
  summary); keyboard shortcut semantics (arm → confirm within 4s, skip/pause/
  stop/quit, second-quit force-kill during drain) unchanged; cancellation
  ordering unchanged. No config, telemetry, store, laps, or commit-message
  change; `internal/buildinfo/VERSION` untouched; no release.
- **Guardrails**: archguard policy-table updates only (no
  `architecture-guardrails` spec change, per #4 precedent).
- **Sequencing**: before #6 (which then splits orchestration-only files) and
  before #7 (whose `monitor.go` split stands alone). `build-new-tui` and the
  operator-control parts of the future TUI work consume this boundary.
- **Out of scope**: implementing the TUI; changing shortcuts, timings, or
  cancellation behaviour; routing the monitor status line or relay-log lines
  through events (documented residuals); telemetry consuming events (telemetry
  stays an independent runner concern); splitting `monitor.go` (#7) or
  `run_one.go` (#6).
