---
name: test-driving-rally
description: Smoke-test rally for real to verify features work end-to-end. Use when the user wants to verify that rally is working correctly after changes or before a release.
license: MIT
compatibility: Requires rally built from source, plus at least one agent CLI (claude, codex, gemini, opencode).
metadata:
  author: rally
  version: "1.0"
---

Run real end-to-end smoke tests of rally to verify features work correctly. This skill drives rally from the CLI in isolated `/tmp/` repos, observes actual behaviour, and reports findings.

**Goal**: High-signal smoke tests, not exhaustive QA. Prioritise breadth across features over depth on any one feature. Report issues found but do not investigate or fix them — keep focus on surface-area coverage.

---

## 1. Setup

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

Define a harness in config:

```toml
[harness.mycode]
command = ["opencode"]
model_flag = "-m"
output_strategy = "tail"
output_lines = 50
tail_stream = "combined"

[harness.mycode.models]
kimi = "opencode-go/kimi-k2.6"
mini = "opencode-zen/minimax-m2.5-free"
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

## 3. Known environment-specific failures

Not all agents may be authenticated or available. These are non-rally failures:

- **Gemini**: Fails with exit code 41 if no API key is set. Rally correctly retries and pauses the agent.
- **Codex**: May fail if CLI flags changed incompatibly. Check try record `summary` for the exact error.
- **OpenCode**: Model availability varies by configured provider.

When an agent CLI is broken/unauthed, verify that rally's retry and resilience handling works correctly (pause recorded, relay continues or ends gracefully) rather than treating it as a rally failure.

---

## 4. Reporting

After testing, compile a concise report:

```
## Smoke Test Results — rally vX.Y.Z

### Passed
- Basic claude relay: ✓
- Monitor status line: ✓ (token estimate, slowing indicator)
- Config validation: ✓ (invalid harness name, missing default route)
- Routes: ✓
- Laps integration: ✓ (auto-detected, hooks installed, both tasks completed)
- Custom harness: ✓ (mycode:kimi resolved, relay ran)
- Resume prompt: ✓
- --new flag: ✓ (old relay closed)
- Weighted mix cc:2: ✓

### Failed / Degraded
- Codex: CLI argument conflict (--dangerously-bypass-approvals-and-sandbox + --full-auto now mutually exclusive)
  Suggestion: Update CodexExecutor args in internal/agent/codex.go
- Gemini: No auth configured (environment issue, not a rally bug)

### Observations
- relay log files in shared data_dir (~/.local/share/rally/) use per-repo relay IDs which can collide across repos — logs may interleave
- [N/M] in relay header shows global run_id / target_iterations, not iteration-within-relay
```

---

## 5. Keeping this skill current

Update this skill after test-driving sessions where:
- New features are added to rally (add them to section 2)
- Agent CLI interfaces change (update known failure notes in section 3)
- New harness configs or model strings become standard (update examples)
- The skill's test patterns reveal consistent gotchas (add to observations)

Do **not** duplicate rally's own documentation here — the authoritative source is the source code, `AGENTS.md`, and `README.md`. This skill captures *how to test-drive*, not *what rally can do*.
