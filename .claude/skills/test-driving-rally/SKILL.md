---
name: test-driving-rally
description: Smoke-test rally for real to verify features work end-to-end. Use when the user wants to verify that rally is working correctly after changes or before a release.
license: MIT
compatibility: Requires rally built from source, plus at least one agent CLI (agy, claude, codex, gemini, opencode).
metadata:
  author: rally
  version: "1.4"
---

Run real end-to-end smoke tests of rally to verify features work correctly. This skill drives rally from the CLI in isolated `/tmp/` repos, observes actual behaviour, and reports findings.

**Goal**: High-signal smoke tests, not exhaustive QA. Prioritise breadth across features over depth on any one feature. When you find a defect that's small enough to fix in the same session, fix it; otherwise note it and move on.

## Default workflow (unless the user explicitly overrides)

When the user invokes this skill, follow this loop:

1. **Update the skill first.** Apply any new guidance the user just gave (model slugs, env state, behaviours). Also refresh any status-shaped text that's gone stale (e.g. "gemini is unauthenticated" when it now works). Commit the skill update separately at the end if it's substantive.
2. **Read prior session state.** Check `./tmp/session-handoff.md` and the real-backend tests for what's already covered. Pick behaviours that are *not* yet tested or that need re-verification.
3. **Test drive.** Run the pre-built real-backend tests as a baseline, then do targeted manual smoke tests on the gaps.
4. **Apply fixes** for any defects you find that are tractable in-session.
5. **Verify the fixes** by re-running the relevant smoke test or adding a real-backend test.
6. **Bump the minor version** in `internal/buildinfo/VERSION` and commit the code patch (one commit per logical change is fine), unless the user explicitly asks for patch or major.
7. **Wrap up** once confident or approaching ~300k context:
   - Update the skill with any new findings (workflow gotchas, status corrections, new model behaviours).
   - Write a new `./tmp/session-handoff.md` (overwrite the previous — it's a single rolling doc).
   - Commit the docs update.

**NEVER replace user-provided model slugs with older ones on a hunch.** If the user names a model, save it verbatim into this skill and use it. Older slugs (e.g. gemini-2.5, gpt-4o, opencode-zen variants) are not valid in this environment. If the new slug appears not to work, report the failure mode — don't silently fall back.

## Recording progress — important

Previous sessions have failed to update this skill correctly. To prevent recurrence:

- **Section 1 has a "Current model slugs" subsection.** Treat it as the source of truth. When the user provides new slugs, edit that list *immediately*, before doing any testing. Do not embed model slugs only in scattered examples below — update the canonical list.
- **Section 3 ("Known environment-specific failures")** must reflect current reality. If an agent that was previously broken now works (e.g. gemini after workspace-trust fix), *delete* the old failure note instead of stacking caveats on top of it. Stale "Gemini fails with exit 41 if no API key" lines mislead future sessions.
- **The session handoff** at `./tmp/session-handoff.md` is a single rolling doc, not an append log. Overwrite it each session. Do not create dated copies in `/tmp/` outside the repo.
- **Commit the skill update separately from code patches** so reviewers can see the skill diff in isolation.

## 0. Reuse pre-built real-backend tests first

Before doing manual smoke tests, run the existing real-backend integration
tests. They cover the core scenarios automatically:

```bash
RALLY_TEST_REAL_AGENTS=1 go test ./internal/relay/... -run TestRealBackend -v -timeout 600s
```

These tests skip automatically when `RALLY_TEST_REAL_AGENTS` is unset. They cover:
- Basic claude relay with file creation
- Laps queue integration
- Log scoping per-repo (two repos → two subdirectories in data_dir)
- Codex executor (checks for CLI arg conflicts)
- OpenCode executor (checks headless mode — no TUI ANSI in summary)
- Antigravity executor (checks `agy --print`, settings-backed model selection, and conversation-id capture)
- Resilience retry budget exhaustion and agent pausing
- Custom harness via `opencode run` (no TUI, valid try record)
- Multi-harness round-robin (`cc ge op` → one of each, in order)

If they all pass, proceed to the manual smoke tests below for broader coverage. If any fail, investigate before continuing — the pre-built tests are cheaper to run and faster to interpret than manual ones.

Add new tests to `internal/relay/runner_real_backend_test.go` whenever you find a new category of failure during manual testing.

---

## 1. Setup

**Bump VERSION before testing patches.** Any session that commits patches should increment the minor number in `internal/buildinfo/VERSION` by default (e.g. `0.7.0` → `0.8.0`) unless the user explicitly asks for patch or major. Commit it so CI builds an updated binary for distribution. The file is embedded into the binary, so `rally version` on a dev build reports `vX.Y.Z-dev`. Do this once per session, before building:

```bash
# increment minor version, e.g.:
echo "0.8.0" > internal/buildinfo/VERSION
git add internal/buildinfo/VERSION && git commit -m "bump version to 0.8.0"
```

**Build rally from source** (do not rely on PATH rally — it may be a stale version):

```bash
go build -o /tmp/rally ./cmd/rally/
export PATH="/tmp:$PATH"
rally version
```

**Check which agent CLIs are available:**

```bash
which agy claude codex gemini opencode 2>/dev/null
```

### Current model slugs (canonical list)

Always use these slugs in tests. They are the only slugs known to be available in this environment as of the latest session. If a slug fails, report it — do not fall back to older names.

| Harness | Slug | Notes |
|---|---|---|
| `ag`/`agy` (antigravity) | `Gemini 3.5 Flash (High)` | Verified 2026-05-21 via `agy --print`; `agy` 1.0.0 has no CLI model flag, so Rally sets `~/.gemini/antigravity-cli/settings.json` for the run and restores it. |
| `cc` (claude) | `claude-haiku-4-5` | Cheapest/fastest; default for smoke tests. |
| `cx` (codex) | `gpt-5.4-mini` | Verified working (see `TestRealBackend_CodexRelay`). |
| `ge` (gemini) | `gemini-3.1-pro-preview` | Verified 2026-05-11: simple task in ~2min. Authenticated. `GEMINI_CLI_TRUST_WORKSPACE=true` is set by rally so headless mode works. |
| `ge` (gemini) | `gemini-3-flash-preview` | Verified 2026-05-11: simple task in ~15s. Lighter/faster flash variant. |
| `op` (opencode) | `opencode-go/kimi-k2.6` | Verified 2026-05-11: ~18s when not rate-limited. Free tier; rate-limits after a few runs (~12h window). |
| `op` (opencode) | `opencode/minimax-m2.5-free` | Verified 2026-05-11: ~14s. NOT `opencode-zen/...` — the zen prefix is wrong. |
| `op` (opencode) | `zai-coding-plan/glm-5.1` | Verified 2026-05-11: ~10s. The `zai-coding-plan` provider with `glm-5.1` suffix. |

Alias note: Antigravity is `ag` or `agy`; gemini is `ge`, NOT `gm`. Rally rejects `gm` with `unknown agent alias`.

Check `/workspace/rally.toml` for the project-default slugs in use, and `AGENTS.md` for terminology. The slugs above override anything you see in older docs or memory.

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

**Check**: exit 0, file exists, try record in `.rally/state/tries.jsonl` shows `"completed": true`.

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
mini = "opencode/minimax-m2.5-free"  # NOT opencode-zen
zai  = "zai-coding-plan/glm-5.1"
```

The custom-harness path with `opencode run` has been verified in `TestRealBackend_CustomHarnessRelay` — it produces valid try records with no ANSI in summaries, and the relay record's `agent_mix` shows `mycode`. The TUI warning (section 3) is what protects against the bad config; don't disable it.

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

**Pause semantics (changed 2026-05-28 in `harden-relay-run-lifecycle`):** an agent
is only **paused** (`"paused"` event in `agent_status.jsonl`) when its failures are
classified `FailureInfra` AND there is more than one infra failure
(`failureClass == FailureInfra && infraFailures > 1`). A *plain* agent task-failure
(`FailureAgent` — e.g. the agent runs but makes no changes, or returns a non-infra
error) **no longer pauses** the agent; the scheduler still rotates off it within the
relay via `OnAgentFailed`, but cross-relay resilience-pause is reserved for repeated
infra problems (rate limits, connection refused, usage limits, harness/launch errors —
see `internal/reliability/patterns.go`). Classification reads the **last 50 lines of the
try log file**, so to force an infra classification in a fake-executor test, write an
infra-pattern line (e.g. `rate limit`) to `opts.LogPath`.

To verify pause-on-infra (e.g. codex CLI broken, or a rate-limited free-tier provider),
check:
- `.rally/state/agent_status.jsonl` contains a `"paused"` event for that agent
- Subsequent relay attempts for that agent show "all agents paused, waiting Xm" in the relay log
- `~/.local/share/rally/relays/relay-N.log` for confirmation

`TestRealBackend_ResilienceRetryBudget` (deterministic, fake executor) guards the
infra-pause path; `TestRealBackend_OpenCodeRelay` only requires a pause event when the
opencode failure was infra-classified (a plain "no changes" failure is a valid
non-paused outcome). Do NOT re-assert the old "any failure pauses" behavior.

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

**Check**: Relay records in `.rally/state/relays.jsonl` — old relay gets `ended_at` set when `--new` is used.

### 2i. Weighted mix

```bash
rally relay --new --iterations 2 --agent "cc:2" "Create mix-test.txt"
```

**Check**: `agent_mix` in relay record shows the weight spec. Both iterations run with claude.

### 2j. Multi-harness relay (cc + other)

```bash
rally relay --new --iterations 3 --agent "cc ge op" "Create a unique file per iteration."
```

Watch the header line cycle through `claude`, `gemini`, `opencode`. The
`agent_type` field in `state/tries.jsonl` should also alternate. **Regression
note (fixed in 0.7.4)**: prior versions stuck on the first harness because
the override path didn't inject a default quota for bare aliases.
`TestRealBackend_MultiHarnessRoundRobin` guards this. If you see all
iterations using the same agent, the override quota default has likely
regressed.

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

**Check**: `.rally/state/summary.jsonl` updated with new entries.

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

The per-try netstat log at `~/.local/share/rally/tries/<repo>/try-N.netstat.jsonl` records `{ts, log_silent_s, connections, io_bytes, syscall_bytes}` each tick. Typical baselines:
- Simple task (file creation): connections 2-6, syscall delta 2-5 MB total
- npm install: connections 1-2 with massive syscall delta (400 MB–2 GB), sporadic "No I/O" warnings during download wait phases
- Rate-limited idle: connections > 0, syscall bytes plateau (< 1 MB/min), log silent

Check `~/.local/share/rally/relays/<repo>/relay-N.log` for "freeze detected" vs no freeze.

---

## 3. Known environment-specific quirks

These are agent-CLI behaviours that affect how tests appear. None are rally bugs.

- **Gemini**: Authenticated and working in this environment. `GEMINI_CLI_TRUST_WORKSPACE=true` is set by rally (commit `ee86a21`) so headless mode no longer fails with exit 41/55. **Gemini never writes to its log file** — `last activity` counts from t=0 for the entire run, so on Linux `classicFrozen` fires purely by elapsed time. For simple tasks gemini exits cleanly in ~3-4 min; for complex tasks use `freeze_threshold_secs = 600`. The freeze-recovery-for-committed-work fix (`3f87af4`) lets rally treat a freeze-killed try as success when files were committed.
- **Codex**: `--full-auto` / `--dangerously-bypass-approvals-and-sandbox` conflict resolved (commit history) — only the bypass flag is passed now. `TestRealBackend_CodexRelay` guards this.
- **OpenCode**: Model availability varies by provider. Use the built-in `op` alias — NOT a custom harness with `command = ["opencode"]` (which starts TUI mode). Rally warns on this at startup. When rate-limited (`opencode-go` free tier in particular), opencode maintains TCP connections silently for ~2 minutes then disconnects; `classicFrozen` fires ~130s after start regardless of freeze threshold (provided threshold < 130s). After freeze, rally marks the agent paused and retries later. Free tier resets roughly every 12h.

### Session resume per harness (verified 2026-06-08)

Resume reuses a harness's prior session on pause/resume and on any retry that has a
tracked session ID. The runner is harness-agnostic: it carries `result.SessionID` into
the next attempt's `RunOptions.ResumeSessionID` (runner.go ~`:999`/`:1439`) and persists
it to run-state. A harness contributes to resume only if it BOTH (a) captures its
session ID into `TryResult.SessionID`, and (b) passes its resume flag when
`ResumeSessionID != ""`. Current truth:

| Harness | `ResumeSupported()` | Captures session? | Resume flag | Verified |
|---|---|---|---|---|
| claude | true | ✅ | `--resume <id>` | works |
| antigravity | true | ✅ | `--conversation=<id>` | works |
| codex | true | ✅ (`thread.started`) | `exec [flags] resume <id>` | **end-to-end CLI proven** — same thread reused, prior context retained |
| opencode | true | ✅ (`sessionID` field, **fixed 0.8.7**) | `--session <id>` | CLI resume proven; capture was missing pre-0.8.7 |
| gemini | **false** | n/a | n/a (CLI `--resume` is **index/`latest` only, not a session UUID**) | correctly honest-false |

Gotcha: gemini *does* have `-r, --resume` but it takes an index number or `latest`, not
a captured session UUID, so `ResumeSupported()=false` is the correct honest answer — do
not "fix" it to true. If you find a harness reporting `ResumeSupported()=true` whose
`parseXxxOutput` never sets `TryResult.SessionID`, resume is silently dead (that was the
opencode bug). Drive a real 2-step resume check: have the agent memorize a codeword in
try 1, then resume and ask for it.

**Linux freeze behavior**: Two paths — `classicFrozen` (log silent + no connections) fires once connections drop (either after task completion or after rate-limit timeout). `connectedFrozen` (log silent + connections open + no syscall I/O for 5 min) catches agents holding a connection open indefinitely (e.g., different rate-limit behavior). The `TestRealBackend_OpenCodeRelay` test takes ~3 minutes when opencode-go is rate-limited (2m10s for freeze + 50s for ctx expiry); this is expected and the test passes.

When an agent CLI is broken/unauthed, verify that rally's retry and resilience handling works correctly (pause recorded, relay continues or ends gracefully) rather than treating it as a rally failure.

**Test-artifact leak**: some real-backend tests (e.g. OpenCodeRelay) can leave task
output files (`opencode-e2e.txt`, `step-3.txt`, …) in `internal/relay/` because the
agent occasionally writes into the package dir rather than its temp workspace. After a
`RALLY_TEST_REAL_AGENTS=1` run, check `git status` and `git checkout`/`rm` these strays
before committing — they are not real changes and `opencode-e2e.txt` is already an
accidentally-tracked artifact.

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
- Rally progress command: ✓ (summary.jsonl updated)

### Failed / Degraded
- OpenCode rate-limited models: hang silently; `classicFrozen` fires ~130s after connections drop, agent paused (working as intended)

### Observations
- `rally tail --try N` uses global try IDs from the shared data_dir; across multiple repos in the same session, try 1 from repo A and try 1 from repo B go to different subdirectories (fixed), but the `--try N` flag maps to local store IDs, not data-dir IDs
```

---

## 5. Keeping this skill current

Update this skill *during* the session — not as an afterthought at the very end. Concretely:

- **Step 1 of the default workflow** (section above) is to update the skill with any new user guidance *before testing*. Do this so the slug list, env-state notes, and workflow guidance are right when you reach for them later.
- **Section 1's slug table is the canonical source.** Edit it any time the user names a model, and propagate the change down into example snippets if needed.
- **Delete stale failure notes** in section 3 rather than layering caveats. If gemini works now, the "fails with exit 41" line is misleading — remove it.
- **End-of-session pass**: before writing the handoff, re-skim sections 1, 3, and 5 once more. Anything that was true at the start of the session but isn't anymore? Fix it.

What goes here vs. elsewhere:
- *This skill*: how to test-drive, current env state, environment-specific quirks, slug list.
- *AGENTS.md / README.md / source*: what rally does, terminology, release flow.
- *Memory*: durable cross-session preferences and references (e.g. "user prefers fixing root cause over patching symptoms"). Not slug lists or test recipes — those belong here.
- *./tmp/session-handoff.md*: a single rolling doc with what *this session* did and what's outstanding. Overwrite each session.

Do **not** duplicate rally's own documentation here — the authoritative source is the source code, `AGENTS.md`, and `README.md`. This skill captures *how to test-drive*, not *what rally can do*.
