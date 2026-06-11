## 1. Run-environment context

- [ ] 1.1 Add an environment-context builder in `internal/telemetry/` returning `version` (`internal/buildinfo`), `go_os`, `go_arch`, and `term` (`$TERM` or `"non-tty"` via `golang.org/x/term.IsTerminal(int(os.Stdout.Fd()))`)
- [ ] 1.2 Extend the telemetry API with a structured failure/event input that carries scalar tags separately from context blocks; update `Sink`, `NoopSink`, `SentrySink`, and existing telemetry test mocks
- [ ] 1.3 Attach the `rally` context block on the relay span and on every captured failure through the structured API, not by flattening context into tags
- [ ] 1.4 Neutralize Sentry's host-derived `server_name` by setting a static non-host `ClientOptions.ServerName` or clearing `event.ServerName` in `scrubEvent`
- [ ] 1.5 Tests: context block carries version/os/arch/term; hostname/username are absent; top-level Sentry `server_name` is not host-derived

## 2. Anonymous machine-local hash

- [ ] 2.1 Add `DataDir` to `telemetry.Config`; pass the resolved data dir from `cmd/rally/main.go` into `telemetry.Init` before sink creation
- [ ] 2.2 Add a machine-id helper: read `<dataDir>/machine-id`; if absent, generate a random 128-bit value (`crypto/rand`), hex-encode, and write it `0600`
- [ ] 2.3 Only create the file when the sink is active; fall back to an ephemeral per-process value when the file is unreadable/unwritable or no data dir is available
- [ ] 2.4 Tests: id is stable across reads; absent file is created once; the value is not derived from any machine attribute; disabled telemetry writes no file; init with no data dir uses ephemeral identity

## 3. Globally-unique relay identity

- [ ] 3.1 In `tags.go` (or a new builder), compute `relay_guid = <machine-id-prefix>-<repo-key>-<YYYYMMDD>-<relay_id>` from the machine id, existing repo key, relay `StartedAt`, and local `relay_id`
- [ ] 3.2 Attach `relay_guid`, `relay_started_at` (RFC3339), and the decided machine identity field placement at the relay span and on each failure capture; keep emitting local `relay_id`
- [ ] 3.3 Resolve product call before implementation: full `machine_id` tag vs short prefix tag + full context-only id (default recommendation: short prefix tag + full context-only id)
- [ ] 3.4 Tests: guid is stable for a relay, unique across machine ids, repo keys, and dates; local `relay_id` still present; machine identity appears only in the decided tag/context locations

## 4. Username-stripped working directory

- [ ] 4.1 Add a home-prefix collapse helper in `internal/telemetry/scrubber.go` (compare against `os.UserHomeDir()`, replace with `~`)
- [ ] 4.2 Attach `cwd` (home-collapsed) to the `rally` context; retain the existing `repo` path-hash tag
- [ ] 4.3 Run string values in event contexts, breadcrumbs, and span data through recursive home-prefix collapse as defense-in-depth, including paths embedded inside free text
- [ ] 4.4 Tests: a home-prefixed cwd is collapsed to `~/...`; a non-home absolute path is left intact but exposes no username; a home path embedded in raw signal/message text is collapsed; the username never appears in the payload

## 5. Agent state on failure

- [ ] 5.1 Extend the failure-capture path to carry `attempt`, `max_attempts`, the stable `failure_category` (from `improve-error-categorisation`), and `agent_state` (active/probation/frozen/benched) as scalar tags, reusing the resilience vocabulary
- [ ] 5.2 When the category is a usage/quota failure, also attach the `FailureEvidence` reset fields (`quota_scope`, `reset_at`/`reset_after`) as scalar tags where present
- [ ] 5.3 Wire terminal try failures from the latest `TryResult.Evidence`/`StrategyDecision`, attempt number, max attempts, quota scope, and route/resilience state already in the run path; do not re-classify in telemetry
- [ ] 5.4 Wire unfinalized-agent captures as `failure_category=incomplete_finalization` with run/runner tags plus max attempts and last known attempt when available; omit reset/quota/raw-signal fields unless upstream evidence exists
- [ ] 5.5 Wire relay stall/all-frozen captures as relay-level events with `agent_state=frozen`; omit try-only attempt/reset/raw-signal fields
- [ ] 5.6 Tests: a captured usage-limit failure carries attempt/budget/category/quota_scope/reset/state tags; unfinalized captures carry `incomplete_finalization`; relay stall captures carry frozen state without try-only fields; agent-class failures recorded as spans/logs are unaffected
- [ ] 5.7 When the category is `usage_limit`/`short_rate_limit`/`provider_overloaded`, attach the bounded `FailureEvidence.RawSignal` + `Message` as a `failure_evidence` context block, so the exact provider limit-response shapes accumulate for the `improve-harness-consistency` parser-normalization pass
- [ ] 5.8 Tests: a limit-category capture carries the bounded raw signal through the scrubber; home paths inside raw signal/message are collapsed; prompt/transcript-looking sensitive fields are not attached; non-limit categories attach no raw-signal context

## 6. Docs

- [ ] 6.1 Document the new context fields, the anonymous machine id (and how to reset it by deleting the file), the decided tag/context placement for machine identity, and the privacy guarantees (no hostname/username/IP) in the telemetry/config docs
