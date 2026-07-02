package runtimeevent

import (
	"context"
	"sync"
)

// Kind identifies the kind of an [Event]. It lets a sink dispatch on the event
// type via a switch on the discriminator and lets tests assert ordering by kind
// without inspecting payload fields. Every concrete payload type returns its own
// Kind from [Event.Kind].
type Kind int

const (
	// Lifecycle markers (data-only; a terminal sink no-ops these — they exist for
	// alternate presentations such as a future TUI).
	KindRelayStarted Kind = iota
	KindRelayCompleted

	// Operator-facing output the runner prints today.
	KindRouteWarning
	KindTaskFileWarning
	KindRunHeaderReady
	KindTryStatusSnapshot
	KindShortcutHintReady
	KindRetryFooterUpdated
	KindAttemptFinished
	KindAttemptCancelled
	KindHandoffAttemptFinished
	KindRateLimitWaitStarted
	KindWaitStarted
	KindWaitTick
	KindWaitFinished
	KindOperatorActionArmed
	KindOperatorActionApplied
	KindPausePromptShown
	KindRelaySummaryReady
)

// String renders k as its constant name. It is intended for test failure
// messages and diagnostics, not for presentation to operators.
func (k Kind) String() string {
	switch k {
	case KindRelayStarted:
		return "RelayStarted"
	case KindRelayCompleted:
		return "RelayCompleted"
	case KindRouteWarning:
		return "RouteWarning"
	case KindTaskFileWarning:
		return "TaskFileWarning"
	case KindRunHeaderReady:
		return "RunHeaderReady"
	case KindTryStatusSnapshot:
		return "TryStatusSnapshot"
	case KindShortcutHintReady:
		return "ShortcutHintReady"
	case KindRetryFooterUpdated:
		return "RetryFooterUpdated"
	case KindAttemptFinished:
		return "AttemptFinished"
	case KindAttemptCancelled:
		return "AttemptCancelled"
	case KindHandoffAttemptFinished:
		return "HandoffAttemptFinished"
	case KindRateLimitWaitStarted:
		return "RateLimitWaitStarted"
	case KindWaitStarted:
		return "WaitStarted"
	case KindWaitTick:
		return "WaitTick"
	case KindWaitFinished:
		return "WaitFinished"
	case KindOperatorActionArmed:
		return "OperatorActionArmed"
	case KindOperatorActionApplied:
		return "OperatorActionApplied"
	case KindPausePromptShown:
		return "PausePromptShown"
	case KindRelaySummaryReady:
		return "RelaySummaryReady"
	default:
		return "Unknown"
	}
}

// Event is a presentation-neutral runtime event emitted by the runner at the
// exact call sites that render operator-facing output today. Each concrete
// payload type implements Event by returning its [Kind]; payloads carry data
// only and never ANSI escape sequences or lipgloss styles.
//
// Consumers dispatch either on the [Event.Kind] discriminator or via a Go type
// switch on the concrete payload type. Both are supported; the discriminator is
// kept so a sink need not perform a type assertion to record ordering.
type Event interface {
	Kind() Kind
}

// Sink receives runtime events. Delivery is synchronous and unbuffered: Emit is
// called inline at each emit site in the emitting goroutine and must return
// before the caller proceeds. A sink MUST be fast and MUST NOT block the
// emitting goroutine (which runs the relay's control loop); a slow or blocking
// sink would stall retries, waits, and operator-input handling. A nil Sink is
// treated by callers as a [NoopSink].
type Sink interface {
	Emit(ctx context.Context, event Event)
}

// NoopSink is a [Sink] that discards every event. It is the zero-value sink
// used when no presentation is attached (direct library/test construction); the
// CLI always injects a real terminal sink, so operator behaviour is unchanged.
type NoopSink struct{}

// Emit implements [Sink] by discarding the event.
func (NoopSink) Emit(context.Context, Event) {}

// RecordingSink is a [Sink] for tests: it records every emitted event in
// delivery order. Because [Sink.Emit] is synchronous, events arrive from the
// emitter's goroutine; the accessors are nevertheless mutex-guarded so a test
// goroutine may read them concurrently.
type RecordingSink struct {
	mu     sync.Mutex
	events []Event
}

// NewRecordingSink returns a ready-to-use [RecordingSink].
func NewRecordingSink() *RecordingSink { return &RecordingSink{} }

// Emit implements [Sink] by appending event to the recorded sequence.
func (r *RecordingSink) Emit(_ context.Context, event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

// Events returns a copy of the recorded events in delivery order. The returned
// slice is safe to retain and mutate.
func (r *RecordingSink) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// Kinds returns the [Kind] of each recorded event in delivery order. It is the
// convenience form of Events for order-only assertions.
func (r *RecordingSink) Kinds() []Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Kind, len(r.events))
	for i, e := range r.events {
		out[i] = e.Kind()
	}
	return out
}

// Reset drops all recorded events. It is a no-op when the sink is empty.
func (r *RecordingSink) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// Len returns the number of recorded events.
func (r *RecordingSink) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}
