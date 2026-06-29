## Draft: Separate Runtime Presentation Boundary

Status: drafted 2026-06-29 - initial architecture concept only.

This change prepares Rally's runtime for multiple presentation surfaces,
including the active `build-new-tui` change, without implementing the TUI itself.

## Why

`internal/relay` currently owns both orchestration and terminal presentation:

- run headers and footers via `internal/style`,
- wait countdown rendering,
- keyboard shortcut handling,
- monitor lifecycle and status-line control,
- direct stdout/stderr writes,
- relay summary rendering.

That is acceptable for a CLI-only tool, but Rally is expected to grow a terminal
UI. If the TUI reaches directly into relay internals, or if relay gains TUI
imports, the architecture will become harder to evolve. Relay should expose a
small runtime event/control boundary. CLI and TUI presentation layers should
adapt that boundary to terminal output, full-screen UI, or tests.

The existing `build-new-tui` proposal already anticipates `OnStatus` callbacks.
This draft scopes the prerequisite architecture so that TUI work has a clean
place to attach.

## Intent

Separate core runtime from presentation concerns:

- Relay emits structured events about relay/run/try lifecycle.
- Presentation adapters render those events.
- Operator controls enter through a small control interface or channel.
- The existing CLI output remains the default adapter.
- TUI code can live under a future presentation module without importing relay
  helper internals.

The desired direction:

```text
cmd/rally or TUI entry
        |
        v
presentation adapter  <---->  runtime control/events
        |                              |
        v                              v
terminal/TUI widgets           internal/relay.Runner
```

## Candidate Work

### A. Define a small runtime event model

Add a narrow event type owned by relay or a small runtime package.

Candidate events:

- relay started,
- relay completed,
- route warning,
- run selected,
- try started,
- try status updated,
- try stalled,
- operator action armed,
- operator action applied,
- try completed,
- run completed,
- wait started/updated/completed,
- summary ready.

Do not encode UI styling in these events. Events should carry data, not
terminal escape sequences or `lipgloss` strings.

### B. Introduce a presenter or observer interface

Possible shape:

```go
type EventSink interface {
    Emit(context.Context, RuntimeEvent)
}
```

Alternative shape:

```go
type Observer struct {
    OnRelayStart func(RelayStartEvent)
    OnTryStatus func(TryStatusEvent)
    OnRunFinish func(RunFinishEvent)
}
```

Recommended lean: one `EventSink` interface first. Callback structs become
wide quickly and make optional events awkward.

The default sink can render the current CLI output. Tests can use a recording
sink.

### C. Separate operator controls from keyboard implementation

Relay should not need to know whether an operator action came from raw keyboard
input, a TUI key binding, or a test.

Candidate shape:

```go
type ControlSource interface {
    Actions(context.Context) <-chan OperatorAction
}
```

The current `keyboard` package can become the CLI implementation of that
interface. TUI can produce the same actions from its event loop.

This should be staged carefully because cancellation timing is correctness
sensitive.

### D. Keep `monitor` data separate from monitor rendering

The current monitor package appears to combine polling/status calculation and
terminal rendering. For TUI readiness, separate:

- status sampling, such as runtime, git stats, log activity, stall state,
- rendering of that status as a CLI line.

Possible packages:

- `internal/runstatus`: presentation-agnostic sampling,
- `internal/presentation/terminal`: current CLI rendering,
- future `internal/tui`: full-screen rendering.

This can start by adapting `monitor.Monitor` behind an interface rather than
rewriting it immediately.

### E. Move style usage out of relay over time

After events exist, `internal/relay` should not need direct imports of
`internal/style` for headers, footers, summaries, or shortcut hints. The CLI
presentation adapter should own style rendering.

This can be gradual:

1. Keep existing style calls while emitting parallel events.
2. Add a CLI event sink that renders the same output.
3. Switch relay to sink-driven rendering.
4. Remove direct style imports from relay.

### F. Add guardrails for presentation boundaries

Once presentation packages exist, update architecture guardrails so:

- harness packages cannot import presentation packages,
- config and store cannot import presentation packages,
- relay can import only the presentation-neutral event/control API,
- presentation packages may import relay API types only through deliberate
  public event/control interfaces.

## Testing Strategy

This change touches operator interaction and output, so tests should be layered.

For event emission:

- Add unit tests with a recording event sink for representative relay/run/try
  flows.
- Assert event order for simple success, retry failure, cancellation, stall, and
  wait paths.
- Avoid asserting terminal styling in runtime event tests.

For CLI presentation parity:

- Keep existing output tests green.
- Add golden-ish tests at the rendering adapter level if useful, but avoid
  brittle snapshots of ANSI escapes.
- Preserve current keyboard shortcut semantics with action-loop tests.

For control abstraction:

- Drive `runActionLoop` with fake `ControlSource` actions.
- Keep timeout, skip, pause, graceful stop, and quit-now escalation tests.
- Run `go test -race -shuffle=on -count=1 ./internal/relay` after action-loop
  or channel changes.

Before completion:

- Run `go test -count=1 ./...`.
- Run targeted CLI tests under `cmd/rally` if output or signal wiring changed.

## Sequencing

1. Complete or start `decompose-relay-runner` so terminal/action-loop code is
   already isolated.
2. Add event sink types and a no-op/default sink without removing existing
   output.
3. Add recording-sink tests around high-value lifecycle paths.
4. Move CLI rendering behind an event sink.
5. Introduce control source abstraction for keyboard/TUI/test actions.
6. Update `build-new-tui` design to consume the new boundary instead of direct
   relay internals.
7. Add architecture guardrail rules for presentation modules.

## Open Questions

- Should events live in `internal/relay`, `internal/runtime`, or a small
  `internal/runstream` package?
- Should event delivery be synchronous, buffered, or best-effort?
- Should telemetry consume the same runtime events later, or should telemetry
  remain an independent relay concern for now?
- How much CLI output parity should be tested directly before switching to an
  event sink?

## Out of Scope

- Implementing the full TUI.
- Changing keyboard shortcuts or cancellation behaviour.
- Changing telemetry payloads.
- Rewriting monitor internals unless needed to define the boundary.
