package telemetry

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nr "github.com/newrelic/go-agent/v3/newrelic"
)

// Compile-time interface checks.
var (
	_ Sink = (*NewRelicSink)(nil)
	_ Span = (*newRelicSpan)(nil)
)

const (
	defaultNewRelicAppName                    = "Rally CLI"
	defaultNewRelicHostDisplayName            = "rally-cli"
	defaultNewRelicAppLogForwardingMaxSamples = 1000

	envNewRelicLicenseKey = "NEW_RELIC_LICENSE_KEY"
)

var rallySpanCounter atomic.Uint64

type newRelicSpanContextKey struct{}

type newRelicSpanContext struct {
	spanID string
}

// NewRelicSink is a telemetry sink backed by the New Relic Go APM agent.
// It is constructed independently from resolveSink until the later cut-over
// lap wires New Relic into telemetry activation.
type NewRelicSink struct {
	app             *nr.Application
	shutdownTimeout time.Duration
}

// NewNewRelicSink initializes the New Relic Go APM agent using Rally's
// bounded, scrubbed telemetry defaults. An empty license creates a disabled
// agent so tests and unconfigured source builds make no network calls.
func NewNewRelicSink(cfg Config) (*NewRelicSink, error) {
	app, err := nr.NewApplication(newRelicConfigOptions(cfg)...)
	if err != nil {
		return nil, err
	}
	if cfg.NewRelicStartupWaitTimeout > 0 {
		_ = app.WaitForConnection(cfg.NewRelicStartupWaitTimeout)
	}
	return &NewRelicSink{
		app:             app,
		shutdownTimeout: newRelicTimeoutOrDefault(cfg.NewRelicShutdownTimeout, flushTimeout),
	}, nil
}

func newRelicConfigOptions(cfg Config) []nr.ConfigOption {
	return []nr.ConfigOption{
		nr.ConfigFromEnvironment(),
		func(nrc *nr.Config) {
			license := strings.TrimSpace(nrc.License)
			if license == "" {
				license = strings.TrimSpace(cfg.NewRelicLicenseKey)
			}
			if license == "" {
				license = strings.TrimSpace(cfg.DefaultNewRelicLicenseKey)
			}
			nrc.License = license
			if license == "" {
				nrc.Enabled = false
			}

			if strings.TrimSpace(nrc.AppName) == "" {
				if cfg.NewRelicAppName != "" {
					nrc.AppName = truncateValue(collapseHomePaths(strings.TrimSpace(cfg.NewRelicAppName)))
				} else {
					nrc.AppName = defaultNewRelicAppName
				}
			}

			if strings.TrimSpace(nrc.HostDisplayName) == "" {
				if cfg.NewRelicHostDisplayName != "" {
					nrc.HostDisplayName = truncateValue(collapseHomePaths(strings.TrimSpace(cfg.NewRelicHostDisplayName)))
				} else {
					nrc.HostDisplayName = defaultNewRelicHostDisplayName
				}
			}
			nrc.ErrorCollector.RecordPanics = true
		},
		func(nrc *nr.Config) {
			appLogEnabled := boolConfigValue(cfg.NewRelicAppLogEnabled, true)
			forwardingEnabled := boolConfigValue(cfg.NewRelicAppLogForwardingEnabled, true)
			metricsEnabled := boolConfigValue(cfg.NewRelicAppLogMetricsEnabled, true)
			decoratingEnabled := boolConfigValue(cfg.NewRelicAppLogDecoratingEnabled, false)
			maxSamples := cfg.NewRelicAppLogForwardingMaxSamplesStored
			if maxSamples <= 0 {
				maxSamples = defaultNewRelicAppLogForwardingMaxSamples
			}

			nr.ConfigAppLogEnabled(appLogEnabled)(nrc)
			if appLogEnabled {
				nr.ConfigAppLogForwardingEnabled(forwardingEnabled)(nrc)
				nr.ConfigAppLogMetricsEnabled(metricsEnabled)(nrc)
				nr.ConfigAppLogForwardingMaxSamplesStored(maxSamples)(nrc)
				nr.ConfigAppLogDecoratingEnabled(decoratingEnabled)(nrc)
				return
			}

			nr.ConfigAppLogForwardingEnabled(false)(nrc)
			nr.ConfigAppLogMetricsEnabled(false)(nrc)
			nr.ConfigAppLogForwardingMaxSamplesStored(0)(nrc)
			nr.ConfigAppLogDecoratingEnabled(false)(nrc)
		},
	}
}

func boolConfigValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func newRelicTimeoutOrDefault(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func (s *NewRelicSink) StartSpan(ctx context.Context, operation, description string) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.app == nil {
		return ctx, NoopSpan{}
	}

	parent := newRelicSpanFromContext(ctx)
	spanID := nextRallySpanID()
	txn := nr.FromContext(ctx)
	if txn == nil {
		txn = s.app.StartTransaction(newRelicSpanName(operation, description))
		ctx = nr.NewContext(ctx, txn)
		span := newNewRelicSpan(txn, nil, operation, description, spanID, parent.spanID)
		return context.WithValue(ctx, newRelicSpanContextKey{}, newRelicSpanContext{spanID: spanID}), span
	}

	segment := txn.StartSegment(newRelicSpanName(operation, description))
	span := newNewRelicSpan(txn, segment, operation, description, spanID, parent.spanID)
	return context.WithValue(ctx, newRelicSpanContextKey{}, newRelicSpanContext{spanID: spanID}), span
}

func (s *NewRelicSink) EmitTryLog(_ context.Context, fields map[string]interface{}) {
	fields = ensureTryLogOutcome(fields)
	if s == nil || s.app == nil {
		return
	}
	s.app.RecordCustomEvent(newRelicEventRallyTry, buildFlatAttributes(fields))
}

func (s *NewRelicSink) EmitRouteEvent(_ context.Context, fields map[string]interface{}) {
	if s == nil || s.app == nil {
		return
	}
	s.app.RecordCustomEvent(newRelicEventRallyRoute, buildFlatAttributes(fields))
}

func ensureTryLogOutcome(fields map[string]interface{}) map[string]interface{} {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	if outcome, ok := fields["outcome"].(string); ok && strings.TrimSpace(outcome) != "" {
		return fields
	}
	fmt.Fprintln(os.Stderr, "warning: telemetry RallyTry event missing non-empty outcome; filling outcome=\"unknown\"")
	fields["outcome"] = "unknown"
	return fields
}

func (s *NewRelicSink) CaptureFailure(ctx context.Context, msg string, evt FailureEvent) {
	class := newRelicErrorClass(msg, evt)
	attrs := buildFailureAttributesWithFields(evt, map[string]interface{}{
		"error_class": class,
		"message":     msg,
	})

	if txn := nr.FromContext(ctx); txn != nil {
		txn.NoticeError(nr.Error{
			Message:    truncateValue(collapseHomePaths(msg)),
			Class:      class,
			Attributes: attrs,
		})
	}

	if s == nil || s.app == nil {
		return
	}
	s.app.RecordCustomEvent(newRelicEventRallyFailure, attrs)
}

func (s *NewRelicSink) CaptureEvent(_ context.Context, msg string, evt Event) {
	if s == nil || s.app == nil {
		return
	}
	attrs := buildEventAttributesWithFields(evt, map[string]interface{}{"message": msg})
	s.app.RecordCustomEvent(newRelicEventRallyDiagnostic, attrs)
}

func (s *NewRelicSink) Flush(timeout time.Duration) {
	if s == nil || s.app == nil {
		return
	}
	timeout = newRelicTimeoutOrDefault(timeout, s.shutdownTimeout)
	s.app.Shutdown(timeout)
}

func newRelicSpanFromContext(ctx context.Context) newRelicSpanContext {
	if ctx == nil {
		return newRelicSpanContext{}
	}
	state, _ := ctx.Value(newRelicSpanContextKey{}).(newRelicSpanContext)
	return state
}

func nextRallySpanID() string {
	return strconv.FormatUint(rallySpanCounter.Add(1), 10)
}

func newRelicSpanName(operation, description string) string {
	if description != "" {
		return description
	}
	if operation != "" {
		return operation
	}
	return "rally"
}

func newRelicErrorClass(msg string, evt FailureEvent) string {
	switch evt.Tags["failure_category"] {
	case "usage_limit":
		return "RallyUsageLimit"
	case "short_rate_limit":
		return "RallyShortRateLimit"
	case "provider_overloaded":
		return "RallyProviderOverloaded"
	case "transient_infra":
		return "RallyTransientInfra"
	case "invalid_model":
		return "RallyInvalidModel"
	case "auth_or_proxy":
		return "RallyAuthOrProxy"
	case "harness_launch":
		return "RallyHarnessLaunch"
	case "incomplete_finalization":
		return "RallyIncompleteFinalization"
	case "agent_error":
		return "RallyAgentError"
	case "unidentified_issue":
		return "RallyUnidentifiedIssue"
	}

	if evt.Tags["recovery_classification"] == "needs_user" {
		return "RallyNeedsUser"
	}
	if evt.Tags["agent_state"] == "frozen" {
		return "RallyRelayStall"
	}
	if strings.Contains(strings.ToLower(msg), "panic") {
		return "RallyPanic"
	}
	if outcome := evt.Tags["outcome"]; outcome != "" {
		return fmt.Sprintf("RallyOutcome%s", newRelicClassPart(outcome))
	}
	return "RallyFailure"
}

func newRelicClassPart(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '-' || r == ' ' || r == '.' || r == '/'
	})
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		if len(part) > 1 {
			b.WriteString(part[1:])
		}
	}
	if b.Len() == 0 {
		return "Unknown"
	}
	return b.String()
}

type newRelicSpan struct {
	mu          sync.Mutex
	txn         *nr.Transaction
	segment     *nr.Segment
	operation   string
	description string
	spanID      string
	parentID    string
	startedAt   time.Time
	tags        map[string]string
	fields      map[string]interface{}
	contexts    map[string]map[string]interface{}
	finished    bool
}

func newNewRelicSpan(txn *nr.Transaction, segment *nr.Segment, operation, description, spanID, parentID string) *newRelicSpan {
	return &newRelicSpan{
		txn:         txn,
		segment:     segment,
		operation:   operation,
		description: description,
		spanID:      spanID,
		parentID:    parentID,
		startedAt:   time.Now(),
		tags:        make(map[string]string),
		fields:      make(map[string]interface{}),
		contexts:    make(map[string]map[string]interface{}),
	}
}

func (s *newRelicSpan) SetTag(key, value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tags[key] = value
}

func (s *newRelicSpan) SetData(key string, value interface{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if contextValue, ok := spanContextValue(value); ok {
		s.contexts[key] = contextValue
		return
	}
	s.fields[key] = value
}

func (s *newRelicSpan) Finish() {
	if s == nil {
		return
	}

	tags, fields, contexts, segment, txn, alreadyFinished := s.snapshotForFinish()
	if alreadyFinished {
		return
	}

	var recovered interface{}
	if segment == nil && txn != nil {
		recovered = recover()
	}

	attrs := buildSpanAttributes(tags, fields, contexts)
	for key, value := range attrs {
		if segment != nil {
			segment.AddAttribute(key, value)
			continue
		}
		if txn != nil {
			txn.AddAttribute(key, value)
		}
	}

	if segment != nil {
		segment.End()
		return
	}
	if txn != nil {
		if recovered != nil {
			msg := newRelicPanicMessage(recovered)
			txn.NoticeError(nr.Error{
				Message:    msg,
				Class:      newRelicPanicClass,
				Attributes: buildPanicAttributes(tags, fields, contexts, recovered, msg),
				Stack:      nr.NewStackTrace(),
			})
			txn.End()
			panic(recovered)
		}
		txn.End()
	}
}

const newRelicPanicClass = "RallyPanic"

func newRelicPanicMessage(recovered interface{}) string {
	msg := fmt.Sprint(recovered)
	return truncateValue(collapseHomePaths(msg))
}

func buildPanicAttributes(tags map[string]string, fields map[string]interface{}, contexts map[string]map[string]interface{}, recovered interface{}, msg string) map[string]interface{} {
	panicFields := cloneAttributeMap(fields)
	panicFields["error_class"] = newRelicPanicClass
	panicFields["message"] = msg
	panicFields["panic_type"] = fmt.Sprintf("%T", recovered)
	return buildSpanAttributes(tags, panicFields, contexts)
}

func (s *newRelicSpan) snapshotForFinish() (map[string]string, map[string]interface{}, map[string]map[string]interface{}, *nr.Segment, *nr.Transaction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return nil, nil, nil, nil, nil, true
	}
	s.finished = true

	tags := cloneStringMap(s.tags)
	fields := cloneAttributeMap(s.fields)
	fields["operation"] = s.operation
	fields["description"] = s.description
	fields["duration_ms"] = time.Since(s.startedAt).Milliseconds()
	fields["rally_span_id"] = s.spanID
	if s.parentID != "" {
		fields["rally_parent_span_id"] = s.parentID
	}
	contexts := cloneContextMaps(s.contexts)
	return tags, fields, contexts, s.segment, s.txn, false
}

func spanContextValue(value interface{}) (map[string]interface{}, bool) {
	switch x := value.(type) {
	case map[string]interface{}:
		return cloneAttributeMap(x), true
	case map[string]string:
		out := make(map[string]interface{}, len(x))
		for k, v := range x {
			out[k] = v
		}
		return out, true
	default:
		return nil, false
	}
}
