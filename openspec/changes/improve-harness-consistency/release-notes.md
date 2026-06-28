
# Release Notes: 0.12.0

This release cuts the deprecated `gemini` CLI harness, tightens failure
categorization so silent exits always carry diagnostic context, and cleans
up routing telemetry.

## Breaking changes

### `gemini`/`ge` aliases removed; `ui` role defaults to `antigravity`

The standalone `gemini` CLI harness is gone. Antigravity is now Rally's only
Google-owned harness — it serves the same Gemini model family on the same
provider account, so this is a routing consolidation, not a capability loss.

- The `gemini` and `ge` aliases no longer resolve. A route that still pins
  `gemini`/`ge` now warns **once** at startup (naming the role, the route
  entry, and the offending alias) and recommends `antigravity`, instead of
  failing late at runner launch with "no executor for agent gemini".
  Update `[routes]` and `--agent` entries from `gemini`/`ge` to `ag`.
- The `ui` role's default route changed from `gemini` to `ag`
  (antigravity), keeping a Google-family default. `rally init roles`
  regenerates the role defaults.
- `gemini_model` and the `[harness.gemini]` config surface are removed.
- The antigravity-scoped `gemini-cli exit 1` classification pattern is
  retained (antigravity shells out to gemini-cli); only the standalone
  harness was cut.

### New `unidentified_issue` failure category

`unidentified_issue` replaces `agent_error` as the **default** for failures
that could not be classified into a known category. It carries a bounded raw
signal so unrecognised failures are still inspectable in telemetry.

- `agent_error` is now **reserved** for failures where a specific agent-level
  error was actually extracted — either matched by a text pattern at
  classification Priority 4, or unambiguously shown in a harness disk log.
- Net effect: failures that used to be labelled `agent_error` with no
  context are now `unidentified_issue` (with a bounded log tail). Any
  dashboard or alert keyed on `failure_category = 'agent_error'` as a
  catch-all will see that population move to `unidentified_issue`; alerts
  that specifically tracked extracted agent errors will be more precise.
- The Priority-5 default also emits `failure_evidence.source = "unmatched"`
  with a bounded log tail, and Priority 3 (dirty-tree / incomplete
  finalization) and Priority 4 (text pattern) failures now carry their own
  `failure_evidence` (`source = "dirty_tree"` / `source = "text_pattern"`).

## Failure diagnostics

### Disk-log fallback for every harness

Every harness now has a disk-log fallback so a silent failure always carries
bounded diagnostic context, surfaced as `failure_evidence`:

| Harness | Disk-log source | `failure_evidence.source` |
|---|---|---|
| codex | session log (`$CODEX_HOME/sessions/…/*.jsonl`) | `codex_session_log` |
| codex | (no matching session log) | `codex_no_session_log` → `harness_launch` |
| claude | session JSONL (`~/.claude/projects/<project>/<uuid>.jsonl`) | `claude_session_log` |
| opencode | server log | `opencode_disk_log` |
| antigravity | glog (`~/.gemini/antigravity-cli/log/cli-*.log`) | `antigravity_glog` |

Each fallback is bounded (raw signals are capped, credential paths are
stripped, and verbose per-token/per-tool logs are skipped) and respects
in-band precedence: a recognised category extracted from stdout/stderr is
authoritative and is never replaced by a disk-log fallback.

### Try-budget and run-budget kills are now categorised

Previously, a try that was killed on its budget carried an empty
`failure_category` in telemetry. Both budget-kill paths now set
`failure_category = "unidentified_issue"` when no more specific evidence was
produced (executor/disk-log evidence with a non-empty category remains
authoritative):

| Kill kind | `fail_reason` | `failure_category` |
|---|---|---|
| try-cap only (per-try deadline fired, run budget remains) | `try budget exhausted; no output` | `unidentified_issue` |
| run-budget (whole-run budget consumed) | `run timeout` | `unidentified_issue` |

**Distinguishing "slow model" vs "underqualified model":** both budget kills
are separable in NRQL via the existing `timeout_kind` tag (`try_cap` /
`run_budget` / `handoff`) together with `runtime_ms`, `tool_calls`,
`files_changed`, and `last_output_age_ms`:

- high `tool_calls` + small `last_output_age_ms` → active work in progress
  (slow but productive model);
- low `tool_calls` + large `last_output_age_ms` → stalled/underqualified.

## Telemetry

### `runner` tag now includes the resolved model

The `runner` tag is now `<harness>:<resolved-model>` even for bare-alias
routes (e.g. a `codex` route now reports `runner = "codex:gpt-5.5"` rather
than the bare `runner = "codex"`).

**Action required:** NRQL alerts or dashboards keyed on an exact
`runner = 'codex'` (or any other bare harness) match will stop firing.
Widen the filter to `runner LIKE 'codex%'`.

### `RallyRoute` event; NULL-outcome `RallyTry` cleanup

Routing decisions — route fallback (rotating past a failing harness) and
recovery-cap hits — now emit a dedicated **`RallyRoute`** custom event
instead of a `RallyTry` entry with a NULL `outcome`.

- The recovery-cap-hit path **also** keeps its existing `RallyFailure`
  (`needs_user`) emission, so the operator-worthy alert is not lost.
- Every `RallyTry` emission now carries a non-empty `outcome`; the telemetry
  boundary back-fills `outcome = "unknown"` and warns if a caller forgets.

**Action required:** dashboards that filtered
`RallyTry WHERE outcome IS NOT NULL` (intentionally or to drop the routing
noise) will see those routing events disappear — they now live in
`RallyRoute`. Update NRQL to read routing events from `RallyRoute`.
