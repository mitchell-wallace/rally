package telemetry

import (
	"os"
	"time"
)

const (
	// flushTimeout is the maximum time to wait for pending events to be
	// sent on process exit. Bounded so an unreachable network never hangs.
	flushTimeout = 2 * time.Second

	// envKillSwitch force-disables telemetry when set to "0".
	envKillSwitch = "RALLY_TELEMETRY"

	// envSentryDSN overrides the config-file DSN.
	envSentryDSN = "SENTRY_DSN"
)

// Config holds the telemetry configuration from [telemetry] in config.toml.
type Config struct {
	SentryDSN string
}

// Init initialises the telemetry sink. It returns the active Sink and a
// cleanup function that must be deferred by the caller to flush buffered
// events before exit.
//
// Precedence:
//  1. RALLY_TELEMETRY=0 → disabled (NoopSink), regardless of DSN.
//  2. SENTRY_DSN env var → overrides config.toml sentry_dsn.
//  3. Config SentryDSN → used when env var is empty.
//  4. No DSN → disabled (NoopSink).
//
// Errors from Sentry SDK initialisation are swallowed (telemetry is best-
// effort and must never prevent the CLI from running).
func Init(cfg Config) (Sink, func()) {
	noop := func() {}

	// Kill switch: RALLY_TELEMETRY=0 force-disables.
	if os.Getenv(envKillSwitch) == "0" {
		return NoopSink{}, noop
	}

	// DSN resolution: env overrides config.
	dsn := os.Getenv(envSentryDSN)
	if dsn == "" {
		dsn = cfg.SentryDSN
	}
	if dsn == "" {
		return NoopSink{}, noop
	}

	sink, err := NewSentrySink(dsn)
	if err != nil {
		// Best-effort: if Sentry init fails, fall back to no-op.
		return NoopSink{}, noop
	}

	cleanup := func() {
		sink.Flush(flushTimeout)
	}
	return sink, cleanup
}
