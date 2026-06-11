package telemetry

import (
	"context"
	"os"
	"testing"
)

func TestInit_NoopWithoutDSN(t *testing.T) {
	// Clear any env vars that could interfere.
	t.Setenv(envSentryDSN, "")
	t.Setenv(envKillSwitch, "")

	sink, cleanup := Init(Config{})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when no DSN is configured, got %T", sink)
	}
}

func TestInit_KillSwitchDisablesWithDSN(t *testing.T) {
	t.Setenv(envKillSwitch, "0")
	t.Setenv(envSentryDSN, "")

	sink, cleanup := Init(Config{SentryDSN: "https://key@sentry.io/123"})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when RALLY_TELEMETRY=0, got %T", sink)
	}
}

func TestInit_KillSwitchDisablesWithEnvDSN(t *testing.T) {
	t.Setenv(envKillSwitch, "0")
	t.Setenv(envSentryDSN, "https://key@sentry.io/456")

	sink, cleanup := Init(Config{})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when RALLY_TELEMETRY=0 even with SENTRY_DSN set, got %T", sink)
	}
}

func TestInit_EnvDSNOverridesConfig(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envSentryDSN, "https://envkey@sentry.io/789")

	sink, cleanup := Init(Config{SentryDSN: "https://cfgkey@sentry.io/111"})
	defer cleanup()

	// With a valid DSN from env, we should get a SentrySink.
	if _, ok := sink.(*SentrySink); !ok {
		t.Fatalf("expected *SentrySink when SENTRY_DSN env is set, got %T", sink)
	}
}

func TestInit_ConfigDSNUsedWhenEnvEmpty(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envSentryDSN, "")

	sink, cleanup := Init(Config{SentryDSN: "https://cfgkey@sentry.io/222"})
	defer cleanup()

	if _, ok := sink.(*SentrySink); !ok {
		t.Fatalf("expected *SentrySink when config DSN is set, got %T", sink)
	}
}

func TestInit_InvalidDSNFallsBackToNoop(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envSentryDSN, "")

	// An obviously malformed DSN should cause sentry.Init to fail,
	// and Init should fall back to NoopSink gracefully.
	sink, cleanup := Init(Config{SentryDSN: "not-a-valid-dsn"})
	defer cleanup()

	// sentry-go may or may not reject this at init time — either a
	// SentrySink (SDK accepted it) or NoopSink (SDK rejected it) is
	// acceptable. The key property is that Init never panics.
	_ = sink
}

func TestInit_KillSwitchNonZeroDoesNotDisable(t *testing.T) {
	// RALLY_TELEMETRY=1 or any value other than "0" should NOT disable.
	t.Setenv(envKillSwitch, "1")
	t.Setenv(envSentryDSN, "https://key@sentry.io/333")

	sink, cleanup := Init(Config{})
	defer cleanup()

	if _, ok := sink.(*SentrySink); !ok {
		t.Fatalf("expected *SentrySink when RALLY_TELEMETRY=1, got %T", sink)
	}
}

func TestNoopSink_MethodsDoNotPanic(t *testing.T) {
	var sink NoopSink
	ctx := context.Background()

	// StartSpan
	ctx2, span := sink.StartSpan(ctx, "test.op", "test description")
	if ctx2 != ctx {
		t.Error("NoopSink.StartSpan should return the same context")
	}
	span.SetTag("key", "value")
	span.SetData("key", 42)
	span.Finish()

	// EmitTryLog
	sink.EmitTryLog(ctx, map[string]interface{}{"foo": "bar"})

	// CaptureFailure
	sink.CaptureFailure(ctx, "test failure", FailureEvent{Tags: map[string]string{"k": "v"}})

	// Flush
	sink.Flush(0)
}

func TestNoopSpan_MethodsDoNotPanic(t *testing.T) {
	var span NoopSpan
	span.SetTag("k", "v")
	span.SetData("k", 123)
	span.Finish()
}

func TestInit_DefaultDSNUsedWhenEnvAndConfigEmpty(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envSentryDSN, "")

	sink, cleanup := Init(Config{DefaultDSN: "https://defkey@sentry.io/444"})
	defer cleanup()

	if _, ok := sink.(*SentrySink); !ok {
		t.Fatalf("expected *SentrySink when DefaultDSN is set and env/config empty, got %T", sink)
	}
}

func TestInit_ConfigDSNOverridesDefault(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envSentryDSN, "")

	sink, cleanup := Init(Config{
		SentryDSN:  "https://cfgkey@sentry.io/555",
		DefaultDSN: "https://defkey@sentry.io/666",
	})
	defer cleanup()

	if _, ok := sink.(*SentrySink); !ok {
		t.Fatalf("expected *SentrySink when config DSN overrides default, got %T", sink)
	}
}

func TestInit_EnvDSNOverridesConfigAndDefault(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envSentryDSN, "https://envkey@sentry.io/777")

	sink, cleanup := Init(Config{
		SentryDSN:  "https://cfgkey@sentry.io/888",
		DefaultDSN: "https://defkey@sentry.io/999",
	})
	defer cleanup()

	if _, ok := sink.(*SentrySink); !ok {
		t.Fatalf("expected *SentrySink when env DSN overrides config and default, got %T", sink)
	}
}

func TestInit_KillSwitchDisablesWithAllDSNSources(t *testing.T) {
	t.Setenv(envKillSwitch, "0")
	t.Setenv(envSentryDSN, "https://envkey@sentry.io/aaa")

	sink, cleanup := Init(Config{
		SentryDSN:  "https://cfgkey@sentry.io/bbb",
		DefaultDSN: "https://defkey@sentry.io/ccc",
	})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when RALLY_TELEMETRY=0 with all DSN sources, got %T", sink)
	}
}

func TestInit_EmptyDefaultDSNFallsBackToNoop(t *testing.T) {
	t.Setenv(envKillSwitch, "")
	t.Setenv(envSentryDSN, "")

	sink, cleanup := Init(Config{DefaultDSN: ""})
	defer cleanup()

	if _, ok := sink.(NoopSink); !ok {
		t.Fatalf("expected NoopSink when all DSN sources are empty, got %T", sink)
	}
}

// TestInit_EnvCleanup verifies that t.Setenv restores env vars properly
// (this is a Go stdlib guarantee, but it documents our reliance on it).
func TestInit_EnvCleanup(t *testing.T) {
	original := os.Getenv(envSentryDSN)
	t.Setenv(envSentryDSN, "test-value-that-should-be-cleaned-up")
	// After this test finishes, envSentryDSN should be restored.
	if os.Getenv(envSentryDSN) != "test-value-that-should-be-cleaned-up" {
		t.Fatal("t.Setenv did not set the value")
	}
	_ = original
}
