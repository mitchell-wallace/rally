## Context

`.rally/` mixes human-edited config (`config.toml`, `agents/`, `README.md`), a user-facing run digest (`progress.yaml`), machine-churned JSONL records (`tries.jsonl` ~1.8MB, `relays.jsonl`, `agent_status.jsonl`, `messages.jsonl`), audit/state files (`hook-audit.jsonl`, `run-state.json`), a large transient prompt (`current_task.md` ~120KB), and dead legacy dirs (`batches/`, `relays/`, `config.toml.bak`). Verbose per-try/per-relay logs already live elsewhere — in `dataDir` (`~/.local/share/rally/`, configurable). Paths are built ad-hoc with `filepath.Join(..., ".rally", ...)` across `internal/store`, `internal/progress`, `internal/cli`, and `internal/relay`.

The `store` spec asserts JSONL files are git-tracked "source of truth", but `git ls-files` shows only `config.toml`, `agents/`, `README.md`, and `.gitignore` are committed in real repos. Run history does not travel between containers; only `laps.json` does — and in this repo even that is blocked by a stray hand-committed `.laps/.gitignore`.

## Goals / Non-Goals

**Goals:**
- A clean tracked top-level `.rally/`: `config.toml`, `agents/`, `README.md`, `summary.jsonl` only.
- All machine churn under one gitignored `.rally/state/` subfolder.
- `summary.jsonl` (JSONL, append-only) replaces `progress.yaml` as the single durable, tracked run digest.
- Durable centralised observability via an opt-in Sentry sink — replacing the hollow "git-tracked JSONL" durability story.
- Laps installed and updated as a first-class companion binary, while staying standalone and decoupled.
- A safe, idempotent migration for existing repos.

**Non-Goals:**
- Changing `dataDir` verbose-log layout (`relays/`, `tries/`, `sessions/`).
- Moving `.laps/` inside `.rally/` or coupling laps to rally state (laps must never read `.rally/`).
- Building long-horizon analytics/warehousing (Sentry covers errors + recent traces; PostHog-style analytics is explicitly deferred).
- Vendoring laps inside the rally binary (rejected — see Decisions).

## Decisions

**1. `state/` subfolder, gitignored; `summary.jsonl` tracked.**
Centralise path construction behind helpers (e.g. `store.StateDir(workspaceDir)`), then move `tries|relays|agent_status|messages.jsonl`, `hook-audit.jsonl`, `run-state.json`, and `current_task.md` under `.rally/state/`. New `.rally/.gitignore` is a single line: `state/`. Alternative considered: per-file gitignore entries at top level — rejected as the current brittle approach (easy to forget a new file; `current_task.md` 120KB churn already leaks intent).

**2. `summary.jsonl` replaces `progress.yaml`.**
Keep the existing `RunEntry`/`HandoffEntry` shape from `internal/progress/store.go`; serialize one JSON object per line instead of a windowed YAML doc. Rationale: consistency with every other record file, append-only (no rewrite/merge races), trivially `tail`/`jq`-able, and small enough to track in git. `history_window` trimming is dropped — the file is the canonical digest and stays small (one line per finalized run/handoff). The `AppendRunEntry` signature is preserved so `runner.go:1302` is untouched.

**3. Sentry sink behind a `telemetry.Sink` interface, opt-in, default off.**
A no-op `Sink` is the default; a Sentry impl activates only when a DSN is configured (`config.toml [telemetry] sentry_dsn`, overridable by `SENTRY_DSN`, killable by `RALLY_TELEMETRY=0`). Wire two existing emit points — `store.AppendTry` (`runner.go:957`) and `progress.AppendRunEntry` (`runner.go:1302`) — plus relay start/end. Mapping: relay → Sentry transaction (trace), run → span, try → child span; per-try structured log; genuine failures (non-zero exit, route fallback, panic, "agent exited without finalizing", Kimi `laps done`-as-text) → `CaptureMessage`/`CaptureException` Issues. Every event tagged `relay_id`/`run_id`/`try_id`/`role`/`runner`/`repo`/`lap_ids`. The per-try log also carries the assembled-prompt size and a per-source breakdown (recent-context / previous-summary / instructions / role / task / messages) so runaway growth is visible without shipping transcripts — the budget that *prevents* the growth lives in `harden-relay-run-lifecycle`; this sink only measures. Issues are reserved for operator-worthy failures: infra-class failures (per that change's classification) and relay stalls (a pass ending with all agents frozen). Ordinary agent-class retries stay spans/logs so alerting is not drowned in normal recoverable failures. Init once in `cmd/rally/main.go`; `defer sentry.Flush(2s)` before exit (mandatory for a short-lived CLI). `before_send` scrubber drops `current_task.md` contents and full transcripts, sending summaries + metadata only. Alternative considered: OpenTelemetry — heavier setup, no managed Issues/alerting; deferred behind the interface so it can be swapped later.

**4. Laps bundled alongside, not vendored.**
`install.sh` fetches the `laps` release binary next to `rally`; a new `rally update` subcommand re-runs the upgrade for both. A startup minimum-laps-version check warns (does not hard-fail) when laps is too old for the hooks contract. Rejected: vendoring laps into the rally binary (loses standalone laps, couples release cadences). Rejected: moving `.laps/` into `.rally/` (would force laps to know about rally's dir — the coupling we explicitly avoid).

**5. Idempotent migration in `runInit` (and lazily on first write).**
If legacy flat files exist: `mkdir -p .rally/state` and move them in; convert `progress.yaml` → `summary.jsonl` (parse YAML `recent_runs`, emit one JSON line each); delete `batches/`, legacy `relays/`, `config.toml.bak`. Re-running is a no-op. Removing the stray `.laps/.gitignore` and tracking `laps.json` is a one-shot repo fix (git op), not part of the runtime migration.

## Risks / Trade-offs

- **Losing git-tracked run history** → Acceptable per the chosen git boundary; the prior promise was never actually delivered (files weren't committed). Durability now comes from Sentry (opt-in) + local `state/` retention. Document clearly in `README.md`.
- **Sentry DSN/secrets or PII leaking via events** → `before_send` scrubber + explicit allowlist of fields; never ship `current_task.md`/transcripts; default-off so nothing is sent without explicit opt-in.
- **Migration data loss / partial moves** → Idempotent, move-don't-copy with existence checks; never overwrite an existing `state/` target; `progress.yaml` retained until `summary.jsonl` write succeeds; covered by tests on a fixture `.rally/`.
- **Laps/rally version skew after bundling** → Startup min-version check warns; `rally update` upgrades both together.
- **Other tooling reading `progress.yaml`** → BREAKING; called out in proposal. The `prepare-laps`/`post-relay-review` skills and README references must be updated to `summary.jsonl` + `state/` paths.
- **Sentry SDK on offline/airgapped runs** → No-op when no DSN; flush has a bounded 2s timeout so a missing network never blocks CLI exit.
