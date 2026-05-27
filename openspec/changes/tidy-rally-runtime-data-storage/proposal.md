## Why

Rally's `.rally/` directory has accreted a flat pile of machine-churned files (`tries.jsonl` at ~1.8MB, a 120KB `current_task.md`, `hook-audit.jsonl`, legacy `batches/` and `relays/` dirs, `config.toml.bak`) alongside the user-facing `progress.yaml`, which has grown messy and inconsistent with the JSONL-everywhere convention. The `store` spec claims these JSONL files are git-tracked "source of truth", but in practice none of them are committed — only `config.toml`, `agents/`, and `README.md` travel via git, so run history never moves between containers and the durability promise is hollow. We want a clean top-level layout, a durable centralised log store (Sentry) instead of relying on bloated git-tracked JSONL, laps bundled as a first-class companion to rally, and the stray hand-committed `.laps/.gitignore` removed so `laps.json` (the real cross-container handoff artifact) is tracked.

## What Changes

- **BREAKING**: Move all machine-churned data into a new gitignored `.rally/state/` subfolder: `tries.jsonl`, `relays.jsonl`, `agent_status.jsonl`, `messages.jsonl`, `verify-reports.jsonl` (introduced by `harden-relay-run-lifecycle`), `hook-audit.jsonl`, `run-state.json`, `current_task.md`. These are no longer git-tracked; durability shifts to Sentry + local retention.
- **BREAKING**: Replace `.rally/progress.yaml` with an append-only `.rally/summary.jsonl` — the sole top-level data file and the only tracked run-history artifact. Same per-run/handoff record shape, one JSON line each.
- Reduce tracked `.rally/` contents to: `config.toml`, `agents/`, `README.md`, `summary.jsonl`. The new `.rally/.gitignore` is just `state/`.
- Add a one-time, idempotent migration: move existing flat files into `state/`, convert `progress.yaml` → `summary.jsonl`, and remove legacy `batches/`, `relays/`, and `config.toml.bak`.
- Add an opt-in Sentry telemetry sink (default off; DSN via `config.toml [telemetry]` + `SENTRY_DSN` env; `RALLY_TELEMETRY=0` kill switch). Emits tries as structured logs, relay/run/try as a trace hierarchy, and genuine failures as Issues, tagged with `relay_id`/`run_id`/`try_id`/`role`/`runner`/`repo`/`lap_ids`. Issues are reserved for operator-worthy failures (infra-class failures, relay stall with all agents frozen, panic, no-finalize, `laps done`-as-text, lap-integrity violations); ordinary agent-class retries stay spans/logs to avoid alert noise. Each try log also carries the assembled-prompt size and a per-source breakdown so runaway prompt growth is caught (the enforcement-side budget lives in `harden-relay-run-lifecycle`). Flushes on CLI exit; a `before_send` scrubber never ships `current_task.md` or full transcripts.
- Persist the full ordered commit list per try (not just one final hash) so causal chains across tries survive in the try record.
- Bundle the `laps` binary alongside `rally` in `install.sh`, add a `rally update` command that upgrades both, and add a startup minimum-laps-version check. `.laps/` stays a separate top-level dir (laps never reads `.rally/`).
- Remove the stray manually-committed `.laps/.gitignore` and track `laps.json`.

## Capabilities

### New Capabilities
- `run-summary`: the append-only `summary.jsonl` digest of finalized runs and handoffs that replaces `progress.yaml` as the sole tracked top-level data file.
- `telemetry`: opt-in Sentry sink emitting structured try logs (with assembled-prompt size + per-source breakdown), relay/run/try traces, and operator-worthy failure Issues (infra failures and relay stalls, not agent-class retries), with config, scrubbing, and CLI-exit flushing.
- `tooling-distribution`: bundling laps alongside rally (install + `rally update` + min-version check) and removing the stray `.laps/.gitignore`.

### Modified Capabilities
- `store`: JSONL records relocate to gitignored `.rally/state/` and are no longer git-tracked (**BREAKING**); the commit-then-truncate-to-git windowing requirement is replaced with local-only retention since git history is no longer the archive; adds the one-time migration requirement and a per-try commit-history field (full ordered commit list, not a single hash).

## Impact

- **Code**: `internal/store/{store,cache}.go` (paths → `state/`, drop git-commit windowing), `internal/progress/` (replace `store.go` YAML with `summary.jsonl` appender; relocate `runstate.go`), `internal/cli/hooks.go` (hook-audit path), `internal/relay/runner.go` (current_task.md path; telemetry emit points at `AppendTry` and `AppendRunEntry`), `cmd/rally/main.go` (gitignore template, README template, migration in `runInit`, new `update` command, Sentry init+flush, min-laps-version check).
- **New deps**: `github.com/getsentry/sentry-go`.
- **New config**: `[telemetry] sentry_dsn` in `config.toml`; `SENTRY_DSN` / `RALLY_TELEMETRY` env vars.
- **Distribution**: `install.sh` fetches/installs laps; new `rally update`.
- **Repos using rally**: existing `.rally/` dirs migrate on next run; consumers of `progress.yaml` must switch to `summary.jsonl`; the false "git-tracked JSONL" expectation is corrected.
- **Sequencing with `harden-relay-run-lifecycle`**: that change ships first and alters `agent_status.jsonl` freeze semantics (decay to probation + `--new` reset, per-harness-model `model` field, 500-event window), adds `verify-reports.jsonl` (50-event window), defines failure classification (infra/agent/incomplete), and adds `laps_attempted` to the try record. This change relocates all of those files to `state/` (path-agnostic, no semantic conflict), preserves their windowing/semantics rather than re-specifying them, and consumes the classification for Issue criteria. Record-shape additions here (per-try commit list) must stay compatible with that change's `laps_attempted` recording — neither change forks the try record.
- **Not changed**: `dataDir` verbose logs (`~/.local/share/rally/`), laps' own `.laps/` location and standalone usability.
