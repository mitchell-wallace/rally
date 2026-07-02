// Package runtimeevent defines the presentation-neutral contract for the
// runtime events the relay runner emits and the operator controls it consumes.
//
// It is the boundary both halves of any presentation surface consume: the CLI
// terminal adapter today, and a future TUI. Events flow out of the runner as
// data-only payloads delivered to a [Sink]; operator controls flow in as [Press]
// values delivered from a [ControlSource].
//
// The package imports only the Go standard library. It must never import the
// runner (that would form a cycle) nor any concrete presentation, style, or
// keyboard package — keeping the boundary dependency-free is what lets any
// presentation import it without ceremony. The absence of such imports is the
// structural guarantee; there is nothing here to remove.
//
// # Delivery contract
//
// [Sink.Emit] is synchronous, unbuffered, and called inline from the emitting
// goroutine at the exact call site that renders output today. This is what
// makes byte-for-byte output parity achievable: a terminal sink writes the same
// bytes to the same writer in the same order as the current inline prints, with
// no reordering relative to the monitor's background renders. Sinks MUST be
// fast and MUST NOT block the emitting goroutine, which runs the relay's control
// loop (retries, waits, and operator-input handling).
//
// A nil runner sink means no event-rendered output: callers treat a nil [Sink]
// as [NoopSink]. The monitor status line is a separate, runner-driven residual
// that is intentionally not routed through this boundary.
//
// # Payloads carry data, not presentation
//
// Event payloads carry data only — names, durations, counts, and message
// strings. They never carry ANSI escape sequences or lipgloss style values; a
// presentation adapter renders the bytes. Message strings are the canonical
// operator-facing text (e.g. a warning line) so that parity is held by carrying
// the text rather than the formatting.
package runtimeevent
