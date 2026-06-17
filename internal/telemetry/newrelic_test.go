package telemetry

import (
	"context"
	"testing"
	"time"
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
