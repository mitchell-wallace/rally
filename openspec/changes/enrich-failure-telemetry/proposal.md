## Why

Rally already has an opt-in Sentry-backed telemetry sink (`internal/telemetry/`):
relays are traces, tries are spans, and operator-worthy failures are captured as
Issues with correlation tags (`relay_id`, `run_id`, `try_id`, `role`, `runner`,
`repo`, `lap_id`). What it lacks is the **run-context** needed to make a captured
failure actionable in isolation: you can see *that* a try failed but not *where* it
ran, *which build* produced it, or *what the agent's state was* at the moment of
failure. And every identifier is local to one machine's store — a `relay_id` of `7`
means nothing once events from several machines and repos land in one Sentry project.

This change enriches the existing sink (it is **not** a new integration) so each
failure carries enough environment and state to triage without the local machine in
hand, and so relays are globally identifiable across machines.

## What Changes

- **Run environment context.** Attach a `rally` context to events: rally version
  (`internal/buildinfo`), OS + architecture, and a coarse terminal descriptor (TTY
  vs non-TTY, `$TERM`). No hostname, no username, no IP.
- **Anonymous machine-local hash.** Generate a random, stable per-machine identifier
  once and persist it in rally's data dir (e.g. `<dataDir>/machine-id`). It is a
  fresh random value, **not** derived from hostname/MAC/username, so it correlates a
  machine's events over time without fingerprinting the operator.
- **Globally-unique relay identity.** Derive a `relay_guid` from the machine-local
  hash, the relay's start date, and the local `relay_id`
  (`<machine-hash>-<YYYYMMDD>-<relay_id>`), and attach it plus `relay_started_at` and
  `machine_id` as tags/context. The local `relay_id` tag is retained for back-compat;
  `relay_guid` is the cross-machine-safe key.
- **Username-stripped working directory.** Attach the working directory with the home
  prefix collapsed (`/home/<user>/…` → `~/…`), so the path shape is visible for
  triage without leaking the username. The existing `repo` tag (path-hash) is kept.
- **Agent state on failure.** When a failure is captured, attach the runner's state
  snapshot: harness+model (already in `runner`), current attempt / retry budget,
  failure class (infra vs agent vs incomplete), and the agent-type resilience state
  (active / probation / frozen) where known.

## Capabilities

### Modified Capabilities
- `telemetry`: adds run-environment context, an anonymous machine-local hash, a
  globally-unique relay identity, username-stripped cwd, and a failure-time agent
  state snapshot. The existing PII-scrubbing requirement is extended to cover the new
  cwd and environment fields.

## Impact

- **Code**: `internal/telemetry/` (`tags.go` for the new tags, a new
  environment/context builder, `scrubber.go` for cwd/home stripping,
  `sentry.go`/`sink.go` if the context attachment needs a new entry point), a small
  machine-id helper (new file under `internal/telemetry/` or `internal/buildinfo/`),
  and the three `CaptureFailure` call sites in `internal/relay/runner.go`
  (`runner.go:393`, `:1227`, `:1298`) plus the relay-start span tagging
  (`runner.go:337`).
- **Behavior**: when telemetry is disabled (no DSN, or `RALLY_TELEMETRY=0`) nothing
  changes — the machine-id file is only written when the sink is active. When enabled,
  captured failures gain environment + identity + state context.
- **Privacy**: no new PII is transmitted. The machine hash is random and anonymous;
  cwd is home-stripped; hostname/username/IP are never sent. The change extends, not
  relaxes, the existing `before_send` scrubber.
- **Out of scope**: changing the failure taxonomy (which failures become Issues vs
  spans is owned by the existing telemetry spec / `harden-relay-run-lifecycle`); any
  non-Sentry backend; sampling-rate changes.
- **Coordination**: the agent-resilience state names (active / probation / frozen)
  are governed by `harden-relay-run-lifecycle`'s vocabulary; reuse those terms rather
  than inventing new ones.
