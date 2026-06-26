package telemetry

import (
	"context"
	"time"
)

// Compile-time interface checks.
var (
	_ Sink = NoopSink{}
	_ Span = NoopSpan{}
)

// NoopSink is the default telemetry sink. Every method is a no-op: no
// allocations, no network access, no side effects. It is safe for
// concurrent use (all methods are stateless).
type NoopSink struct{}

func (NoopSink) StartSpan(ctx context.Context, _, _ string) (context.Context, Span) {
	return ctx, NoopSpan{}
}

func (NoopSink) EmitTryLog(context.Context, map[string]interface{}) {}

func (NoopSink) EmitRouteEvent(context.Context, map[string]interface{}) {}

func (NoopSink) CaptureFailure(context.Context, string, FailureEvent) {}

func (NoopSink) CaptureEvent(context.Context, string, Event) {}

func (NoopSink) Flush(time.Duration) {}

// NoopSpan is the span returned by NoopSink. All methods are no-ops.
type NoopSpan struct{}

func (NoopSpan) SetTag(string, string)       {}
func (NoopSpan) SetData(string, interface{}) {}
func (NoopSpan) Finish()                     {}
