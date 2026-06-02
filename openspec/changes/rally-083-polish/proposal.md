## Why

Running Rally v0.8.0 through the 0.8.1/0.8.2 work surfaced a cluster of small but
real rough edges: the "slowing"/stall heuristic fires during normal reasoning,
retries spam the console and inflate failure counts, `tries.jsonl`/`summary.jsonl`
sometimes capture an entire run transcript, state commits crash on operator-ignored
files, and the agent prompt content (roles + shared instructions) is awkward to edit
because it is not centralized. Several of these trace back to one root cause — the
opencode adapter's brittle JSON parsing — and to thresholds tuned for a single
harness's behaviour. This change is operational polish for the 0.8.3 release.

## What Changes

- **Soften stall/slowing thresholds.** Raise the default `stall_threshold_secs`
  from 120s to 900s (15m); it stays configurable. The "slowing" display indicator
  (currently 0.6× the threshold, ~72s today) consequently moves to ~9m. Global,
  not per-harness: we cannot yet reliably distinguish long reasoning from a frozen
  agent, and reasoning models run on opencode too, so a short threshold risks
  cutting them off. Knowingly accepts that opencode runs idle ~15m post-completion
  before being reaped.
- **Tidy retry console output.** Show retries as an inline `retry N/M` field on the
  existing live status line instead of printing a new block per retry. Final stats
  tally each *run* once — a run fails only if all retries are exhausted, so a
  5-retry-then-success counts as 1 pass / 0 failures (today it shows as 5 failures).
- **Cap persisted final snippets.** Apply a 3000-rune cap per final-snippet text
  field (notably `summary`) when persisting try records and summary records, as
  a universal backstop against runaway output regardless of harness.
- **Normalize summary capture.** Ensure a try's `Summary` is sourced from one
  consistent final-snippet flow: `laps wrapup` is the golden data source after
  `laps done`/`laps handoff`; if no wrapup was recorded, use the parsed final
  assistant message; if that is unavailable, use a bounded tail of process text.
- **Harden opencode failure fallback.** The opencode adapter currently dumps the
  entire raw stdout into `Summary` when JSON-event parsing yields no text — the
  primary cause of the transcript-sized summaries. The fallback SHALL never emit
  raw stdout as a summary. The completed spike showed text/tool extraction is
  mostly correct and the durable parser fix is top-level `error` handling plus
  explicit `step_finish`/exit status completion detection.
- **Tolerate operator-gitignored paths in state commits.** When Rally adds its
  `.rally` operational state paths, paths the operator has chosen to
  `.gitignore` SHALL be skipped without error and without `-f` — respect the
  operator's intent. Default committed paths explicitly include
  `.rally/config.toml` and `.rally/summary.jsonl`; this behavior does not apply
  to `.laps/laps.json`.
- **Restructure agent prompt content.** Rename `internal/prompt` →
  `internal/user_prompt`; add `internal/agent_prompt` with `general/` and `roles/`
  subfolders of `go:embed`-ded `.md` files. Migrate the current `.rally/agents/*.md`
  role docs in as the new embedded role defaults (on-disk files remain operator
  overrides). Factor the shared "finalize" block (commit + `laps done`/`handoff` +
  `laps wrapup`) into a `general/` snippet so role docs hold only role-specific
  guidance. Add `general/headless.md`: Rally runs headless/non-interactive with no
  inline user confirmations; the best reference for intent is the planning docs the
  laps reference (e.g. an OpenSpec change).

## Capabilities

### New Capabilities
- `agent-prompt`: Embedded, composable agent-facing prompt sources. Defines the
  `general/` (shared finalize + headless guidance) and `roles/` (per-role) snippet
  layout, `go:embed` packaging, role-default-with-on-disk-override precedence, and
  how a full agent prompt is composed (general + role + task context).

### Modified Capabilities
- `executor`: OpenCode adapter SHALL never emit raw stdout as a `Summary` on parse
  failure; `TryResult.Summary` semantics clarified to be a finalization summary, not
  a transcript; prompt-building sources move to the new prompt packages.
- `relay-runner`: configurable stall threshold with a 15m default; inline `retry
  N/M` status-line indicator; run-level (not per-try) pass/fail tally; per-field
  summary cap applied to recorded try results; state-commit tolerance for
  operator-gitignored paths.
- `store`: persisted try-record text fields (e.g. `summary`) SHALL be length-capped
  on write as a durable backstop.

## Impact

- **Code:** `internal/config/config_v2.go` (stall default), `internal/monitor`
  (slowing indicator, retry field), `internal/relay/runner.go` (tally, truncation,
  capture, stall wiring), `internal/store` (field cap), `internal/agent/opencode.go`
  (safe fallback), `internal/gitx/git.go` (`CommitRallyState` add tolerance),
  `internal/prompt` → `internal/user_prompt`, new `internal/agent_prompt`,
  `internal/prompt/roleloader` (override resolution), `.rally/agents/*.md` (become
  embedded defaults).
- **Docs:** `AGENTS.md` gains a note that the prompt package naming reflects *who*
  is being prompted (`user_prompt` vs `agent_prompt`).
- **Behaviour:** opencode runs idle longer before stall reaping (accepted); repos
  without `.rally/agents/` begin receiving the richer embedded role defaults;
  `summary.jsonl` remains append-only JSONL.
- **Spike (complete):** live opencode `run --format json` output was captured and
  diffed against the adapter's expected event schema. Implementation should apply
  the recorded error/completion parsing contract rather than re-guessing the
  schema.
- **Not breaking:** package rename is under `internal/` (no public API); no config
  keys removed (stall threshold already exists, only its default changes).
