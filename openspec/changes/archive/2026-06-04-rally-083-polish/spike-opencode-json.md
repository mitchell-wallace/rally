# Spike — opencode `run --format json` schema + in-container "crash" investigation

Date: 2026-06-01. Captured live against real opencode backends.

- Spike repo (host): `/tmp/rally-spike-oc`
- Working backend used for schema capture: inside container
  `ca5990ebcc41` (`dune-prayer-app-2-6c-default-agent-1`), opencode `1.15.11`,
  model `zai-coding-plan/glm-5.1`.
- Raw evidence saved under `spike-evidence/`:
  - `opencode-tooluse-events.jsonl` — full event stream for a tool-using run
  - `opencode-error-event-try167.jsonl` — a real opencode server-error event
  - `codex-usagelimit-try163.jsonl` — a codex usage-limit failure (for contrast)

This spike grew a second half: while capturing schema, the operator flagged that
opencode had been "crashing across providers" in container `ca5990ebcc41` earlier
the same day. Both findings are recorded below.

---

## Part A — Task 1.1/1.2: live JSON event schema

`opencode run "<prompt>" --format json` emits **newline-delimited JSON** (one event
per line). Each line has a **top-level `type`** and (for normal events) a nested
**`part` object with its own `type`**.

> **Gotcha:** the two `type` fields use *different casing conventions*.
> Top-level event `type` is **snake_case**; `part.type` is **kebab-case**.

### Observed events (tool-using run: "create a file then reply")

| line | top-level `type` | `part.type` | notable `part` fields |
|---|---|---|---|
| 1 | `step_start`  | `step-start`  | `id, messageID, sessionID` |
| 2 | `tool_use`    | `tool`        | `tool="write"`, `callID`, `state.status="completed"` |
| 3 | `step_finish` | `step-finish` | `reason`, `tokens{total,input,output,reasoning,cache}`, `cost` |
| 4 | `step_start`  | `step-start`  | — |
| 5 | `text`        | `text`        | `text="DONE_TOOLS"`, `time{start,end}` |

A pure text run (no tools) emits just `step_start` then `text`.

### Assistant text

Carried by **`type":"text"`** events; the text is at **`part.text`**. A run may emit
**multiple** `text` events (one per step) — they must be concatenated in order.

### Completion signal

**`type":"step_finish"`** (part.type `step-finish`) carries `reason` + `tokens` +
`cost`. This is the per-step completion marker. There is no separate top-level
"done" event; process exit-0 plus a final `step_finish` with no `error` event is the
reliable "finished cleanly" signal.

### Error events (the important one)

A failed call emits a **top-level `type":"error"` with NO `part`** and an `error`
object. Real capture (`try-167`, opencode gateway error):

```json
{"type":"error","timestamp":1780285834220,"sessionID":"ses_17eb1fcb4ffeaM4Hrx1qJbTQHa",
 "error":{"name":"UnknownError",
          "data":{"message":"Unexpected server error. Check server logs for details.",
                  "ref":"err_e558e8ba"}}}
```

The human-readable message lives at **`error.data.message`** (with `error.name` and
an optional `error.data.ref`). Note this is structurally different from the codex
executor's error shape (`{"type":"error","message":...}` + `{"type":"turn.failed"}`)
— don't share a parser between them.

### Diff against current `opencodeJSONEvent` (`internal/agent/opencode.go`)

Current struct only models `Type` + `Part.{Type,Text}`. Against live output:

| current code | reality | verdict |
|---|---|---|
| `ev.Type == "text"` ⇒ collect `ev.Part.Text` | matches live `text`/`part.text` | ✅ assistant text **is** captured correctly |
| `ev.Type == "tool_use" \|\| ev.Part.Type == "tool"` ⇒ count | matches (`tool_use`/`tool`) | ✅ tool counting works |
| (none) for `step_finish` | completion marker ignored | ⚠️ no explicit completion detection — relies solely on "did we collect any text" |
| (none) for `type":"error"` | error event ignored ⇒ `textParts` empty | ❌ **root bug**: falls through to `parseOpenCodeOutput`, which dumps **raw stdout** as `Summary` (proposal item 5) |

So the existing `text`/`tool` extraction is actually *correct*; the defect is purely
the **missing `error` branch** + **no completion marker**, which together produce the
raw-stdout-as-summary fallback.

---

## Task 1.3 — corrected extraction (input to task 3.2)

Parse decision, per line of the NDJSON stream:

1. **`type == "text"`** → append `part.text` to assistant text (ordered). This is
   the summary source.
2. **`type == "tool_use"` (or `part.type == "tool"`)** → increment tool count.
3. **`type == "step_finish"`** → mark "saw a clean step completion" (optionally read
   `reason`/`tokens` for telemetry).
4. **`type == "error"`** → mark failure; capture a **bounded** indicator from
   `error.data.message` (fallback `error.name`). **Never** emit raw stdout.

`Completed` = (no `error` event seen) AND (process exit 0) AND (got `text` or a
`step_finish`). On the no-text / error path, return `Completed=false` with a short
bounded indicator (e.g. `opencode error: Unexpected server error (err_e558e8ba)`),
satisfying task 3.1's "never dump raw stdout" requirement.

---

## Part B — in-container "crash across providers" investigation

The operator reported opencode crashing in container `ca5990ebcc41` (incl. non-opencode
providers) earlier on 2026-06-01, shortly after a weekly opencode-go rate-limit reset,
and wondered whether it was container-specific (the container runs **TZ=UTC**, vs the
host's AEST) or a pipelock/network issue.

### Environment facts established

- **I (Claude Code) run on the host `MINTAERO` (TZ AEST), not in the container.** The
  container is reachable via `docker exec`. Container is `dune-prayer-app-2-6c-default-agent-1`
  (image `dune-base:0.4.2`, TZ=UTC), paired with a healthy `pipelock:2.0.0` egress container.

### What the logs actually show (window ~01:00–04:40 UTC, relay 11)

- **opencode failed exactly ONCE** — `try-167`, the `UnknownError / "Unexpected server
  error"` event above. Grep across every try log finds a single occurrence.
- That try's netstat tick: `connections:1, io_bytes:8994856, syscall_bytes:9728883`
  — i.e. **a connection was open and ~9 MB flowed**. The request reached the network
  and got a response. **Pipelock/egress was NOT blocking.** The error is upstream
  (opencode gateway-side; note the `ref` token), transient.
- The other "harness error" failures in the same window were **codex account
  usage-limits** (`"You've hit your usage limit … try again at 5:22 AM"`, tries
  163–166) and a burst of ~30–45 ms instant fails on `SENIOR` (route head
  `ag:opus`/antigravity) — i.e. provider/account state, not container networking.
- opencode's own log (`~/.local/share/opencode/log/2026-06-01T010618.log`, 6 MB) is
  essentially clean: a single `429`, no `level=error`/panic/TLS/DNS/socket errors.

### Live re-test (now, ~10:36 UTC, ~10 h post reset)

`opencode run "Reply with exactly: SPIKE_OK" --model zai-coding-plan/glm-5.1` **inside
the container → EXIT=0, returned `SPIKE_OK`.** Tool-using run also EXIT=0. opencode is
healthy in-container; the failure is **not reproducing**.

### Conclusion

No evidence of a container-specific or pipelock/network root cause. The "crash across
providers" was a **coincident bad window**: codex hit account usage limits, antigravity
fast-failed, and opencode took a **single transient upstream gateway error** — all
during/around the rate-limit-reset window, which is easy to perceive as one systemic
outage. Network egress through pipelock worked (9 MB on the failing try). No dune /
pipelock change is indicated.

The one durable, actionable defect this surfaced is a **rally** bug, already scoped by
this change: rally mishandles opencode's `type":"error"` event (raw-stdout summary,
no completion/error parse) — see Part A and tasks 3.1/3.2.

### Secondary observation (host-side, separate issue — NOT the container question)

On the host, two standalone `opencode run` captures **hung at 0 bytes for 10–13 min**
(killed; no stdout, no stderr, no file written) while a *concurrent* host rally relay's
opencode runs completed fine. The host carried **4 stale `opencode` server processes
(4–5 days old**, incl. one `--port 64318`). This smells like `opencode run`
attaching to / contending with a stale local server, distinct from the in-container
question. Worth a separate look (reap stale opencode servers); not pursued here.

---

## Recommendations feeding the rest of rally-083

- Implement the Task 1.3 extraction in `parseOpenCodeOutput` / `Execute`
  (`internal/agent/opencode.go`): add the `type":"error"` branch, prefer
  `error.data.message` (bounded), use `step_finish`/exit-0 for completion, and never
  emit raw stdout (tasks 3.1, 3.2).
- The existing `text`/`tool` handling is correct and can stay.
- No change needed to dune/pipelock or container TZ on account of this incident.
