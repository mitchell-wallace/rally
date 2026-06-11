## 1. Run-environment context

- [ ] 1.1 Add an environment-context builder in `internal/telemetry/` returning `version` (`internal/buildinfo`), `go_os`, `go_arch`, and `term` (`$TERM` or `"non-tty"` via the same TTY detection the display uses)
- [ ] 1.2 Attach the context as a `rally` context block on the relay span and on every `CaptureFailure`
- [ ] 1.3 Tests: context block carries version/os/arch/term; hostname/username are absent

## 2. Anonymous machine-local hash

- [ ] 2.1 Add a machine-id helper: read `<dataDir>/machine-id`; if absent, generate a random 128-bit value (`crypto/rand`), hex-encode, and write it `0600`
- [ ] 2.2 Only create the file when the sink is active; fall back to an ephemeral per-process value when the file is unreadable/unwritable
- [ ] 2.3 Tests: id is stable across reads; absent file is created once; the value is not derived from any machine attribute; disabled telemetry writes no file

## 3. Globally-unique relay identity

- [ ] 3.1 In `tags.go` (or a new builder), compute `relay_guid = <machine-id-prefix>-<YYYYMMDD>-<relay_id>` from the machine id, the relay `StartedAt`, and the local `relay_id`
- [ ] 3.2 Attach `relay_guid`, `machine_id`, and `relay_started_at` (RFC3339) at the relay span (`runner.go:377`) and on each failure capture; keep emitting local `relay_id`
- [ ] 3.3 Tests: guid is stable for a relay, unique across machine ids and dates; local `relay_id` still present

## 4. Username-stripped working directory

- [ ] 4.1 Add a home-prefix collapse helper in `internal/telemetry/scrubber.go` (compare against `os.UserHomeDir()`, replace with `~`)
- [ ] 4.2 Attach `cwd` (home-collapsed) to the `rally` context; retain the existing `repo` path-hash tag
- [ ] 4.3 Run path-shaped event fields through the collapse helper as defense-in-depth
- [ ] 4.4 Tests: a home-prefixed cwd is collapsed to `~/…`; a non-home absolute path is left intact but exposes no username; the username never appears in the payload

## 5. Agent state on failure

- [ ] 5.1 Extend the failure-capture path to carry `attempt`, `max_attempts`, the stable `failure_category` (from `improve-error-categorisation`), and `agent_state` (active/probation/frozen/benched) as scalar tags, reusing the resilience vocabulary
- [ ] 5.2 When the category is a usage/quota failure, also attach the `FailureEvidence` reset fields (`quota_scope`, `reset_at`/`reset_after`) as scalar tags where present
- [ ] 5.3 Wire the state at the three `CaptureFailure` sites (`runner.go:433`, `:1418`, `:1489`) by reading `TryResult.Evidence`/`StrategyDecision` already in scope — do not re-classify in telemetry
- [ ] 5.4 Tests: a captured usage-limit failure carries attempt/budget/category/quota_scope/reset/state tags; agent-class failures recorded as spans/logs are unaffected
- [ ] 5.5 When the category is `usage_limit`/`short_rate_limit`/`provider_overloaded`, attach the bounded `FailureEvidence.RawSignal` + `Message` as a `failure_evidence` context block, so the exact provider limit-response shapes accumulate for the `improve-harness-consistency` parser-normalization pass
- [ ] 5.6 Tests: a limit-category capture carries the bounded raw signal through the scrubber; non-limit categories attach no raw-signal context

## 6. Docs

- [ ] 6.1 Document the new context fields, the anonymous machine id (and how to reset it by deleting the file), and the privacy guarantees (no hostname/username/IP) in the telemetry/config docs
