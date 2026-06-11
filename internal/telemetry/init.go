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
	SentryDSN  string
	DefaultDSN string

	// DataDir is the resolved data directory (e.g. ~/.local/share/rally)
	// where the persistent machine-id file is stored. An empty value means
	// no data directory is available — machine identity falls back to an
	// ephemeral per-process value.
	DataDir string
}

// InitResult holds the outputs of Init for the caller.
type InitResult struct {
	// Sink is the active telemetry sink (NoopSink when disabled).
	Sink Sink

	// Cleanup flushes buffered events; must be deferred by the caller.
	Cleanup func()

	// MachineID is the anonymous stable machine identity. It is a 32-char
	// hex string (128-bit) when telemetry is active and persistence
	// succeeds, or an ephemeral per-process value on storage failure.
	// It is empty when telemetry is disabled.
	MachineID string
}

// Init initialises the telemetry sink. It returns an InitResult containing
// the active Sink, a cleanup function, and the resolved machine identity.
//
// Precedence:
//  1. RALLY_TELEMETRY=0 → disabled (NoopSink), regardless of DSN.
//  2. SENTRY_DSN env var → overrides config.toml sentry_dsn and default.
//  3. Config SentryDSN → overrides the baked-in default.
//  4. Config DefaultDSN → baked-in default (injected by GoReleaser).
//  5. No DSN → disabled (NoopSink).
//
// Machine identity is only resolved/created when telemetry is active.
// Disabled telemetry writes no file and returns an empty MachineID.
//
// Errors from Sentry SDK initialisation are swallowed (telemetry is best-
// effort and must never prevent the CLI from running).
func Init(cfg Config) (Sink, func()) {
	noop := func() {}

	// Kill switch: RALLY_TELEMETRY=0 force-disables.
	if os.Getenv(envKillSwitch) == "0" {
		return NoopSink{}, noop
	}

	// DSN resolution: env overrides config, config overrides default.
	dsn := os.Getenv(envSentryDSN)
	if dsn == "" {
		dsn = cfg.SentryDSN
	}
	if dsn == "" {
		dsn = cfg.DefaultDSN
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

// InitWithIdentity initialises the telemetry sink and resolves the anonymous
// machine identity. It returns an InitResult containing the sink, cleanup
// function, and machine ID. Machine identity is only resolved when telemetry
// is active; disabled telemetry returns an empty MachineID and writes no file.
func InitWithIdentity(cfg Config) InitResult {
	noop := func() {}

	// Kill switch: RALLY_TELEMETRY=0 force-disables.
	if os.Getenv(envKillSwitch) == "0" {
		return InitResult{Sink: NoopSink{}, Cleanup: noop}
	}

	// DSN resolution: env overrides config, config overrides default.
	dsn := os.Getenv(envSentryDSN)
	if dsn == "" {
		dsn = cfg.SentryDSN
	}
	if dsn == "" {
		dsn = cfg.DefaultDSN
	}
	if dsn == "" {
		return InitResult{Sink: NoopSink{}, Cleanup: noop}
	}

	sink, err := NewSentrySink(dsn)
	if err != nil {
		// Best-effort: if Sentry init fails, fall back to no-op.
		return InitResult{Sink: NoopSink{}, Cleanup: noop}
	}

	// Telemetry is active — resolve machine identity.
	machineID := resolveOrCreateMachineID(cfg.DataDir)

	cleanup := func() {
		sink.Flush(flushTimeout)
	}
	return InitResult{Sink: sink, Cleanup: cleanup, MachineID: machineID}
}
