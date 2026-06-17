package telemetry

import (
	"context"
	"reflect"
	"testing"
	"time"

	nr "github.com/newrelic/go-agent/v3/newrelic"
	"github.com/newrelic/go-agent/v3/newrelic/integrationsupport"
)

func TestNewRelicSink_ConstructsDisabledWithZeroConfig(t *testing.T) {
	t.Setenv(envNewRelicLicenseKey, "")
	t.Setenv("NEW_RELIC_APP_NAME", "")
	t.Setenv("NEW_RELIC_PROCESS_HOST_DISPLAY_NAME", "")

	sink, err := NewNewRelicSink(Config{})
	if err != nil {
		t.Fatalf("NewNewRelicSink() error = %v", err)
	}
	var _ Sink = sink

	ctx, span := sink.StartSpan(context.Background(), "relay", "relay-smoke")
	span.SetTag("relay_id", "1")
	span.SetData("completed", true)
	span.SetData("rally", map[string]interface{}{"version": "test"})
	sink.EmitTryLog(ctx, map[string]interface{}{
		"event":    "try",
		"relay_id": 1,
		"run_id":   1,
		"try_id":   1,
		"outcome":  "completed",
	})
	sink.CaptureEvent(ctx, "smoke diagnostic", Event{
		Level: LevelWarning,
		Tags:  map[string]string{"event_kind": "smoke"},
	})
	sink.CaptureFailure(ctx, "smoke failure", FailureEvent{
		Tags: map[string]string{"failure_category": "harness_launch"},
	})
	span.Finish()

	done := make(chan struct{})
	go func() {
		sink.Flush(10 * time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabled NewRelicSink Flush did not return promptly")
	}
}

func TestNewRelicConfigOptions_RecordPanics(t *testing.T) {
	t.Setenv(envNewRelicLicenseKey, "")
	t.Setenv("NEW_RELIC_APP_NAME", "")
	t.Setenv("NEW_RELIC_PROCESS_HOST_DISPLAY_NAME", "")

	var cfg nr.Config
	for _, option := range newRelicConfigOptions(Config{}) {
		option(&cfg)
	}
	if !cfg.ErrorCollector.RecordPanics {
		t.Fatal("New Relic config did not enable ErrorCollector.RecordPanics")
	}
}

func TestNewRelicSpanFinish_RecordsPanicEndsTransactionAndRepanics(t *testing.T) {
	testApp := integrationsupport.NewTestApp(integrationsupport.SampleEverythingReplyFn, func(cfg *nr.Config) {
		cfg.ErrorCollector.RecordPanics = true
	})
	sink := &NewRelicSink{
		app:             testApp.Application,
		shutdownTimeout: time.Second,
	}

	sentinel := &panicSentinel{message: "relay panic"}
	var recovered interface{}
	func() {
		defer func() {
			recovered = recover()
		}()

		_, span := sink.StartSpan(context.Background(), "relay", "relay-panic")
		span.SetTag("relay_id", "42")
		span.SetTag("prompt", "must be dropped")
		span.SetData("details", map[string]interface{}{
			"visible": "value",
			"log":     "must be dropped",
		})
		defer span.Finish()

		panic(sentinel)
	}()

	if recovered != sentinel {
		t.Fatalf("recovered panic = %#v, want original sentinel %#v", recovered, sentinel)
	}

	expectNewRelicErrors(t, testApp, []newRelicErrorExpectation{{
		Msg:   sentinel.message,
		Klass: newRelicPanicClass,
		UserAttributes: map[string]interface{}{
			"description":     "relay-panic",
			"details.visible": "value",
			"duration_ms":     "*",
			"error_class":     newRelicPanicClass,
			"message":         sentinel.message,
			"operation":       "relay",
			"panic_type":      "*telemetry.panicSentinel",
			"rally_span_id":   "*",
			"relay_id":        "42",
		},
	}})
}

type panicSentinel struct {
	message string
}

func (p *panicSentinel) String() string {
	return p.message
}

type newRelicErrorExpectation struct {
	Msg            string
	Klass          string
	UserAttributes map[string]interface{}
}

func expectNewRelicErrors(t *testing.T, app interface{}, want []newRelicErrorExpectation) {
	t.Helper()

	method := reflect.ValueOf(app).MethodByName("ExpectErrors")
	if !method.IsValid() {
		t.Fatal("New Relic test app does not expose ExpectErrors")
	}
	methodType := method.Type()
	if methodType.NumIn() != 2 {
		t.Fatalf("ExpectErrors input count = %d, want 2", methodType.NumIn())
	}

	wantType := methodType.In(1)
	wantValue := reflect.MakeSlice(wantType, len(want), len(want))
	for i, expected := range want {
		item := wantValue.Index(i)
		setStringField(t, item, "Msg", expected.Msg)
		setStringField(t, item, "Klass", expected.Klass)
		if expected.UserAttributes != nil {
			field := item.FieldByName("UserAttributes")
			if !field.IsValid() || !field.CanSet() {
				t.Fatal("WantError.UserAttributes is not settable")
			}
			field.Set(reflect.ValueOf(expected.UserAttributes))
		}
	}

	method.Call([]reflect.Value{reflect.ValueOf(t), wantValue})
}

func setStringField(t *testing.T, value reflect.Value, name, fieldValue string) {
	t.Helper()
	field := value.FieldByName(name)
	if !field.IsValid() || !field.CanSet() {
		t.Fatalf("WantError.%s is not settable", name)
	}
	field.SetString(fieldValue)
}
