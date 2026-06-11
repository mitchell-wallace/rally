## Context

The telemetry sink (`internal/telemetry/`) is already wired: `Init` resolves a DSN
and returns a `Sink`; `SentrySink` implements spans (`StartSpan`), per-try logs
(`EmitTryLog`), failure capture (`CaptureFailure`), and bounded `Flush`. A
`before_send` scrubber (`scrubber.go`) drops sensitive keys and truncates oversized
values. Correlation tags are built in `tags.go` from `EventInfo`. The current sink
contract only accepts tags on `CaptureFailure`, so this change must extend the
telemetry API with a structured event/context input instead of hiding context in tags.

Failures are captured at three sites in `internal/relay/runner.go`: relay stall
(all agents frozen), per-try failure, and "agent exited without finalizing". The
relay-level span is tagged when `Run` starts the relay span. Avoid locking the plan to
line numbers here; the implementation should update the named blocks in the current
file.

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
- Release binaries can report to Rally's product Sentry project by default without
  requiring per-container environment injection.

**Non-Goals:**
- Changing which failures are captured as Issues vs recorded as spans (owned by the
  existing telemetry taxonomy requirement).
- A non-Sentry backend or sampling changes.
- Transmitting prompt/transcript content (the scrubber's existing guarantee stands).
- Building a general secret-management system for container managers.

## Decisions

**1. Run-environment context as a single `rally` context block.**
Build it once at sink init (values are process-stable) and attach to events:
`version` (`buildinfo`), `go_os`, `go_arch`, `term` (`$TERM` or `"non-tty"` when
stdout is not a terminal, using `golang.org/x/term.IsTerminal(int(os.Stdout.Fd()))`).
Hostname, username, and network identity are deliberately excluded. Sentry's own
hostname field must also be neutralized: set `ClientOptions.ServerName` to a static
non-host value (or clear `event.ServerName` in `scrubEvent`) and test that the outgoing
event has no host-derived `server_name`.

**2. Default product DSN with override and kill-switch precedence.**
Add `var DefaultSentryDSN = ""` in `cmd/rally/main.go` next to `Version`. Release
builds inject it with an ldflag from a GitHub Actions secret, e.g.
`-X main.DefaultSentryDSN={{ .Env.RALLY_SENTRY_DSN }}`. Wire the release workflow to
expose that secret only to GoReleaser. The effective DSN precedence is:
`RALLY_TELEMETRY=0` disables telemetry regardless of any DSN; `SENTRY_DSN` env var
overrides everything else; `.rally/config.toml` `sentry_dsn` overrides the baked
default; `DefaultSentryDSN` is the fallback; an empty effective DSN leaves telemetry
disabled. This preserves the existing local override behavior while making product
release binaries work out of the box.

Initialize the active telemetry sink only from the relay command path after the
workspace config and data directory have been resolved. Process-start defaults remain
`NoopSink` plus an empty machine id, so mechanical commands (`--help`, `--version`,
`update`, and other commands that do not run a relay) do not create the machine-id file,
open a Sentry client, or send telemetry solely because a release binary has a baked DSN.
The relay path still uses the same precedence and still defers cleanup/flush for the
duration of the command.

**3. Structured telemetry event input, not ad hoc scope mutation.**
Extend `telemetry.Sink` with a structured failure/event input that can carry filterable
tags separately from context blocks (for example a `FailureEvent` with `Tags` and
`Contexts`). Update `NoopSink`, `SentrySink`, and test mocks together. `SentrySink`
should set scalar filter fields with `scope.SetTag` and attach nested objects with
`scope.SetContext`; relay spans should receive the same `rally` context via
`Span.SetData` or the Sentry equivalent. This keeps the tag/context split testable and
prevents implementers from smuggling context-only data into high-cardinality tags.

**4. Anonymous machine-local hash, persisted once.**
On first active-telemetry run, generate a random 128-bit value (`crypto/rand`), hex-
encode it, and write it to `<dataDir>/machine-id` (0600). Subsequent runs read it.
It is **not** derived from any machine attribute, so it cannot be reversed to a host
or user; it only lets a machine's events be grouped over time. The file is written
only when the sink is active, so disabled telemetry leaves no trace. If the file is
unreadable/unwritable, fall back to an ephemeral per-process value (still anonymous).
Because `telemetry.Init` currently receives only the DSN, add `DataDir` to
`telemetry.Config` and pass the resolved data dir from `cmd/rally/main.go` before
initializing the active sink. If no data dir is available, use an ephemeral value rather
than creating files in an implicit location.

**5. Globally-unique relay identity.**
`relay_guid = <machine-id-prefix>-<repo-key>-<YYYYMMDD>-<relay_id>`, where `repo-key`
is the same hashed repo identifier already emitted as the `repo` tag, the date comes
from the relay's `StartedAt`, and `relay_id` is the existing workspace-local counter.
Attach `relay_guid` and `relay_started_at` (RFC3339) at the relay span and on every
failure capture, and keep emitting the local `relay_id` tag for back-compat and
within-workspace correlation with `summary.jsonl`. Emit `machine_id_prefix` as the
filterable machine tag and put the full anonymous machine id only in the `rally`
context. The prefix gives enough grouping for routine triage while reducing Sentry tag
cardinality and avoiding a full long-lived pseudonymous identifier as an indexed tag.
Rationale for a composite over a random UUID: it stays human-greppable and ties back
to the local store, while the machine prefix + repo key + date guarantee cross-machine
and cross-repo uniqueness.

**6. Username-stripped working directory.**
Attach `cwd` with the user's home prefix collapsed to `~` (compare against
`os.UserHomeDir()`), e.g. `~/Documents/Mycode/rally-2`. This shows the path shape for
triage without the username. Implement the stripping in `scrubber.go` (or a helper it
calls) so it is enforced centrally and unit-tested. Apply it recursively to string
values in event contexts, breadcrumbs, and span data, including free-text raw-signal
fields, so home paths embedded inside provider text are collapsed too. The existing
`repo` path-hash tag is retained.

**7. Agent state on failure, with site-specific source rules.**
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

The three capture sites have different source data:
- Terminal try failure: attach `attempt`, `max_attempts`, the latest
  `failureCategory`, `resetEvidence`, quota scope, and the selected runner's current
  resilience state from the route selection (`active`, `probation`, `benched`, or
  `frozen` where known).
- Unfinalized-agent capture: classify as `incomplete_finalization`; include run/runner
  tags, `max_attempts`, and the last known attempt number if available. Omit reset/quota
  fields unless upstream evidence exists.
- Relay stall/all-frozen capture: this is relay-level, not a try failure. Attach
  `agent_state=frozen` and relay/global context, but omit try-only fields such as
  `attempt`, `max_attempts`, raw signal, and quota reset evidence.

**8. Raw limit-signal corpus for parser validation.**
When a failed try's category is a provider-limit signal (`usage_limit`,
`short_rate_limit`, `provider_overloaded`), emit the bounded
`FailureEvidence.RawSignal` and `Message` in a `failure_evidence` context block
(context, not tags — they are free text, not filterable scalars). Emit this as an
info-level diagnostic event even when the failure is not otherwise captured as an
operator-worthy Issue. If the same failure is operator-worthy, the Issue event may also
carry the same context, but the diagnostic emission is the contract that ensures corpus
coverage.

Purpose: the per-harness evidence parsers in `internal/reliability/`
(`ParseClaudeError`, `ParseCodexError`, `ParseGeminiError`, `ParseOpencodeError`)
encode provider response shapes partly from memory; capturing the exact raw
shapes observed in the field builds the corpus that `improve-harness-consistency`
needs to validate and normalize those parsers against real data (and to retire
signatures that never occur). `RawSignal` is already bounded (<=256 runes) and contains
provider error text. Treat it as untrusted free text anyway: run it through the same
recursive scrubber as other contexts, collapse home paths inside the string, preserve
the existing sensitive-key redaction, and add fixtures proving prompt/transcript fields
are not attached under `failure_evidence`.

Use a structured event API for this path rather than overloading `CaptureFailure`.
Sentry should receive the diagnostic at info level with tags such as
`event_kind=limit_signal`; alerting can ignore that event kind/level while Discover
queries can aggregate the raw provider shapes.

## Risks / Trade-offs

- **Machine-id file is user-deletable / not portable** → acceptable: deleting it just
  starts a new anonymous identity; portability across machines is explicitly not a
  goal. Never block a run on the file.
- **`relay_guid` composite collides if the clock is wrong** → the machine prefix makes
  cross-machine collision negligible; within a repo the local `relay_id` is already
  unique, and the repo key prevents same-machine cross-repo collisions.
- **Home-prefix stripping misses non-home absolute paths** (e.g. `/srv/work`) → those
  expose no username, so they are lower-risk; still run all path-shaped fields through
  the collapse helper as defense-in-depth and document that only the home prefix is
  guaranteed stripped.
- **Context built at init goes stale if the process changes TTY** → the environment
  values are process-stable in practice; rebuilding per-event is unnecessary cost.
- **Baked DSN can be abused if copied from the binary** → acceptable for Rally's current
  product posture; DSNs are client-side ingestion endpoints, not privileged API
  secrets. Keep `RALLY_TELEMETRY=0` as the user kill switch, and treat Sentry key/project
  rotation or project closure as the deprecation path if the baked endpoint needs to be
  retired.
- **Full machine id as a Sentry tag may be high-cardinality** → use only
  `machine_id_prefix` as a tag and place the full anonymous id in context.
- **Sentry may populate host-derived `server_name` by default** → explicitly set or
  scrub it so the no-hostname guarantee covers top-level Sentry event fields, not only
  Rally's custom contexts.
- **A baked DSN can create side effects for informational commands** → initialize
  telemetry only in relay execution. Commands that merely report local information
  remain no-op for telemetry even in release binaries.
- **Low-severity limit diagnostics can add Sentry volume** → keep them bounded,
  scrubbed, tagged with `event_kind=limit_signal`, and info-level so they do not create
  operator alerts.
