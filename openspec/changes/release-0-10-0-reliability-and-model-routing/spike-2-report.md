# Spike 2 Report: opencode Usage-Limit Detection Gap

Date: 2026-06-16. All signatures below are from live opencode `1.17.7` on this
machine with real auth for `zai-coding-plan` (5h limit, resetting ~18:29:51
AEST during the spike) and `opencode-go` (monthly limit, "Resets in 7 days"),
opencode's own server log, and the New Relic APM dashboard against
`moved-by-the-word/rally`.

## TL;DR

opencode subscription-provider usage limits are **not** classified as
`usage_limit`. They land as `agent_error` ("failed: harness error"), so the
quota scope is **never benched** and Rally keeps re-selecting the exhausted
provider until the run dies. Three compounding causes: (A) a stall/timeout race
that usually kills the try before the limit text is ever emitted; (B) the parser
keys on opencode-native error names that the real provider errors never use; and
(C) the reset phrasings these providers use do not parse, so even a correctly
categorized limit would bench for the wrong (default 5h) window. The benching
machinery itself is correct and provider-agnostic — the gap is entirely upstream
classification + reset parsing + signal observability.

## 1. Live provider error signatures

### 1.1 What opencode's server log carries (the ground truth)

From `~/.local/share/opencode/log/opencode.log`, distinct `error.error=` values
for the two providers (extracted live):

**opencode-go (monthly):**
```
AI_APICallError: Monthly usage limit reached. Resets in 7 days. To continue
  using this model now, enable usage from your available balance:
  https://opencode.ai/workspace/wrk_.../go
AI_RetryError: Failed after 3 attempts. Last error: Monthly usage limit reached.
  Resets in 7 days. To continue using this model now, enable usage from your
  available balance: https://opencode.ai/workspace/wrk_.../go
```

**zai-coding-plan (5h):**
```
AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at
  2026-06-16 18:29:51
AI_RetryError: Failed after 3 attempts. Last error: Usage limit reached for
  5 hour. Your limit will reset at 2026-06-16 18:29:51
```
(zai also emits transient siblings: `AI_APICallError: Network error, error id:
…, please try again later`, `AI_APICallError: The service may be temporarily
overloaded, please try again later`, and raw `ECONNRESET` to
`https://api.z.ai/api/coding/paas/v4/chat/completions`.)

Two things stand out: opencode **retries internally** (`AI_RetryError: Failed
after 3 attempts`) before surfacing anything, and the reset is phrased as either
a space-separated span ("Resets in 7 days") or an **absolute timestamp** ("will
reset at 2026-06-16 18:29:51") — neither matches Rally's current reset parser
(see §3).

### 1.2 What the `--format json` stream emits (what Rally's parser sees)

This is the decisive unknown, and finding **A** is why: across three live
reproductions —
`opencode run "Print exactly: PONG" --format json --model zai-coding-plan/glm-5.2`,
`… --model opencode-go/glm-5.1`, and a 320s `… --model opencode-go/deepseek-v4-pro`
— opencode emitted **zero bytes to stdout** while spinning through its internal
`AI_RetryError` backoff. The first two hit a 180s timeout empty; the third was
still spinning with no output after 7+ minutes. So in practice the structured
error event frequently **never reaches** `ParseOpencodeError` at all.

When opencode *does* surface a provider error to the JSON event, the evidence
points to it arriving under opencode's catch-all wrapper rather than a native
limit type:

- opencode's `NamedError` taxonomy (from the binary) is dominated by
  `UnknownError` (33 occurrences) and `MessageAbortedError` (12); the native
  `UsageLimitError` (2) / `QuotaExceededError` (4) types exist but are
  opencode's **own** Zen-account accounting, not provider passthroughs.
- `internal/reliability/opencode_test.go`'s real-looking `UnknownError` fixture
  carries `data.message = "Unexpected server error. Check server logs for
  details."` — i.e. the actual "usage limit" text stays in the server log and
  the JSON event's `data.message` is generic.
- When the message *is* carried (via the `AI_RetryError` wrapper), the literal
  is `"Failed after 3 attempts. Last error: …usage limit…"` — the substring is
  present but the wrapper `name` (`AI_RetryError` / `AI_APICallError`) is not
  one the parser recognizes.

**Confirmed Follow-up Capture Findings:**
Across multiple manual captures (including runs timed out past 30s and 120s with full permissions allowed), `opencode run` consistently emitted **zero bytes to stdout and stderr** for these stream errors when using a limited provider, and exited with code 124 (timeout). The DB session logs (`message`, `part`, `event`) also carried no error fields.

This confirms **Finding A**: subscription-provider usage limits are completely masked on stdout/stderr, making the **task 8.3 server-log-tail evidence path strictly required**, rather than optional.

If the error *were* successfully serialized to the client JSON stream, it would wrap the provider errors under the `AI_APICallError` or `AI_RetryError` names. The exact expected JSON lines would be:

**opencode-go (monthly):**
```json
{"type":"error","error":{"name":"AI_APICallError","data":{"message":"Monthly usage limit reached. Resets in 7 days. To continue using this model now, enable usage from your available balance: https://opencode.ai/workspace/wrk_01KE8J1VN675HZTWC005M2JCES/go","ref":"err_oc_go"}}}
```
or
```json
{"type":"error","error":{"name":"AI_RetryError","data":{"message":"Failed after 3 attempts. Last error: Monthly usage limit reached. Resets in 7 days. To continue using this model now, enable usage from your available balance: https://opencode.ai/workspace/wrk_01KE8J1VN675HZTWC005M2JCES/go","ref":"err_oc_go"}}}
```

**zai-coding-plan (5h):**
```json
{"type":"error","error":{"name":"AI_APICallError","data":{"message":"Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51","ref":"err_oc_zai"}}}
```
or
```json
{"type":"error","error":{"name":"AI_RetryError","data":{"message":"Failed after 3 attempts. Last error: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51","ref":"err_oc_zai"}}}
```

These wrappers (`AI_APICallError` / `AI_RetryError`) and their specific substrings must be added to task 8.1's matcher list so that the parser can classify them correctly when they are read from either the JSON stream (if it does emit) or the server log tail.


## 2. How Rally classifies these today (Telemetry)

New Relic APM dashboard, project `moved-by-the-word/rally`, terminal failures
in the last few hours:

| Issue | Runner | failure_category | event_kind | level | Title |
|-------|--------|------------------|-----------|-------|-------|
| RALLY-Q | `opencode:zai-coding-plan/glm-5.2` | **agent_error** | (none) | error | failed: harness error |
| RALLY-K | `opencode:opencode-go/kimi-k2.7-code` | **agent_error** | (none) | error | failed: harness error |
| RALLY-D | `opencode:opencode-go/deepseek-v4-pro` | **agent_error** | (none) | error | failed: harness error (attempt 3/5) |

Contrast — codex short rate limits **are** correctly tagged (RALLY-2, event
`159a11b1a13b`): `event_kind=limit_signal`, `failure_category=short_rate_limit`,
`level=info`. So the taxonomy/telemetry plumbing works; opencode usage limits
simply never reach the `usage_limit` branch.

RALLY-Q's breadcrumb trail shows the consequence directly: the same exhausted
`opencode:zai-coding-plan/glm-5.2` runner is re-selected attempt after attempt
(`fail_reason=harness error`, attempt 1 → 2) with no bench — exactly the
budget-burn the user reported. There is no `benched` event for the
`opencode:zai-coding-plan` or `opencode:opencode-go` quota scopes anywhere in
the incident.

## 3. Root cause (three compounding gaps)

### A. Timing / stall race — dominant, and provider-general

`OpenCodeExecutor.Execute` (`internal/agent/opencode.go:84-98`) only attaches
evidence from `ParseOpencodeError(string(out), model)` after the subprocess
returns. But opencode retries provider errors internally and emits nothing
meanwhile. Rally's `DefaultStallThreshold` is **180s**
(`internal/reliability/stall.go:16`), and opencode does not support a liveness
probe (`LivenessProbeSupported() == false`). So the try is stall-killed (or
context-times-out) at ~180s — *before* any usage-limit text is emitted. The log
tail then contains no "usage limit" string, so classification falls through
priority 4 to the priority-5 default: `agent_error` (`patterns.go:358-365`).
This is what produces the APM `agent_error` tags, and it applies to **any**
harness whose CLI retries provider errors internally (codex/claude/gemini), not
just opencode.

### B. Error-name / wrapper gap

`ParseOpencodeError` (`internal/reliability/opencode.go:76-83`) enters the
`usage_limit` branch only on opencode-native tokens —
`name ∈ {usagelimit, quotaexceeded, resourceexhausted}` or
`message ∈ {usage limit, quota exceeded, resource_exhausted}`. The real provider
errors arrive as the Vercel-AI-SDK wrappers `AI_APICallError` / `AI_RetryError`,
surfaced to the JSON event as opencode's catch-all `UnknownError` (§1.2). Even
when the message substring "usage limit" is present, the unrecognized wrapper
name plus the generic-message possibility make the match unreliable. The
`opencodeResetsInRe`/`opencodeRetryAfterRe` regexes in this file are also unused
by the usage-limit branch — reset timing is delegated to `parseResetsIn`.

### C. Reset-format parse gap

`parseResetsIn` reuses the gemini regex `geminiResetsInRe = Resets\s+in\s+(\S+)`
(`internal/reliability/antigravity.go:15,57-64`) then `parseGoDuration`:

- opencode-go **"Resets in 7 days"** → `(\S+)` captures `"7"` (units are after a
  space) → `parseGoDuration("7")` fails (no unit) → **0**.
- zai **"Your limit will reset at 2026-06-16 18:29:51"** → no "resets in" phrase
  at all; it is an **absolute timestamp**. Neither `parseResetsIn` nor
  `parseRetryAfterSeconds` handle "reset at <timestamp>".

So `ResetAfter = 0`, `ResetAt = nil`, and `benchResetDeadline`
(`internal/relay/runner.go:1187-1197`) falls back to `BenchDefaultDuration = 5h`
(`internal/relay/constants.go:31`). A **monthly/weekly** limit benched for only
5h gets re-probed and re-fails every 5h, wasting the lane; the spec's
"re-probe still exhausted re-benches a fresh window" then loops it. (For zai's 5h
window the default is coincidentally close, but it is not derived from the stated
reset and is wrong if the limit is hit late in the window.)

### D. The benching machinery is correct — only the inputs are wrong

`routing.QuotaScope` already resolves `opencode:zai-coding-plan` and
`opencode:opencode-go` as per-provider bench keys
(`internal/routing/quota_scope.go:24-28`), and the runner benches the whole
scope on `Category == CategoryUsageLimit` (`internal/relay/runner.go:794-797`)
using `benchResetDeadline`. None of this fires because the failure never carries
`CategoryUsageLimit`. Fixing A–C is sufficient; no bench-side change is needed.

## 4. Recommended change (the "extra set" for 0.10.0)

A new reliability work group, **"opencode (and general) usage-limit detection"**:

1. **Broaden opencode usage-limit classification.** In `ParseOpencodeError`,
   match the live signatures across the `AI_APICallError` / `AI_RetryError` /
   `UnknownError` wrappers, against `name`, `data.message`, **and** the raw
   signal: add substrings `"usage limit reached"`, `"monthly usage limit"`,
   `"usage limit reached for"`. Keep `usage_limit` winning over `short_rate_limit`
   when both could match (as the existing priority test asserts).
2. **Parse opencode reset phrasings.** Add reset parsing for space-separated
   spans (`"Resets in 7 days" / "… 5 hour" / "… 30 minutes"`) and absolute
   timestamps (`"reset at 2026-06-16 18:29:51"`, `"will reset at …"`), feeding
   `ResetAfter` / `ResetAt`. This is opencode-specific phrasing; do not overload
   the gemini regex.
3. **Make the signal observable despite opencode's internal retry (finding A).**
   Recommended: when an opencode try ends without a usable result (stall kill or
   error event), have the executor read the tail of opencode's server log for
   that session and surface any provider usage-limit signature as
   `FailureEvidence`, even when the JSON stream stalled. This reliably carries
   the structured provider error that the JSON stream withholds. (Alternatives:
   shorten/observe the interim error; treat a silent-backoff stall on a
   subscription provider as usage-limit-suspected. Document the trade-off; the
   server-log-tail approach is the most robust and lowest-risk.)
4. **Generalize the lesson.** Note in the taxonomy/evidence requirement that any
   harness with internal provider-error retry can mask usage limits behind a
   stall; the observability fix (3) is the general mitigation, and codex/claude
   parsers already cover their own phrasings when text reaches stderr.
5. **Tests.** Add opencode fixtures for the exact zai and opencode-go signatures
   (both `AI_APICallError` and `AI_RetryError` wrappers, and the `UnknownError`
   generic-message case) asserting `CategoryUsageLimit` and the correct
   `ResetAfter`/`ResetAt`; add a server-log-tail evidence test for the
   stall-then-limit path.

## 5. Recommended edits to change artifacts

### draft.md
- Add a sixth grouped behavior: **opencode usage-limit detection & reset parsing**
  (zai-coding-plan / opencode-go and general providers), with the three root
  causes summarized.
- Extend "Current signal quality (research)" with the §2 telemetry rows
  (RALLY-Q/K/D as `agent_error`) and the §1 live signatures.

### proposal.md
- Add a target outcome: opencode subscription-provider usage limits are detected
  and bench the quota scope until the parsed reset (not the 5h default).
- Add a decision under "Research-backed limit handling" capturing the wrapper /
  reset-format / stall-race findings and the server-log-tail observability fix.
- Add `internal/reliability` and `internal/agent/opencode.go` to in-scope paths.

### design.md
- New section "opencode usage-limit detection" documenting findings A–C, the
  exact signatures, and the chosen observability approach (server-log-tail
  evidence), with the QuotaScope/benchResetDeadline path it feeds into.

### tasks.md
- New task group (e.g. §8) covering: broaden classification, reset parsing, the
  stall/observability mitigation, fixtures/tests, and a follow-up to confirm the
  precise emitted JSON `data.message` (finding A caveat).

### specs
- `executor/spec.md`: ADD a requirement for opencode provider usage-limit
  evidence (the wrapper-name signatures, the reset formats, and surfacing
  server-log evidence when the JSON stream stalls).
- `relay-runner/spec.md`: a scenario clarifying that a usage-limit masked by an
  internal-retry stall must still bench the quota scope (not classify
  `agent_error`).

## 6. Constraints respected

- No product code changed; only spike artifacts written.
- Live reproductions used a disposable `/tmp/oc-spike` workspace, since removed;
  no opencode processes left running.
- zai's 5h limit reset (~18:29:51) mid-spike, so its live JSON error could not be
  re-triggered after reset; the signature is taken from opencode's server log and
  New Relic APM. opencode-go's monthly limit remained active but never emitted to stdout
  within a 320s budget (finding A).

### Additional second-pass confirmation

I repeated the follow-up in fresh workspaces at 19:42–19:52 local time with the exact
requested `--format json --model opencode-go/glm-5.1` flow and longer wait windows.

- Fresh-run files still showed `out.json=0` and `err.txt=0` for baseline captures.
- `timeout 950` and `timeout 120` runs both completed without producing `type:error`
  on stdout.
- With `--print-logs --log-level DEBUG`, the only meaningful lines were startup/stream
  lifecycle logs and the same two error signatures in log text:
  - `AI_APICallError: Monthly usage limit reached...Resets in 7 days...`
  - `AI_RetryError: Failed after 3 attempts. Last error...Resets in 7 days...`
- `opencode db` for each matching session still only contained normal session/message metadata
  (`agent-switched`, `model-switched`, `message.*`) and no structured error payload.

These re-runs are intentionally confirming, not divergent: Finding A still stands as-is.

### Third-pass confirmation (live log re-inspection, 2026-06-16 20:58)

Re-inspected `~/.local/share/opencode/log/opencode.log` (opencode `1.17.7`)
directly to confirm the exact carrier format and resolve finding A's matcher
caveat (task 8.5) before implementation. This closes the follow-up: the
structured error **never reaches stdout** — the server log is the authoritative
source, and these tunings are now locked for tasks 8.1–8.3.

**1. The server log carries the error as a flat field, not nested JSON.** Every
limit lands on a single line keyed by `error.error="<Wrapper>: <message>"`:

```
timestamp=2026-06-16T08:28:23.924Z level=ERROR run=ab168f45 message="stream error" \
  providerID=zai-coding-plan modelID=glm-5.2 session.id=ses_13074405affeIZLEONNv4X9fAe \
  small=false agent=build mode=primary \
  error.error="AI_APICallError: Usage limit reached for 5 hour. Your limit will reset at 2026-06-16 18:29:51"
```

There is no `{"error":{"name":...,"data":{"message":...}}}` object in the log;
the wrapper name and human text are concatenated inside the `error.error=`
string. So the **matcher (task 8.1) must run against this flat `error.error=`
value** (and the JSON event's `name`/`data.message` if it ever appears), not a
nested `data.message`. Distinct limit shapes in the whole log (counts):
`AI_APICallError`/`AI_RetryError` × `Monthly usage limit reached. Resets in 7
days. …` (16/16) and `… Usage limit reached for 5 hour. Your limit will reset at
<ts>` (13/2). No native `UsageLimitError`/`QuotaExceededError` for these
provider limits — confirming finding B.

**2. Session can be correlated to the run without stdout (task 8.3 handle).**
opencode logs a session-creation line that carries the workspace directory:

```
timestamp=2026-06-16T08:28:22.821Z level=INFO run=ab168f45 message=created \
  id=ses_13074405affeIZLEONNv4X9fAe slug=crisp-moon version=1.17.7 \
  directory=/tmp/oc-spike path=tmp/oc-spike …
```

Since `OpenCodeExecutor` sets `--dir <WorkspaceDir>` (and `cmd.Dir`), Rally can
recover the session id by matching `message=created … directory=<WorkspaceDir>`,
then scan that `session.id=`'s `message="stream error"` lines — **even though
stdout emitted the session id zero times** during the stall. Practical
correlation order for the tail: (a) `directory=<WorkspaceDir>` → session id;
(b) fallback `providerID=<provider>` within the try's wall-clock window;
(c) `--session <id>` when Rally already knows it (resumed runs). The provider is
the prefix of Rally's `--model` arg (`opencode-go/glm-5.1` → `opencode-go`).

**3. Only `opencode.log` carries `stream error` lines.** The per-run timestamped
`*.log` files in the same directory do not — so the tail target is exactly
`~/.local/share/opencode/log/opencode.log`.

**4. Both the build agent and the small title model hit the limit.** Each
session emits the limit twice: `small=false agent=build` (primary) and
`small=true agent=title` (opencode's title-generation small model). The tail
should match either; no special-casing needed.

**5. Reset timestamp has no timezone marker.** opencode's own `timestamp=` field
is UTC (`…Z`), but the message reset (`will reset at 2026-06-16 18:29:51`) carries
no zone. Empirically it is **local time**: the error fired at `08:28:23Z` (18:28
AEST) and reset at `18:29:51` — ~1.4 min later, consistent with the spike's
"reset mid-spike". So `ResetAt` parsing (task 8.2) SHOULD interpret the absolute
timestamp in `time.Local` (layout `2006-01-02 15:04:05`) and treat it as
approximate; benching slightly long is the safe direction.

**Finding A: resolved.** stdout stays empty through the internal-retry stall; the
structured provider error reaches only the server log, as the flat `error.error=`
field under the `AI_APICallError`/`AI_RetryError` wrappers. Task 8.5's caveat is
closed — the task 8.1 matcher list (`usage limit reached`, `monthly usage limit`,
`usage limit reached for`) is finalized, and the task 8.3 server-log-tail path is
**required, not optional**.
