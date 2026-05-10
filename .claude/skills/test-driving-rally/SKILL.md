---
name: test-driving-rally
description: Smoke-test rally for real to verify features work end-to-end. Use when the user wants to verify that rally is working correctly after changes or before a release.
license: MIT
compatibility: Requires rally built from source, plus at least one agent CLI (claude, codex, gemini, opencode).
metadata:
  author: rally
  version: "1.1"
---

Run real end-to-end smoke tests of rally to verify features work correctly. This skill drives rally from the CLI in isolated `/tmp/` repos, observes actual behaviour, and reports findings.

**Goal**: High-signal smoke tests, not exhaustive QA. Prioritise breadth across features over depth on any one feature. Report issues found but do not investigate or fix them — keep focus on surface-area coverage.

## 0. Reuse pre-built real-backend tests first

Before doing manual smoke tests, run the existing real-backend integration
tests. They cover the core scenarios automatically:

```bash
RALLY_TEST_REAL_AGENTS=1 go test ./internal/relay/... -run TestRealBackend -v -timeout 300s
```

These tests skip automatically when `RALLY_TEST_REAL_AGENTS` is unset. They cover:
- Basic claude relay with file creation
- Laps queue integration
- Log scoping per-repo (two repos → two subdirectories in data_dir)
- Codex executor (checks for CLI arg conflicts)
- OpenCode executor (checks headless mode — no TUI ANSI in summary)
- Resilience retry budget exhaustion and agent pausing

If they all pass, proceed to the manual smoke tests below for broader coverage. If any fail, investigate before continuing — the pre-built tests are cheaper to run and faster to interpret than manual ones.

Add new tests to `internal/relay/runner_real_backend_test.go` whenever you find a new category of failure during manual testing.

---

## 1. Setup

**Bump VERSION before testing patches.** Any session that commits patches must increment the patch number in `VERSION` (e.g. `0.7.0` → `0.7.1`) and commit it so CI builds an updated binary for distribution. Do this once per session, before building:

```bash
# increment patch version, e.g.:
echo "0.7.1" > VERSION
git add VERSION && git commit -m "bump version to 0.7.1"
```

**Build rally from source** (do not rely on PATH rally — it may be a stale version):

```bash
go build -o /tmp/rally ./cmd/rally/
export PATH="/tmp:$PATH"
rally version
```

**Check which agent CLIs are available:**

```bash
which claude codex gemini opencode 2>/dev/null
```

Note which are present. Current valid model strings to use are in the comments at the top of `/workspace/rally.toml` and in the user's latest guidance. Check `AGENTS.md` for current model naming conventions.

---

## 2. Feature areas to cover

For each area, create an isolated git repo in `/tmp/rally-test-<area>/`, run `rally init`, write a `.rally/config.toml`, and run the test. Use short `--iterations 1` or `--iterations 2` relays so tests complete quickly. Use `claude-haiku-*` for claude tests (cheapest/fastest).

### 2a. Basic relay (claude)

Smoke test: single iteration, simple file creation task.

```bash
mkdir -p /tmp/rally-test-basic && cd /tmp/rally-test-basic
git init -q && git config user.email "t@t.com" && git config user.name "T"
touch init.txt && git add . && git commit -q -m "init"
rally init
# Write config with claude_model set
rally relay --new --iterations 1 --agent cc "Create an empty file called smoke-test.txt"
```

**Check**: exit 0, file exists, try record in `.rally/tries.jsonl` shows `"completed": true`.

### 2b. CLI monitor

Observed during any claude run. Look for:
- `⏱ Xs │ 📁 N files │ last activity: Xs` status line updating
- `~Nk tok` token estimate appearing after first activity
- `⚠ slowing` indicator if liveness probe fires
- Keyboard hint line: `[Ctrl+S skip] [Ctrl+P pause] [Ctrl+X stop] [Ctrl+C quit]`

### 2c. Config validation

```bash
# Valid schema version warning
cat > .rally/config.toml << 'EOF'
schema_version = 99
[defaults]
mix = "cc"
claude_model = "claude-haiku-*"
EOF
rally routes check  # should warn about schema version

# Invalid harness name
cat > .rally/config.toml << 'EOF'
schema_version = 2
[harness.123bad]
command = ["echo"]
EOF
rally routes check  # should error: invalid harness name

# Missing default route with routes configured
cat > .rally/config.toml << 'EOF'
schema_version = 2
[defaults]
claude_model = "claude-haiku-*"
[routes]
planner = ["cc:2"]
EOF
rally routes check  # should warn: no default route
```

### 2d. Routes (role-based routing)

```bash
# Config with default route
[routes]
default = ["cc:2"]
planner = ["cc:2"]
executor = ["cc:1"]
```

Run `rally routes check` → confirm summary shows all routes. Then run a relay without `--agent` (uses default route). Confirm it runs correctly.

### 2e. Laps integration

Prerequisites: `laps` CLI installed and initialized.

```bash
mkdir -p /tmp/rally-test-laps && cd /tmp/rally-test-laps
git init -q && git config user.email "t@t.com" && git config user.name "T"
touch init.txt && git add . && git commit -q -m "init"
laps init && laps on
laps add head --title "Task 1" --description "Create file task1.txt with content 'done'"
laps add tail --title "Task 2" --description "Create file task2.txt with content 'done'"
rally init
# Write config
rally relay --new --iterations 2 --agent cc
```

**Check**: Rally auto-detects `.laps/`, prints "Installed rally hooks in ...". Both files created, `laps list` shows empty queue.

### 2f. Custom harness

**Important:** for opencode specifically, prefer the built-in `op` alias with `opencode_model` in defaults rather than a custom harness. The built-in executor uses `opencode run <prompt> --format json` (headless mode) and handles JSON output parsing. A custom harness using `command = ["opencode"]` starts TUI mode — it will not exit cleanly and the freeze detector will see spurious output. Rally warns about this at startup.

If you do need a custom harness for opencode, use headless mode explicitly:

```toml
[harness.mycode]
command = ["opencode", "run", "$PROMPT", "--format", "json"]
model_flag = "--model"
output_strategy = "tail"
output_lines = 50
tail_stream = "stdout"

[harness.mycode.models]
kimi = "opencode-go/kimi-k2.6"
mini = "opencode-zen/minimax-m2.5-free"
```

For any other CLI that accepts input on stdin and exits:

```toml
[harness.myagent]
command = ["myagent", "--no-interactive"]
model_flag = "--model"
output_strategy = "tail"
output_lines = 40
tail_stream = "combined"
```

Run `rally relay --new --agent mycode:kimi "Create file custom-test.txt"`. Check relay record shows `agent_mix` containing `mycode`.

### 2g. Resilient execution

After a run exhausts its retry budget (e.g., codex CLI broken), check:
- `.rally/agent_status.jsonl` contains a `"paused"` event for that agent
- Subsequent relay attempts for that agent show "all agents paused, waiting Xm" in the relay log
- `~/.local/share/rally/relays/relay-N.log` for confirmation

### 2h. Resume and --new/--resume flags

Create an incomplete relay (kill mid-run or use `--iterations 2` with an agent that partially fails). Then:

```bash
# Interactive prompt test (pipe "resume" or "new")
echo "resume" | rally relay --agent cc "..."  # should show resume prompt
echo "new" | rally relay --agent cc "..."     # should discard + restart

# Non-interactive flags
rally relay --resume --agent cc "..."  # should resume silently
rally relay --new --agent cc "..."    # should close old, start new
```

**Check**: Relay records in `.rally/relays.jsonl` — old relay gets `ended_at` set when `--new` is used.

### 2i. Weighted mix

```bash
rally relay --new --iterations 2 --agent "cc:2" "Create mix-test.txt"
```

**Check**: `agent_mix` in relay record shows the weight spec. Both iterations run with claude.

### 2j. Multi-harness relay (cc + other)

If codex is working:
```bash
rally relay --new --iterations 2 --agent "cc cx" "Create task.txt"
```

Watch header line alternate between `claude` and `codex`.

### 2k. Tail command

```bash
rally tail           # stream latest try log (JSONL)
rally tail --try 1   # stream specific try
```

**Note**: `rally tail` uses the shared data_dir. The `--try N` number is the global try ID across all repos using the same data_dir — be aware of this when testing across multiple repos.

### 2l. Progress command

```bash
rally progress --summary "test done"
rally progress --complete --summary "all done" --followup "check results"
```

**Check**: `.rally/progress.yaml` updated with new entries.

### 2m. Instructions command

```bash
rally instructions show   # shows "(no project instructions set)" or content
```

---

### 2n. Rate-limit / stuck-agent scenario

To test how rally handles a rate-limited or hung agent, use a provider known to be rate-limited (or just a slow model). Configure with a short freeze threshold:

```toml
[reliability]
freeze_threshold_secs = 60
retry_budget = 1
```

Run and observe:
- `⚠ slowing` fires at 36s (60% of threshold)
- On **Linux**: two freeze paths exist:
  - **Classic** (`classicFrozen`): log silent ≥ threshold AND connections == 0. Fires once the agent's TCP connections drop.
  - **Connected-frozen** (`connectedFrozen`): log silent ≥ threshold AND connections > 0 AND no syscall I/O (`rchar+wchar`) for 5 minutes. Catches rate-limited agents keeping a connection alive but sending no data.
- On **non-Linux**: only log silence is checked; `❄ frozen` fires at threshold.

The per-try netstat log at `.rally/data/tries/REPO/try-N.netstat.jsonl` records `{ts, log_silent_s, connections, io_bytes, syscall_bytes}` each tick. Typical baselines:
- Simple task (file creation): connections 2-6, syscall delta 2-5 MB total
- npm install: connections 1-2 with massive syscall delta (400 MB–2 GB), sporadic "No I/O" warnings during download wait phases
- Rate-limited idle: connections > 0, syscall bytes plateau (< 1 MB/min), log silent

Check `.rally/relays/relay-N.log` for "freeze detected" vs no freeze.

---

## 3. Known environment-specific failures

Not all agents may be authenticated or available. These are non-rally failures:

- **Gemini**: Fails with exit code 41 if no API key is set. Rally correctly retries and pauses the agent. **Gemini never writes to its log file** — "last activity" counts from t=0 for the entire run. This means `classicFrozen` fires purely by time. For simple tasks gemini completes and exits in ~3-4 min; for complex tasks (e.g. todo app), use `freeze_threshold_secs = 600` to avoid premature kill.
- **Codex**: May fail if CLI flags changed incompatibly. Check try record `summary` for the exact error.
- **OpenCode**: Model availability varies by configured provider. Use the built-in `op` alias — NOT a custom harness with `command = ["opencode"]` (TUI mode). When rate-limited (`opencode-go` free tier), opencode maintains TCP connections silently for ~2 minutes then disconnects — `classicFrozen` fires ~130s after start regardless of threshold (as long as threshold < 130s). After freeze, rally marks the agent paused and retries later.

**Linux freeze behavior**: Two paths — `classicFrozen` (log silent + no connections) fires once connections drop (either after task completion or after rate-limit timeout). `connectedFrozen` (log silent + connections open + no syscall I/O for 5 min) catches agents holding a connection open indefinitely (e.g., different rate-limit behavior). The `TestRealBackend_OpenCodeRelay` test takes ~3 minutes when opencode-go is rate-limited (2m10s for freeze + 50s for ctx expiry); this is expected and the test passes.

When an agent CLI is broken/unauthed, verify that rally's retry and resilience handling works correctly (pause recorded, relay continues or ends gracefully) rather than treating it as a rally failure.

---

## 4. Reporting

After testing, compile a concise report:

```
## Smoke Test Results — rally vX.Y.Z

### Passed
- Basic claude relay: ✓ (file created, try record written, commit hash shown)
- Monitor status line: ✓ (⚠ slowing indicator, no token estimate)
- Config validation: ✓ (invalid harness name, missing default route, route-name-as-entry)
- Routes: ✓ (routes check, default route relay)
- Laps integration: ✓ (auto-detected, hooks installed, both tasks completed)
- Custom harness: ✓ (mycode:kimi resolved, relay ran, correct agent_mix)
- Resume interactive prompt: ✓ (detected unfinished relay, keep/overwrite mix)
- --new flag: ✓ (old relay closed, new relay started)
- --resume flag: ✓ (non-interactive, found paused agent, waited)
- Weighted mix cc:2: ✓ (2 claude iterations completed)
- Log scoping: ✓ (tries in data_dir/tries/REPOKEY/ per-workspace)
- [N/M] header: ✓ (shows iteration-within-relay / target, e.g. [1/3])
- Rally progress command: ✓ (progress.yaml updated)

### Failed / Degraded
- Gemini: No auth configured (environment issue, not a rally bug)
- OpenCode rate-limited models: hang silently; freeze detector on Linux doesn't kill due to active TCP connection

### Observations
- `rally tail --try N` uses global try IDs from the shared data_dir; across multiple repos in the same session, try 1 from repo A and try 1 from repo B go to different subdirectories (fixed), but the `--try N` flag maps to local store IDs, not data-dir IDs
```

---

## 5. Keeping this skill current

Update this skill after test-driving sessions where:
- New features are added to rally (add them to section 2)
- Agent CLI interfaces change (update known failure notes in section 3)
- New harness configs or model strings become standard (update examples)
- The skill's test patterns reveal consistent gotchas (add to observations)

Do **not** duplicate rally's own documentation here — the authoritative source is the source code, `AGENTS.md`, and `README.md`. This skill captures *how to test-drive*, not *what rally can do*.
