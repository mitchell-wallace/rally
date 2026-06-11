package telemetry

import (
	"context"
	"time"

	"github.com/getsentry/sentry-go"
)

// Compile-time interface checks.
var (
	_ Sink = (*SentrySink)(nil)
	_ Span = (*sentrySpan)(nil)
)

// anonymousServerName is the static value used for Sentry's ServerName field
// to prevent the SDK from populating it with the host's actual hostname.
const anonymousServerName = "rally-cli"

// SentrySink is a telemetry sink backed by Sentry. It is created via
// NewSentrySink and should only be used when a valid DSN is available.
type SentrySink struct{}

// NewSentrySink initialises the Sentry SDK and returns a ready sink. The
// caller must eventually call Flush to drain buffered events.
func NewSentrySink(dsn string) (*SentrySink, error) {
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		TracesSampleRate: 1.0,
		// AttachStacktrace adds Go stack traces to error events for
		// better diagnostics.
		AttachStacktrace: true,
		// EnableTracing enables performance monitoring (spans/transactions).
		EnableTracing: true,
		// ServerName is set to a static non-host value to prevent the
		// Sentry SDK from sending the machine's hostname. This is a
		// privacy guarantee: no host-derived identity is transmitted.
		ServerName: anonymousServerName,
		// before_send scrubber: never ships current_task.md contents or full
		// transcripts, only summaries and metadata.
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return scrubEvent(event)
		},
		BeforeSendTransaction: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return scrubEvent(event)
		},
	})
	if err != nil {
		return nil, err
	}
	return &SentrySink{}, nil
}

// StartSpan begins a new Sentry span. If there is no active transaction on
// the context a new transaction is started (used for relay-level spans).
func (s *SentrySink) StartSpan(ctx context.Context, operation, description string) (context.Context, Span) {
	parentSpan := sentry.SpanFromContext(ctx)
	var span *sentry.Span
	if parentSpan == nil {
		// No parent — start a new transaction (top-level relay span).
		span = sentry.StartTransaction(ctx, operation)
		span.Description = description
	} else {
		span = parentSpan.StartChild(operation)
		span.Description = description
	}
	return span.Context(), &sentrySpan{span: span}
}

// EmitTryLog records a structured per-try event as breadcrumbs on the
// current Sentry scope so they are attached to subsequent events.
func (s *SentrySink) EmitTryLog(ctx context.Context, fields map[string]interface{}) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	hub.AddBreadcrumb(&sentry.Breadcrumb{
		Category: "try",
		Data:     fields,
		Level:    sentry.LevelInfo,
	}, nil)
}

// CaptureFailure reports an operator-worthy failure as a Sentry event
// (Issue). Tags are set on a cloned scope so they don't leak. Context
// blocks are attached via scope.SetContext for structured nested data.
func (s *SentrySink) CaptureFailure(ctx context.Context, msg string, evt FailureEvent) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	hub.WithScope(func(scope *sentry.Scope) {
		for k, v := range evt.Tags {
			scope.SetTag(k, v)
		}
		for name, data := range evt.Contexts {
			scope.SetContext(name, data)
		}
		hub.CaptureMessage(msg)
	})
}

// Flush drains buffered Sentry events with a bounded timeout. Safe to call
// even when the network is unreachable — returns after timeout.
func (s *SentrySink) Flush(timeout time.Duration) {
	sentry.Flush(timeout)
}

// sentrySpan wraps a *sentry.Span to satisfy the Span interface.
type sentrySpan struct {
	span *sentry.Span
}

func (s *sentrySpan) SetTag(key, value string) {
	s.span.SetTag(key, value)
}

func (s *sentrySpan) SetData(key string, value interface{}) {
	s.span.SetData(key, value)
}

func (s *sentrySpan) Finish() {
	s.span.Finish()
}
