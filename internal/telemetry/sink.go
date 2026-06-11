// Package telemetry provides an opt-in telemetry sink abstraction.
// The default implementation is a no-op; a Sentry-backed implementation
// activates only when a DSN is configured.
package telemetry

import (
	"context"
	"time"
)

// Sink is the narrow telemetry interface. Implementations must be safe for
// concurrent use. The interface is intentionally small so an OpenTelemetry
// backend can be swapped in later without changing call sites.
type Sink interface {
	// StartSpan begins a new span (relay, run, or try). The returned Span
	// must be finished by the caller. The context carries the span for
	// child-span correlation.
	StartSpan(ctx context.Context, operation, description string) (context.Context, Span)

	// EmitTryLog records a structured per-try event. Fields are
	// string-keyed; values should be simple scalars (string, int, float).
	EmitTryLog(ctx context.Context, fields map[string]interface{})

	// CaptureFailure reports an operator-worthy failure as a Sentry Issue
	// (or equivalent). The FailureEvent carries scalar tags for filtering
	// and context blocks for structured nested data.
	CaptureFailure(ctx context.Context, msg string, evt FailureEvent)

	// CaptureEvent reports a non-Issue structured event such as a low-severity
	// diagnostic signal. Implementations should preserve the event level.
	CaptureEvent(ctx context.Context, msg string, evt Event)

	// Flush drains buffered events with a bounded timeout. It must return
	// promptly even when the network is unreachable.
	Flush(timeout time.Duration)
}

type EventLevel string

const (
	LevelInfo  EventLevel = "info"
	LevelError EventLevel = "error"
)

// FailureEvent carries structured data for a captured failure. Tags are
// scalar filterable values (Sentry tags); Contexts are nested structured
// blocks (Sentry contexts). This separation prevents high-cardinality or
// nested data from being smuggled into indexed tags.
type FailureEvent struct {
	// Tags are scalar key-value pairs attached to the event for filtering
	// and grouping (e.g., relay_id, run_id, role).
	Tags map[string]string

	// Contexts are named blocks of structured data attached to the event
	// (e.g., a "rally" block with version/os/arch/term).
	Contexts map[string]map[string]interface{}
}

// Event carries structured data for a non-Issue telemetry event. Tags and
// contexts follow the same split as FailureEvent, with Level controlling
// severity in the backend.
type Event struct {
	Level    EventLevel
	Tags     map[string]string
	Contexts map[string]map[string]interface{}
}

// Span represents an in-flight trace span (relay, run, or try).
type Span interface {
	// SetTag attaches a key/value tag to the span.
	SetTag(key, value string)

	// SetData attaches structured data to the span.
	SetData(key string, value interface{})

	// Finish completes the span, recording its duration.
	Finish()
}
