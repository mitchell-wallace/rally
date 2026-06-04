## Context

These items came out of operating Rally v0.8.0 while building 0.8.1
(`harden-relay-run-lifecycle`) and 0.8.2 (`tidy-rally-runtime-data-storage`).
They are individually small but share a few roots:

- **Thresholds tuned for one harness.** `stall_threshold_secs` defaults to 120s
  (`config_v2.go:179`) because opencode finishes in ~25-30s then holds the process
  open until connections drop near 120s — i.e. the stall timeout doubles as
  opencode's "done, reap it" signal. The "slowing" display indicator is
  `0.6 × threshold` (`monitor.go:454`), so today it fires at ~72s of log silence —
  during normal reasoning an opus/agy run looks like it is "slowing" for its entire
  duration.
- **One brittle parser causing several symptoms.** `internal/agent/opencode.go`
  parses `opencode run --format json` NDJSON, collecting `part.text` from
  `type:"text"` events. When that yields nothing (e.g. opencode changed its event
  schema), `parseOpenCodeOutput` falls through to `Summary: string(out)` — the
  entire raw stdout. That single fallback explains the transcript-sized
  `tries.jsonl`/`summary.jsonl` entries AND the "glm-5.1 exits with harness errors"
  reports (which are not model-specific — they are an opencode integration
  regression).
- **Retry accounting.** Each non-completed `TryRecord` is counted as a failure in
  the final summary (`runner.go:600-609`), so a run that succeeds on retry 5 shows
  as 5 failures; retries also render as repeated console blocks rather than a live
  field.
- **Prompt content is hard to edit.** Role and shared instruction text is spread
  between `.rally/agents/*.md` (operator-side, hand-edited) and Go code, with no
  single embedded source of truth.

## Goals / Non-Goals

**Goals:**
- Stop false "slowing"/stall signals during normal reasoning.
- Make retries quiet in the console and honest in the final tally.
- Bound the size of persisted summary text and capture the *right* text.
- Make state commits resilient to operator `.gitignore` choices.
- Centralize agent prompt content as embedded, composable `.md` with operator
  override, and add headless + finalize guidance.

**Non-Goals:**
- Reliably distinguishing "long reasoning" from "frozen agent" (why item 1 is a
  global threshold bump, not per-harness tuning).
- Per-harness stall thresholds or early opencode process reaping based on
  completion detection (deferred; would remove the opencode-idle trade-off later).
- Rewriting the opencode parser blind — the spike has already captured live
  output, so parser changes should follow that evidence instead of guessing.
- The broader `git-hygiene` auto-commit work (separate planned change).

## Decisions

### 1. Global 15m stall threshold, not per-harness
Bump the `config_v2.go` default for `stall_threshold_secs` from 120 → 900. Keep it
configurable. The slowing indicator stays `0.6 × threshold` (→ ~9m) so both move
together from one knob.
- *Why global:* we can't yet tell deep reasoning from a hang, and opencode runs
  reasoning models (glm/kimi/qwen/deepseek) too — a short global or opencode-short
  threshold risks killing them mid-thought.
- *Trade-off accepted:* opencode runs that finish fast and hold the process open
  now idle ~15m before the stall reaps them. Documented so a future
  completion-detection change has the rationale.
- *Alternatives considered:* per-harness threshold map (rejected for now — adds a
  mechanism while the real lifecycle fix is early opencode process reaping);
  reaping opencode immediately on completion now (rejected — process lifecycle
  changes should follow the parser fix and be handled as separate harness
  lifecycle work).
- `reliability.DefaultStallThreshold` (180s) stays as the bare-code fallback when no
  config is loaded; only the config default changes.

### 2. Inline retry field + run-level tally
- Live status line gains a `retry N/M` field (reusing the existing monitor line, no
  new block per attempt).
- Final stats count a *run* once: pass if it ever completed, fail only if all
  retries were exhausted. Aggregate over runs, not raw `TryRecord`s.
- *Why:* the per-try count conflates "the run failed" with "the run needed
  retries", which over-reports failures and hides the success.

### 3. Final-snippet cap at the persistence boundary (3000 runes)
Apply a per-field cap of 3000 runes when final-snippet text is written (the `store`
and `progress` layers that own record persistence), with head+tail truncation and a
`… [truncated] …` marker consistent with the existing in-prompt
`buildRecentContext` truncation (`runner.go:637`). This size is intentionally closer
to a concise review/recommendations section than to a one-line status: enough to
capture useful context, small enough to prevent transcript-sized records.
- *Why at write, not read:* keeps `tries.jsonl`/`summary.jsonl` small on disk and in
  git, and protects every downstream reader at once.
- *Fields:* cap `TryRecord.Summary`, `TryRecord.RemainingWork`,
  `progress.RunEntry.Summary`, `progress.HandoffEntry.Summary`, and each
  free-text `progress.HandoffEntry.Followups` string. Stored values MUST be no
  longer than the cap after truncation.

### 4. Normalize final snippets before storing try summaries
`TryResult.Summary` SHALL be populated from a single final-snippet data flow, not
from raw transcripts. The runner owns final summary normalization after the executor
returns:

1. If the agent successfully called `laps done` or `laps handoff` and then
   `laps wrapup`, the persisted `TryResult.Summary` SHALL use the `laps wrapup`
   summary. This is the golden data source because it is the explicit agent report
   Rally asked for.
2. If no wrapup summary was recorded, but the executor can parse a final assistant
   message or structured `TryResult.Summary` from JSON output, use that text.
3. If neither source exists, use the last bounded portion of process text, or a short
   explicit no-finalization/error indicator when there is no usable text.

The same normalized final snippet SHOULD feed retry context, `tries.jsonl`, and
`summary.jsonl` so those persisted surfaces do not disagree about what the agent
reported. The 3000-rune cap (decision 3) is the persistence backstop.

### 5. opencode safe fallback + completed schema fix
- The failure fallback SHALL NOT emit raw stdout as `Summary`. On empty/parse
  failure, return a short, bounded indicator with `Completed=false`.
- Apply the completed spike's extraction contract: collect ordered `type:"text"` /
  `part.text` assistant text, count `tool_use` / `part.type:"tool"`, treat
  `step_finish` plus process exit 0 as a clean completion signal, and parse
  top-level `type:"error"` events with no `part` from `error.data.message`,
  `error.data.ref`, and `error.name`.

### 6. Embedded, composable agent prompts
- Rename `internal/prompt` → `internal/user_prompt` (prompts authored *for the
  user*). Add `internal/agent_prompt` (prompts fed *to the agent*) with:
  - `general/` — shared snippets: `finalize.md` (commit + `laps done`/`handoff` +
    `laps wrapup`), `headless.md` (non-interactive; intent lives in planning docs).
  - `roles/` — `junior.md`, `senior.md`, `ui.md`, `verify.md`, role-specific only.
  - `go:embed` of the `.md` tree.
- A full agent prompt is composed as `general snippets + role snippet + task
  context`. Embedded content is the default; an on-disk `.rally/agents/<role>.md`
  overrides only its role snippet, preserving today's operator-override behaviour
  without suppressing shared `general/` guidance. The new template preserves the
  existing executor prompt contract: explicit `RunOptions.Prompt` overrides still
  win, and normal prompts still include project instructions, role/persona
  guidance, task name/requirements, inbox or relay messages, previous summary,
  recent try context, and other existing task context sections.
- Migrate the current `.rally/agents/*.md` text in as the embedded defaults, minus
  the repeated finalize block (now shared in `general/finalize.md`).
- Do not rewrite or migrate operator-owned on-disk custom role prompts. Extend
  `rally routes check` to list detected roles and an approximate token count per
  role prompt. It also scans custom role prompts for possible overlap terms such
  as `laps done`, `laps handoff`, `laps wrapup`, and `headless`; when found, print
  the embedded `general/finalize.md` and `general/headless.md` snippets so the
  operator can compare and update their prompt deliberately. These diagnostics
  are advisory and should not fail an otherwise valid routes check.
- *Naming convention* (to be recorded in `AGENTS.md`): package name reflects *who*
  is being prompted — `user_prompt` vs `agent_prompt`.

### 7. gitx tolerates operator-gitignored paths
`CommitRallyState` (`git.go:115`) currently fails if any `.rally` operational state
path has been gitignored by the operator (`git add` → "paths are ignored by
.gitignore"). Detect that specific condition and skip the path silently — never
`-f`, never crash. The default tracked `.rally` paths explicitly include
`.rally/config.toml` and `.rally/summary.jsonl`. This scope is intentionally limited
to `.rally` operational paths and does not change the project rule that
`.laps/laps.json` must be committed.

## Risks / Trade-offs

- **[opencode idle ~15m]** Fast opencode runs waste wall-clock waiting for the stall
  reap. → Accepted and documented; revisit with early opencode process reaping
  once parser-level completion/error handling is reliable.
- **[15m hang detection latency]** A genuinely frozen agent now takes up to 15m to
  be caught. → Acceptable given false-kills during reasoning were the worse failure
  mode in practice; threshold remains configurable for impatient setups.
- **[role-default migration changes behaviour]** Repos without `.rally/agents/` start
  getting richer embedded defaults. → Intended; on-disk files still override.
- **[opencode parser evidence ages]** The spike captured opencode 1.15.11 output;
  future opencode schema changes may still require another parser update. → Tests
  should cover the captured `text`, `tool_use`, `step_finish`, and `error` events.
- **[truncation hides detail]** A 3000-rune cap can still clip useful detail. →
  Head+tail truncation keeps both ends; full transcripts were never the intended
  contents of a summary field.

## Open Questions

- Whether the live `retry N/M` field should also surface the cooldown/strategy on
  infra-class retries, or stay minimal. Default: minimal.
