## Draft: Enrich Telemetry Coverage — Metrics, Structured Logs, and Panic Recovery

## Status

Superseded by `migrate-telemetry-to-new-relic`. The intent has moved into the New Relic migration where it is actively implemented:

- Native panic/error capture via New Relic error reporting (`NoticeError`) and stack traces.
- Application logs enabled through the New Relic Go APM agent, with Rally kept from deliberately writing prompts, transcripts, or raw command output as log records.
- Explicit custom metrics remain deferred unless a later New Relic-specific change needs them.

## Why

Rally's telemetry stack (`internal/telemetry/`) provides tracing (relay→run→try spans), failure capture via `NoticeError`, and per-try custom events. Three observability features are planned to be enriched:

- **Metrics** (`newrelic.RecordCustomMetric`): completely absent. No counters, gauges, or distributions are emitted anywhere.
- **Structured Logs** (log forwarding): Native log ingestion is currently unconfigured. `EmitTryLog` sends custom events instead, which are attached to error events but not independently queryable as logs.
- **Panic / Exception Recovery** (`newrelic.NoticeError` in recovery blocks): Rally's runner catches panics at the failure-reason string level (`runner.go:1706` checks for "panic" in text), but never captures Go panic stack traces or `error` values through the native exception path.

Filling these gaps gives a complete observability picture: traces for performance, structured logs for searchable per-try output, metrics for aggregate trends, and proper exception capture for crash diagnostics.

## Intent

### Metrics

Add a `Meter` method to the `Sink` interface and implement it using `newrelic.RecordCustomMetric`. Emit metrics at existing instrumentation points in `internal/relay/runner.go`:

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

`NoopSink` returns a no-op meter.

### Structured Logs

Enable APM log forwarding in the New Relic configuration. Add a `Log` method to the `Sink` interface that emits structured log entries via the logging API. The existing `EmitTryLog` custom events can be supplemented (not replaced) with proper log entries for the fields already being collected at `runner.go:1652`.

### Panic / Exception Recovery

- Add a `defer` block in the relay entry path (`cmd/rally/main.go:219` area or `runner.go` relay start) that recovers from panics and reports them via `NoticeError`.
- Use `NoticeError(err)` in error-return paths where structured `error` values are available, rather than only stringified descriptions. Key locations: runner command execution failures in `internal/agent/log.go:38-82` `runLoggedCommand`, and the reliability classification paths in `internal/relay/runner.go`.
- The existing string-based panic detection in `runner.go:1706` can remain as a fallback classification, but native Go panic recovery should feed APM proper stack traces.

## Initial Questions

- Should structured logs replace custom events (`EmitTryLog`) or run alongside them? Custom events are useful for querying structured data, while logs are better for streaming. Both have value, but doubling traffic may not be desirable.
- For panic recovery, should the recovery happen at the relay boundary (top-level `cmd/rally/main.go`) or at the run/try boundary (`runner.go`)? Relay-level is simpler but coarser; try-level gives per-try stack traces attached to the right span context.
- What sample rate for logs? Tracing is at 100%, but log volume may be higher since every try emits. Consider a configurable log sample rate.

## Candidate Work

- Extend `Sink` interface with `Meter() Meter` and `Log(ctx, level, message, fields)` methods; add no-op implementations.
- Implement `Meter()` returning a New Relic specific implementation.
- Implement `Log()` using structured log emission.
- Enable log forwarding in APM options.
- Add `defer` panic recovery at relay and/or try boundaries.
- Replace string captures with `NoticeError` where structured `error` values are available.
- Instrument runner.go with metric emissions at the call sites listed above.
- Update `internal/telemetry/sink_test.go` with meter and log method tests.