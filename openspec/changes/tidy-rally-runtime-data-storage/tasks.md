## 1. Laps gitignore fix (quick unblock)

- [ ] 1.1 `git rm .laps/.gitignore` in the rally repo and `git add .laps/laps.json` so the work queue is tracked
- [ ] 1.2 Grep both rally and laps codebases to confirm no code generates a `.laps/.gitignore`; add a regression test asserting `laps.InstallHooks` never writes one

## 2. Centralise `.rally/` path construction

- [ ] 2.1 Add path helpers (e.g. `store.RallyDir`, `store.StateDir(workspaceDir)`, `store.SummaryPath`) and route all `.rally/` path building through them
- [ ] 2.2 Replace ad-hoc `filepath.Join(..., ".rally", ...)` in `internal/store/{store,cache}.go`, `internal/progress/`, `internal/cli/hooks.go`, and `internal/relay/runner.go` with the helpers

## 3. Move machine data into `.rally/state/`

- [ ] 3.1 Point `tries|relays|agent_status|messages.jsonl` writers/readers at `.rally/state/` (`store.go`, `cache.go`)
- [ ] 3.2 Move `hook-audit.jsonl` (`internal/cli/hooks.go`), `run-state.json` (`internal/progress/runstate.go`), and `current_task.md` (`internal/relay/runner.go`) into `.rally/state/`
- [ ] 3.3 Update the `.rally/.gitignore` template in `cmd/rally/main.go` to a single `state/` line
- [ ] 3.4 Replace commit-then-truncate-to-git windowing with in-place local truncation (no git commit)
- [ ] 3.5 Update the `.rally/README.md` template to describe the new layout and correct the false "git-tracked JSONL source of truth" claim
- [ ] 3.6 Persist the full ordered commit list per try in the try record (`tries.jsonl`) instead of a single commit hash; a single commit becomes a one-element list (coordinate the field with `harden-relay-run-lifecycle` so the try record is not forked)
- [ ] 3.7 Coordinate with `harden-relay-run-lifecycle`: that change adds `laps_attempted` to `TryRecord` and a `model` field to `AgentStatusEvent` (with a 500-event window); restructure these fields into the new `state/` layout if they land before tidy

## 4. Replace `progress.yaml` with `summary.jsonl`

- [ ] 4.1 Reimplement `internal/progress/store.go` as an append-only `summary.jsonl` writer, preserving the `RunEntry`/`HandoffEntry` shape and the `AppendRunEntry` signature
- [ ] 4.2 Drop `history_window` trimming and remove YAML read/write paths
- [ ] 4.3 Confirm `runner.go:1302` call site is unchanged and `summary.jsonl` is the only tracked top-level data file

## 5. One-time migration

- [ ] 5.1 Implement an idempotent migration: create `.rally/state/`, move legacy flat files in (never overwriting existing targets)
- [ ] 5.2 Convert legacy `progress.yaml` → `summary.jsonl` (one JSON line per `recent_runs` entry), removing `progress.yaml` only after a successful write
- [ ] 5.3 Remove legacy `.rally/batches/`, top-level `.rally/relays/` log dir, and `.rally/config.toml.bak`
- [ ] 5.4 Invoke migration from `runInit` and lazily on first store write; ensure re-running is a no-op

## 6. Telemetry sink (Sentry)

- [ ] 6.1 Add `github.com/getsentry/sentry-go` dependency
- [ ] 6.2 Define a `telemetry.Sink` interface with a default no-op implementation
- [ ] 6.3 Implement the Sentry sink: activate only with a configured DSN; honor `[telemetry] sentry_dsn`, `SENTRY_DSN` (overrides config), and `RALLY_TELEMETRY=0` kill switch
- [ ] 6.4 Add `[telemetry] sentry_dsn` to the `config.toml` template and config struct
- [ ] 6.5 Emit per-try structured logs at `store.AppendTry` (`runner.go:957`) and model relay/run/try as a trace+span hierarchy; tag every event with `relay_id`/`run_id`/`try_id`/`role`/`runner`/`repo`/`lap_ids`
- [ ] 6.6 Capture recognized failures (non-zero exit, route fallback, panic, "agent exited without finalizing", `laps done`-as-text) as Sentry Issues
- [ ] 6.7 Add a `before_send` scrubber that never ships `current_task.md` contents or full transcripts
- [ ] 6.8 Init the sink once in `cmd/rally/main.go` and `defer sentry.Flush(2s)` before exit (no-op/bounded when disabled or offline)
- [ ] 6.9 Add assembled-prompt size + per-source byte breakdown (recent-context, previous-summary, instructions, role, task, inbox/relay) to the per-try structured log
- [ ] 6.10 Gate Issue capture on operator-worthy failures: infra-class failures (consume `harden-relay-run-lifecycle` classification) and relay stalls (pass ends all-frozen); agent-class retries emit spans/logs only, no Issue

## 7. Bundle laps alongside rally

- [ ] 7.1 Update `install.sh` to fetch and install a compatible `laps` binary next to `rally`
- [ ] 7.2 Add a `rally update` command that upgrades both `rally` and `laps`
- [ ] 7.3 Add a startup minimum-laps-version check that warns (not hard-fail) and advises `rally update`

## 8. Tests

- [ ] 8.1 Tests for the migration on a fixture `.rally/` (flat→state move, progress.yaml→summary.jsonl, legacy cleanup, idempotency, no-overwrite)
- [ ] 8.2 Tests for store/cache reading and writing under `.rally/state/` and for local-only truncation windowing
- [ ] 8.3 Tests for `summary.jsonl` append shape and that `progress.yaml` is never written
- [ ] 8.4 Tests for the telemetry sink: no-op without DSN, kill switch, env-over-config precedence, tag presence, and scrubber dropping `current_task.md`
- [ ] 8.6 Tests for prompt-size fields (total + per-source breakdown present on the try log) and Issue criteria (infra failure + relay stall → Issue; agent-class retry → no Issue)
- [ ] 8.7 Tests for try commit history (multiple commits retained in order; single commit as one-element list)
- [ ] 8.5 Test that `rally update` and the min-version check behave correctly (warn vs silent)

## 9. Docs & rollout

- [ ] 9.1 Update `AGENTS.md`/`README.md` and any skill references (`prepare-laps`, `post-relay-review`) that mention `progress.yaml` or flat `.rally/*.jsonl` paths to the new `state/` + `summary.jsonl` layout
- [ ] 9.2 Document telemetry opt-in (DSN config, env vars, kill switch, what is/ isn't sent) and `rally update`
- [ ] 9.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
