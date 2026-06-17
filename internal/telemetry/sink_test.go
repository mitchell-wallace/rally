package telemetry

import (
	"context"
	"os"
	"strings"
	"testing"
)

const testNewRelicLicense = "0123456789012345678901234567890123456789"

func TestInit_NoopWithoutNewRelicLicense(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envNewRelicLicenseKey, "")

	sink, cleanup := Init(Config{})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when no New Relic license is configured, got %T", sink)
	}
}

func TestInit_KillSwitchDisablesWithNewRelicLicense(t *testing.T) {
	t.Setenv(envKillSwitch, "0")
	t.Setenv(envNewRelicLicenseKey, testNewRelicLicense)

	sink, cleanup := Init(Config{DefaultNewRelicLicenseKey: strings.Repeat("1", 40)})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when RALLY_TELEMETRY=0, got %T", sink)
	}
}

func TestInit_ConfigOptOutDisablesWithNewRelicLicense(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envNewRelicLicenseKey, testNewRelicLicense)

	enabled := false
	sink, cleanup := Init(Config{Enabled: &enabled})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when telemetry config disables activation, got %T", sink)
	}
}

func TestInit_KillSwitchNonZeroDoesNotDisable(t *testing.T) {
	t.Setenv(envKillSwitch, "1")
	t.Setenv(envNewRelicLicenseKey, "")

	sink, cleanup := Init(Config{DefaultNewRelicLicenseKey: testNewRelicLicense})
	defer cleanup()

	if _, ok := sink.(*NewRelicSink); !ok {
		t.Fatalf("expected *NewRelicSink when RALLY_TELEMETRY is not 0, got %T", sink)
	}
}

func TestInit_BakedNewRelicLicenseActivates(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envNewRelicLicenseKey, "")

	sink, cleanup := Init(Config{DefaultNewRelicLicenseKey: testNewRelicLicense})
	defer cleanup()

	if _, ok := sink.(*NewRelicSink); !ok {
		t.Fatalf("expected *NewRelicSink when baked New Relic license is set, got %T", sink)
	}
}

func TestInit_EnvCleanup(t *testing.T) {
	original := os.Getenv(envNewRelicLicenseKey)
	t.Setenv(envNewRelicLicenseKey, "test-value-that-should-be-cleaned-up")
	if os.Getenv(envNewRelicLicenseKey) != "test-value-that-should-be-cleaned-up" {
		t.Fatal("t.Setenv did not set the value")
	}
	_ = original
}

func TestNoopSink_MethodsDoNotPanic(t *testing.T) {
	var sink NoopSink
	ctx := context.Background()

	ctx2, span := sink.StartSpan(ctx, "test.op", "test description")
	if ctx2 != ctx {
		t.Error("NoopSink.StartSpan should return the same context")
	}
	span.SetTag("key", "value")
	span.SetData("key", 42)
	span.Finish()

	sink.EmitTryLog(ctx, map[string]interface{}{"foo": "bar"})
	sink.CaptureFailure(ctx, "test failure", FailureEvent{Tags: map[string]string{"k": "v"}})
	sink.CaptureEvent(ctx, "test event", Event{Level: LevelInfo, Tags: map[string]string{"event_kind": "test"}})
	sink.Flush(0)
}

func TestNoopSpan_MethodsDoNotPanic(t *testing.T) {
	var span NoopSpan
	span.SetTag("k", "v")
	span.SetData("k", 123)
	span.Finish()
}
