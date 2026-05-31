## 1. Laps gitignore regression test

(Line numbers in this document are approximate and reflect the codebase as of May 2026; they may shift during implementation.)

- [x] 1.1 Grep the rally codebase as a one-time manual check to confirm no code generates a `.laps/.gitignore`; then add a permanent regression test asserting `laps.InstallHooks` never writes one (the stray `.laps/.gitignore` was already removed and `laps.json` already tracked as of commit `33d288e`)

## 2. Centralise `.rally/` path construction

- [x] 2.1 Add path helpers (e.g. `store.RallyDir`, `store.StateDir(workspaceDir)`, `store.SummaryPath`) and route all `.rally/` path building through them
- [x] 2.2 Replace ad-hoc `filepath.Join(..., ".rally", ...)` in `internal/store/{store,cache}.go`, `internal/progress/`, `internal/cli/hooks.go`, and `internal/relay/runner.go` with the helpers

## 3. Move machine data into `.rally/state/`

- [x] 3.1 Point `tries|relays|agent_status|messages.jsonl` writers/readers at `.rally/state/` (`store.go`, `cache.go`)
- [x] 3.2 Move `hook-audit.jsonl` (`internal/cli/hooks.go`), `run-state.json` (`internal/progress/runstate.go`), and `current_task.md` (`internal/relay/runner.go:698`) into `.rally/state/`
- [x] 3.3 Update the `.rally/.gitignore` template in `cmd/rally/main.go` to a single `state/` line
- [x] 3.4 Replace commit-then-truncate-to-git windowing (`internal/store/window.go`): append-only log files (`tries.jsonl`, `relays.jsonl`) get no pruning at all — they grow unbounded. Read-oriented state files (`agent_status.jsonl`, `messages.jsonl`) use in-place local truncation (no git commit) with conservative limits (500 for agent_status, 200 resolved for messages; pending messages exempt)
- [x] 3.5 Update the `.rally/README.md` template to describe the new layout and correct the false "git-tracked JSONL source of truth" claim
- [ ] 3.6 Add a `CommitHistory []string` field to `TryRecord` (`internal/store/records.go`) alongside the existing `CommitHash string` and `LapsAttempted []LapAttempt` fields. Persist the full ordered commit list per try in `tries.jsonl` instead of only a single commit hash; a single commit becomes a one-element list. Keep `CommitHash` for backward compatibility (set to last element of `CommitHistory`)
- [x] 3.7 Update `internal/gitx/git.go`: `CommitRallyState` (line 113) currently `git add .rally/*.jsonl` — post-change the only tracked `.rally/*.jsonl` is `summary.jsonl`, so scope the git-add accordingly or remove if no longer needed. Update `IsWorkspaceDirty` (lines 64, 81-82) to remove stale suffix checks for `.rally/current_task.md` and `.rally/relays/` (these move to gitignored `state/`). Update `runner_test.go` comments (lines 85-87) that reference "JSONL state files committed as durable git-backed state"

## 4. Replace `progress.yaml` with `summary.jsonl`

- [x] 4.1 Reimplement `internal/progress/store.go` as an append-only `summary.jsonl` writer, preserving the `RunEntry`/`HandoffEntry` shape and the `AppendRunEntry` signature. Remove the `ProgressLog` struct, `LoadProgress`, and `SaveProgress` functions. Add a `LoadSummaryEntries` function to parse `summary.jsonl`. Update `progressLapsCompletedForRun` in `runner.go:1339` to use `LoadSummaryEntries` instead of `LoadProgress`. Verify `internal/progress/cli.go` (which drives the `rally progress` command) still works — it calls `AppendRunEntry` whose signature is preserved, and verify its `laps add head` subprocess interaction with the new summary path. Note: `RunEntry.LapsCompleted` is `interface{}` — ensure JSONL round-trips correctly (handle `[]interface{}` unwrapping, as the existing YAML reader already does)
- [x] 4.2 Drop `history_window` trimming and remove YAML read/write paths
- [x] 4.3 Confirm `runner.go:1460` call site is unchanged and `summary.jsonl` is the only tracked top-level data file
- [x] 4.4 Update `internal/app/app.go`: change `DefaultRepoProgress` from `".rally/progress.yaml"` to `".rally/summary.jsonl"`. The function `RepoProgressPath` and env var `RALLY_REPO_PROGRESS_PATH` keep their names for now (rename deferred to a future cleanup — add a `// TODO:` comment noting the naming inconsistency)

## 5. One-time migration (runInit only)

- [x] 5.1 Implement an idempotent migration in `runInit`: create `.rally/state/`, move legacy flat files in (never overwriting existing targets)
- [x] 5.2 Remove legacy `.rally/batches/` and top-level `.rally/relays/` log dir (if present — these are legacy artifacts that may not exist in all repos). Do NOT touch `config.toml.bak` (user-managed) or convert `progress.yaml` (left as-is)
- [x] 5.3 Invoke migration from `runInit` only; ensure re-running is a no-op

## 6. Telemetry sink (Sentry)

- [ ] 6.1 Add `github.com/getsentry/sentry-go` dependency
- [ ] 6.2 Define a `telemetry.Sink` interface with a default no-op implementation
- [ ] 6.3 Implement the Sentry sink: activate only with a configured DSN; honor `[telemetry] sentry_dsn`, `SENTRY_DSN` (overrides config), and `RALLY_TELEMETRY=0` kill switch
- [ ] 6.4 Add `[telemetry] sentry_dsn` to the config struct and template. Three changes needed: add `Telemetry TelemetryConfig` field to `V2Config` (`internal/config/config_v2.go:98`), add `Telemetry TelemetryConfig \`toml:"telemetry,omitempty"\`` to `rawConfig` (`config_v2.go:120`), and add `Telemetry: cfg.Telemetry` in the `rawConfig` literal in `SaveV2` (`config_v2.go:532`). Also update `cmd/rally/init_roles.go:140` config save path to preserve the section
- [ ] 6.5 Emit per-try structured logs at `store.AppendTry` (`runner.go:1045`) and model relay/run/try as a trace+span hierarchy; tag every event with `relay_id`/`run_id`/`try_id`/`role`/`runner`/`repo`/`lap_id`
- [ ] 6.6 Capture recognized failures (non-zero exit, panic, "agent exited without finalizing", `laps done`-as-text, lap-integrity violations) as Sentry Issues. Log route fallback (rotating to a backup runner) as a common recovery event, not an Issue
- [ ] 6.7 Add a `before_send` scrubber that never ships `current_task.md` contents or full transcripts
- [ ] 6.8 Init the sink once in `cmd/rally/main.go` and `defer sentry.Flush(2s)` before exit (no-op/bounded when disabled or offline)
- [ ] 6.9 Add assembled-prompt size + per-source byte breakdown (recent-context, previous-summary, instructions, role, task, inbox/relay) to the per-try structured log
- [ ] 6.10 Gate Issue capture on operator-worthy failures: infra-class failures (consume the landed `harden-relay-run-lifecycle` classification) and relay stalls (pass ends all-frozen); agent-class retries emit spans/logs only, no Issue

## 7. Bundle laps alongside rally

- [ ] 7.1 Update `install.sh` to fetch and install a compatible `laps` binary next to `rally` (from https://github.com/mitchell-wallace/laps)
- [ ] 7.2 Extend the existing `rally update` command (`cmd/rally/main.go:505`) to also upgrade `laps` alongside `rally`
- [ ] 7.3 Add a startup minimum-laps-version check that warns (not hard-fail) and advises `rally update`

## 8. Tests

- [ ] 8.1 Tests for the migration on a fixture `.rally/` (flat→state move, legacy dir cleanup, idempotency, no-overwrite, progress.yaml left untouched)
- [x] 8.2 Tests for store/cache reading and writing under `.rally/state/` and for local-only truncation windowing (assert `tries.jsonl`/`relays.jsonl` are never truncated; `agent_status.jsonl`/`messages.jsonl` use conservative in-place limits)
- [x] 8.3 Tests for `summary.jsonl` append shape and that `progress.yaml` is never written. Update `runner_test.go:2045` to reference `summary.jsonl` instead of `progress.yaml`
- [ ] 8.4 Tests for the telemetry sink: no-op without DSN, kill switch, env-over-config precedence, tag presence, and scrubber dropping `current_task.md`
- [ ] 8.5 Tests for prompt-size fields (total + per-source breakdown present on the try log) and Issue criteria (infra failure + relay stall → Issue; agent-class retry → no Issue; route fallback → common event, no Issue)
- [ ] 8.6 Tests for try commit history (multiple commits retained in order; single commit as one-element list; `CommitHash` backward compat set to last element)
- [ ] 8.7 Test that `rally update` and the min-version check behave correctly (warn vs silent)

## 9. Docs & rollout

- [ ] 9.1 Update `AGENTS.md`/`README.md` and any skill references (`test-driving-rally`, `post-relay-review` including `post-relay-review/references/prayer-app-2.md`) that mention `progress.yaml` or flat `.rally/*.jsonl` paths to the new `state/` + `summary.jsonl` layout
- [ ] 9.2 Document telemetry opt-in (DSN config, env vars, kill switch, what is/ isn't sent) and `rally update`
- [ ] 9.3 Bump `internal/buildinfo/VERSION` (per release process) as part of the change
