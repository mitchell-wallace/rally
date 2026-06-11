## Draft: Enrich Sentry Coverage — Metrics, Structured Logs, and Panic Recovery

## Why

Rally's telemetry stack (`internal/telemetry/`) already provides tracing (relay→run→try spans), failure capture via `CaptureMessage`, and per-try breadcrumbs. Three Sentry features are wired into the SDK but not yet used:

- **Metrics** (`sentry.NewMeter`): completely absent. No counters, gauges, or distributions are emitted anywhere.
- **Structured Logs** (`EnableLogs: true`): Sentry's native log ingestion is disabled. `EmitTryLog` sends breadcrumbs instead, which are attached to error events but not independently queryable as logs.
- **Panic / Exception Recovery** (`sentry.Recovery()`, `sentry.CaptureException`): Rally's runner catches panics at the failure-reason string level (`runner.go:1706` checks for "panic" in text), but never captures Go panic stack traces or `error` values through Sentry's native exception path.

Filling these gaps gives a complete observability picture: traces for performance, structured logs for searchable per-try output, metrics for aggregate trends, and proper exception capture for crash diagnostics.

## Intent

### Metrics

Add a `Meter` method to the `Sink` interface and implement it in `SentrySink` using `sentry.NewMeter`. Emit metrics at existing instrumentation points in `internal/relay/runner.go`:

| Metric | Type | Code path | When |
|--------|------|-----------|------|
| `relay.completed` | Counter | `runner.go` relay finish | Each relay finishes (tag: outcome) |
| `run.attempts` | Counter | `runner.go:631` run span start | Each run attempt |
| `run.route_fallback` | Counter | `runner.go:1696` | Runner rotation / fallback |
| `try.duration_ms` | Distribution | `runner.go:1652` try end | Each try completes |
| `try.files_changed` | Gauge | `runner.go:1652` | Files changed in try |
| `try.failure` | Counter | `runner.go:1713` | Try failure by class |
| `agent.exit_without_finalize` | Counter | `runner.go:1801` | Agent exited dirty |
| `relay.stall` | Counter | `runner.go:583` | All agents frozen |

`NoopSink` returns a no-op meter. The meter is created once per `SentrySink` and reused.

### Structured Logs

Enable Sentry's log ingestion by setting `EnableLogs: true` in the `sentry.ClientOptions` at `internal/telemetry/sentry.go:27`. Add a `Log` method to the `Sink` interface that emits structured log entries via the Sentry logs API (the SDK's `sentry.Logger` / log-to-Sentry path). The existing `EmitTryLog` breadcrumbs can be supplemented (not replaced) with proper log entries for the fields already being collected at `runner.go:1652`.

### Panic / Exception Recovery

- Add `sentry.Recovery()` or a `defer sentry.Recover()` call in the relay entry path (`cmd/rally/main.go:219` area or `runner.go` relay start).
- Use `sentry.CaptureException(err)` in error-return paths where structured `error` values are available, rather than only `CaptureMessage` with stringified descriptions. Key locations: runner command execution failures in `internal/agent/log.go:38-82` `runLoggedCommand`, and the reliability classification paths in `internal/relay/runner.go`.
- The existing string-based panic detection in `runner.go:1706` can remain as a fallback classification, but native Go panic recovery should feed Sentry proper stack traces.

## Initial Questions

- Should `Sink.Meter()` return a long-lived `*sentry.Meter` or should each call site create its own scoped meter? The SDK's `NewMeter` is cheap but context-scoped — the interface design should clarify ownership.
- Should structured logs replace breadcrumbs (`EmitTryLog`) or run alongside them? Breadcrumbs attach to error events; logs are independently queryable. Both have value, but doubling traffic may not be desirable.
- For panic recovery, should the recovery happen at the relay boundary (top-level `cmd/rally/main.go`) or at the run/try boundary (`runner.go`)? Relay-level is simpler but coarser; try-level gives per-try stack traces attached to the right span context.
- What sample rate for logs? Tracing is at 100% (`TracesSampleRate: 1.0`), but log volume may be higher since every try emits. Consider a configurable log sample rate.

## Candidate Work

- Extend `Sink` interface with `Meter() Meter` and `Log(ctx, level, message, fields)` methods; add no-op implementations.
- Implement `SentrySink.Meter()` returning `sentry.NewMeter(context.Background())`.
- Implement `SentrySink.Log()` using Sentry's structured log emission.
- Set `EnableLogs: true` in `NewSentrySink()` options.
- Add `defer sentry.Recover()` at relay and/or try boundaries.
- Replace `CaptureMessage` with `CaptureException` where structured `error` values are available.
- Instrument runner.go with metric emissions at the call sites listed above.
- Update `internal/telemetry/sink_test.go` with meter and log method tests.
