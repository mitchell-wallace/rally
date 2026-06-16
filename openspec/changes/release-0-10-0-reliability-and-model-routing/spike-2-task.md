# Spike 2 Task: opencode Usage-Limit Detection Gap

## Purpose

Rally does not correctly detect when an opencode subscription provider hits a
5-hour / weekly / monthly usage limit. The reported symptom: `zai-coding-plan`
(currently on a 5h limit) and `opencode-go` (currently on a monthly limit) burn
the run/retry budget instead of benching the exhausted quota scope, and the same
gap may apply to all providers.

This spike must establish, from live evidence, **exactly what error signatures
opencode emits for these providers when a limit is hit**, **how Rally currently
classifies them**, and **why the existing `usage_limit` path does not fire** —
then recommend the concrete change for the 0.10.0 release.

## Questions to Answer

1. What is the exact provider error signature opencode emits for
   `zai-coding-plan` (5h) and `opencode-go` (monthly/weekly) usage limits, both
   in the `--format json` event stream Rally parses and in opencode's own logs?
2. How does Rally classify these failures today (category, level, bench/no-bench)?
3. Why does `reliability.ParseOpencodeError` not classify them as `usage_limit`?
4. Does the reset window (5h / weekly / monthly) parse into a bench deadline, or
   does Rally fall back to the default bench duration?
5. Is this opencode-specific, or a general all-provider detection/timing gap?
6. What does `sentry` show for these runner failures?

## Required Checks

### 1. Live error signatures

Drive real `opencode run … --format json --model <provider>/<model>` against the
currently-limited providers and capture stdout/stderr exactly as Rally's
`OpenCodeExecutor` would see it. Cross-check against opencode's server log. Use a
disposable workspace.

### 2. Classification path

Read `internal/reliability/opencode.go`, `category.go`, `patterns.go`, and the
consumption path in `internal/relay/runner.go` / `route_runtime.go` /
`routing/quota_scope.go`. Determine which branch the live signatures hit and why.

### 3. Reset parsing

Trace each provider's reset phrasing through `parseResetsIn` /
`parseRetryAfterSeconds` / `benchResetDeadline`. Record whether a correct bench
deadline is produced.

### 4. Sentry evidence

Use the authenticated `sentry` CLI (never `sentry-cli`). Find the terminal
failures for `opencode:zai-coding-plan/*` and `opencode:opencode-go/*` runners
and record their `failure_category`, `level`, and `event_kind` tags.

## Deliverable

A concise report `spike-2-report.md` in this change folder, with the live
signatures, the root-cause classification trace, Sentry findings, and
recommended edits to `draft.md`, `proposal.md`, `design.md`, `tasks.md`, and the
spec deltas.

## Constraints

- Do not implement product changes during this spike.
- Prefer a disposable repo/workspace for live opencode behavior.
- Use `sentry`, never `sentry-cli`.
- If a provider's limit has reset and can no longer be reproduced live, record
  the exact blocker and rely on opencode server logs + Sentry for the signature.

## Follow-up: confirm finding A (exact emitted JSON error event)

Spike 2 could not capture the exact `{"type":"error",...}` event opencode emits
to the `--format json` stream for a subscription-provider usage limit, because
opencode retries the provider error internally (`AI_RetryError: Failed after 3
attempts`) and emitted **zero stdout** within budget — two 180s runs and one 320s
run all stayed silent. The server log carried the real text, but the matcher in
task 8.1 needs the precise `error.name` and `error.data.message` that reach the
parser. This must be confirmed before task 8.1 is finalized.

### What to capture

The full JSON line opencode writes to **stdout** when the limit fires, i.e. the
exact shape of:

```json
{"type":"error","error":{"name":"<?>","data":{"message":"<?>","ref":"<?>"}}}
```

Specifically record: the `error.name` (is it `UnknownError`, `AI_APICallError`,
`AI_RetryError`, `ProviderModelError`, or opencode's native `UsageLimitError`?),
the exact `error.data.message` (does it contain the human "usage limit reached…"
text, or a generic "Unexpected server error. Check server logs for details."?),
and whether the reset phrasing survives into `data.message`.

### How to capture

1. Use a provider whose limit is **hard** (e.g. `opencode-go` monthly, "Resets in
   N days") so it cannot reset out from under the capture, as `zai-coding-plan`'s
   5h window did during spike 2.
2. Reproduce exactly as Rally's `OpenCodeExecutor` invokes it, capturing stdout:
   ```
   mkdir -p /tmp/oc-spike2 && cd /tmp/oc-spike2
   timeout 900 opencode run "Print exactly: PONG" \
     --format json --model opencode-go/<limited-model> --dir /tmp/oc-spike2 \
     > out.json 2> err.txt; echo "EXIT=$?"
   ```
   Use a timeout well past opencode's internal retry/backoff (≥ 900s), since 320s
   was not enough. Run it in the background and let it finish; the goal is to
   observe the eventual error event, not to bound it to Rally's stall window.
3. If stdout is still empty when the process exits/times out, fall back to driving
   the opencode **server HTTP API** directly (it is the same backend `opencode
   run` connects to) and capture the structured error response, or read the
   matching error object out of opencode's storage/DB for that session. The point
   is the structured `error` object, not the human spinner output.
4. Cross-check the captured `error.name`/`error.data.message` against the server
   log line for the same session/timestamp in
   `~/.local/share/opencode/log/opencode.log`.

### Deliverable

Append the captured JSON line(s) and the resolved `error.name` /
`error.data.message` to `spike-2-report.md` (§1.2), and confirm or correct task
8.1's matcher list (`usage limit reached`, `monthly usage limit`, `usage limit
reached for`, plus the wrapper names) so it matches the real event. If the
message is generic and the text lives only in the server log, that confirms the
task 8.3 server-log-tail evidence path is required, not optional.

### Constraints (same as above)

- No product changes; disposable workspace; `sentry` not `sentry-cli`.
- Clean up any background opencode processes and the temp workspace afterward.
