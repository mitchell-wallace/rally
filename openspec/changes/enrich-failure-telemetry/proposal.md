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

Note (2026-06-11): the pre-change sink also does not capture the information
needed to validate per-harness usage-limit parsing and other exit conditions —
events carry no raw provider limit-response text, reset phrasing, or
exit-condition payloads, so the from-memory assumptions in the
`internal/reliability/` parsers cannot be checked against captured data. A
validation pull for `improve-harness-consistency` confirmed this empirically
(no usable limit payloads exist in Sentry). The bounded
`FailureEvidence.RawSignal`/`Message` corpus this change attaches on
limit-category failures is what closes that gap.

## What Changes

- **Run environment context.** Attach a `rally` context to events: rally version
  (`internal/buildinfo`), OS + architecture, and a coarse terminal descriptor (TTY
  vs non-TTY, `$TERM`). No hostname, no username, no IP, including Sentry's top-level
  `server_name`.
- **Anonymous machine-local hash.** Generate a random, stable per-machine identifier
  once and persist it in rally's data dir (e.g. `<dataDir>/machine-id`). It is a
  fresh random value, **not** derived from hostname/MAC/username, so it correlates a
  machine's events over time without fingerprinting the operator.
- **Globally-unique relay identity.** Derive a `relay_guid` from the machine-local
  hash, the repo hash, the relay's start date, and the local `relay_id`
  (`<machine-id-prefix>-<repo-key>-<YYYYMMDD>-<relay_id>`), and attach it plus
  `relay_started_at` and the anonymous machine identity as tags/context. The local
  `relay_id` tag is retained for back-compat; `relay_guid` is the cross-machine-and-
  repo-safe key.
- **Username-stripped working directory.** Attach the working directory with the home
  prefix collapsed (`/home/<user>/…` → `~/…`), so the path shape is visible for
  triage without leaking the username. The existing `repo` tag (path-hash) is kept.
- **Agent state on failure.** When a failure is captured, attach the runner's state
  snapshot: harness+model (already in `runner`), current attempt / retry budget, the
  stable **failure category** and reset/quota evidence produced by
  `improve-error-categorisation` (e.g. `usage_limit` with its `quota_scope` and
  `reset_at`), and the agent-type resilience state (active / probation / frozen /
  benched) where known. The category replaces the older infra/agent/incomplete trio so
  triage reads one consistent vocabulary across CLI, records, and Sentry.

## Capabilities

### Modified Capabilities
- `telemetry`: adds run-environment context, an anonymous machine-local hash, a
  globally-unique relay identity, username-stripped cwd, and a failure-time agent
  state snapshot carrying the stable failure category + reset/quota evidence from
  `improve-error-categorisation`. The existing PII-scrubbing requirement is extended to
  cover the new cwd and environment fields.

## Impact

- **Code**: `internal/telemetry/` (`tags.go` for the new tags, a new
  environment/context builder, `scrubber.go` for cwd/home stripping,
  `sentry.go`/`sink.go` for structured event contexts), `telemetry.Config` and
  `cmd/rally/main.go` data-dir plumbing for a small machine-id helper (new file under
  `internal/telemetry/`), and the `CaptureFailure` call sites / relay span tagging in
  `internal/relay/runner.go` (relay span setup near `Run`, all-frozen route failure,
  terminal try failure, and unfinalized-agent capture).
- **Behavior**: when telemetry is disabled (no DSN, or `RALLY_TELEMETRY=0`) nothing
  changes — the machine-id file is only written when the sink is active. When enabled,
  captured failures gain environment + identity + state context.
- **Privacy**: no new PII is intentionally transmitted. The machine hash is random and
  anonymous; cwd is home-stripped; hostname/username/IP are never sent; bounded
  provider raw-signal text is scrubbed before sending. The change extends, not relaxes,
  the existing `before_send` scrubber.
- **Out of scope**: defining the failure taxonomy itself, and which failures become
  Issues vs spans (owned by `improve-error-categorisation` and the existing telemetry
  spec respectively — this change *consumes* the taxonomy, it does not author it); any
  non-Sentry backend; sampling-rate changes.
- **Coordination**: this change lands **after** `improve-error-categorisation` and
  consumes its typed `FailureCategory`, `quota_scope`, and reset evidence rather than
  re-deriving a failure class. The resilience-state names (active / probation / frozen /
  benched) come from `harden-relay-run-lifecycle`'s vocabulary, with `benched` added by
  `improve-error-categorisation`'s reset-driven usage-limit handling; reuse those terms
  rather than inventing new ones.
