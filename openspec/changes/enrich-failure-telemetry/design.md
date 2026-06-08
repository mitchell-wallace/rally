## Context

The telemetry sink (`internal/telemetry/`) is already wired: `Init` resolves a DSN
and returns a `Sink`; `SentrySink` implements spans (`StartSpan`), per-try logs
(`EmitTryLog`), failure capture (`CaptureFailure`), and bounded `Flush`. A
`before_send` scrubber (`scrubber.go`) drops sensitive keys and truncates oversized
values. Correlation tags are built in `tags.go` from `EventInfo`.

Failures are captured at three sites in `internal/relay/runner.go`: relay stall
(all agents frozen, `runner.go:433`), per-try failure (`runner.go:1418`), and
"agent exited without finalizing" (`runner.go:1489`). The relay-level span is tagged
at `runner.go:377`.

What is missing is **context that survives leaving the machine**: there is no record
of rally version, OS, terminal, or working directory, and every identifier
(`relay_id`, `try_id`) is a local store counter that collides across machines.

This change sequences **after** `improve-error-categorisation`, which introduces the
typed `FailureEvidence` on `TryResult`, the stable `FailureCategory` set, the
harness-aware `quota_scope`, and the reset-driven **benched** state. Telemetry consumes
those values for the failure-state snapshot rather than computing its own failure class,
so the assumed baseline below is the post-error-categorisation runner.

## Goals / Non-Goals

**Goals:**
- A captured failure carries enough environment + identity + agent state to triage
  without the originating machine.
- Relays are globally identifiable across machines and repos.
- Zero new PII: anonymous machine hash, home-stripped cwd, no hostname/username/IP.

**Non-Goals:**
- Changing which failures are captured as Issues vs recorded as spans (owned by the
  existing telemetry taxonomy requirement).
- A non-Sentry backend or sampling changes.
- Transmitting prompt/transcript content (the scrubber's existing guarantee stands).

## Decisions

**1. Run-environment context as a single `rally` context block.**
Build it once at sink init (values are process-stable) and attach to events:
`version` (`buildinfo`), `go_os`, `go_arch`, `term` (`$TERM` or `"non-tty"` when
stdout is not a terminal — reuse the same TTY detection the display path uses).
Hostname, username, and network identity are deliberately excluded.

**2. Anonymous machine-local hash, persisted once.**
On first active-telemetry run, generate a random 128-bit value (`crypto/rand`), hex-
encode it, and write it to `<dataDir>/machine-id` (0600). Subsequent runs read it.
It is **not** derived from any machine attribute, so it cannot be reversed to a host
or user; it only lets a machine's events be grouped over time. The file is written
only when the sink is active, so disabled telemetry leaves no trace. If the file is
unreadable/unwritable, fall back to an ephemeral per-process value (still anonymous).

**3. Globally-unique relay identity.**
`relay_guid = <machine-id-prefix>-<YYYYMMDD>-<relay_id>`, where the date comes from
the relay's `StartedAt` and `relay_id` is the existing local counter. Attach
`relay_guid`, `machine_id`, and `relay_started_at` (RFC3339) as tags/context at the
relay span (`runner.go:377`) and on every failure capture. Keep emitting the local
`relay_id` tag for back-compat and within-machine correlation with `summary.jsonl`.
Rationale for a composite over a random UUID: it stays human-greppable and ties back
to the local store, while the machine prefix + date guarantee cross-machine
uniqueness.

**4. Username-stripped working directory.**
Attach `cwd` with the user's home prefix collapsed to `~` (compare against
`os.UserHomeDir()`), e.g. `~/Documents/Mycode/rally-2`. This shows the path shape for
triage without the username. Implement the stripping in `scrubber.go` (or a helper it
calls) so it is enforced centrally and unit-tested, and so an unexpected absolute path
in any field can be run through the same collapse. The existing `repo` path-hash tag
is retained.

**5. Agent state on failure.**
Extend the failure-capture call sites to pass a small state struct alongside the
existing tags: `attempt`, `max_attempts`, the stable `failure_category` from
`improve-error-categorisation` (e.g. `usage_limit`, `short_rate_limit`,
`invalid_model`, `incomplete_finalization`, `agent_error`, …), and `agent_state`
(active / probation / frozen / benched) for the runner whose try failed. When the
category is a usage/quota failure, also attach the `FailureEvidence` reset fields as
scalar tags where present (`quota_scope`, `reset_at`/`reset_after`), so a captured quota
exhaustion is triageable without re-reading logs. Harness+model already arrive via the
`runner` tag. These are scalar tags, so they remain filterable in Sentry. Read the
category and evidence straight off the `TryResult.Evidence` / `StrategyDecision` that
`improve-error-categorisation` populates — do not re-classify here. Use the resilience
vocabulary (active / probation / frozen / benched) verbatim.

## Risks / Trade-offs

- **Machine-id file is user-deletable / not portable** → acceptable: deleting it just
  starts a new anonymous identity; portability across machines is explicitly not a
  goal. Never block a run on the file.
- **`relay_guid` composite collides if the clock is wrong** → the machine prefix makes
  cross-machine collision negligible; within a machine the local `relay_id` is already
  unique, so same-day collisions cannot happen.
- **Home-prefix stripping misses non-home absolute paths** (e.g. `/srv/work`) → those
  expose no username, so they are lower-risk; still run all path-shaped fields through
  the collapse helper as defense-in-depth and document that only the home prefix is
  guaranteed stripped.
- **Context built at init goes stale if the process changes TTY** → the environment
  values are process-stable in practice; rebuilding per-event is unnecessary cost.
