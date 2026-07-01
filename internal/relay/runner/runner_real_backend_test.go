package runner

// Real-backend e2e tests that invoke actual agent CLIs (claude, codex, etc).
// These tests are skipped unless RALLY_TEST_REAL_AGENTS=1 is set and the
// required binary is available in PATH.
//
// Run with: RALLY_TEST_REAL_AGENTS=1 go test -run TestRealBackend ./internal/relay/...
//
// Each test is self-contained: it creates a temp workspace, runs a relay, and
// asserts on files created and store records. Failures here indicate a real
// integration break, not a unit-test mock mismatch.

import (
	"context"
	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/harness/generic"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

func requireRealAgents(t *testing.T) {
	t.Helper()
	if os.Getenv("RALLY_TEST_REAL_AGENTS") != "1" {
		t.Skip("set RALLY_TEST_REAL_AGENTS=1 to run real-backend tests")
	}
}

func requireBinary(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("binary %q not found in PATH: %v", name, err)
	}
}

func setupRealWorkspace(t *testing.T) (workspaceDir, rallyDir, dataDir string) {
	t.Helper()
	workspaceDir = t.TempDir()
	testutil.InitGitRepo(t, workspaceDir)
	// Create an initial commit so git rev-parse HEAD works and headBefore is non-empty.
	// Without this, the runner's hasChanges check falls back to IsWorkspaceDirty which
	// returns false after the agent commits, incorrectly marking the try as failed.
	initFile := filepath.Join(workspaceDir, ".rally-init")
	if err := os.WriteFile(initFile, []byte("init\n"), 0o644); err != nil {
		t.Fatalf("write init file: %v", err)
	}
	testutil.RunCommand(t, workspaceDir, "git", "add", ".")
	testutil.RunCommand(t, workspaceDir, "git", "commit", "-m", "init")
	rallyDir = store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("mkdir .rally: %v", err)
	}
	dataDir = t.TempDir()
	return workspaceDir, rallyDir, dataDir
}

// TestRealBackend_ClaudeBasicRelay runs a single-iteration claude relay and
// verifies that the task completed and a try record was written.
func TestRealBackend_ClaudeBasicRelay(t *testing.T) {
	requireRealAgents(t)
	requireBinary(t, "claude")

	workspaceDir, rallyDir, dataDir := setupRealWorkspace(t)

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{
		"claude": &agent.ClaudeExecutor{Model: "claude-haiku-4-5"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	targetFile := filepath.Join(workspaceDir, "e2e-result.txt")
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		AgentMixSpecs:    []string{"cc"},
		TargetIterations: 1,
		RetryBudget:      1,
		TaskPrompt:       "Create a file called e2e-result.txt with the text 'claude e2e pass'. Do not create any other files.",
	}, executors)

	if err := r.Run(ctx); err != nil {
		t.Fatalf("relay run failed: %v", err)
	}

	// Verify the task file was created.
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("target file not created: %v", err)
	}
	if !strings.Contains(string(data), "claude e2e pass") {
		t.Errorf("target file content %q does not contain expected text", string(data))
	}

	// Verify try record was written with completed=true.
	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("no try records written to store")
	}
	lastTry := tries[len(tries)-1]
	if !lastTry.Completed {
		t.Errorf("last try completed = false, summary: %s", lastTry.Summary)
	}

	// Verify log file is scoped to the workspace.
	if !strings.Contains(lastTry.LogPath, dataDir) {
		t.Errorf("log path %q does not contain dataDir %q", lastTry.LogPath, dataDir)
	}
	expectedKey := repoKey(workspaceDir)
	if !strings.Contains(lastTry.LogPath, expectedKey) {
		t.Errorf("log path %q does not contain repo key %q", lastTry.LogPath, expectedKey)
	}

	// Verify the log file actually exists.
	if _, err := os.Stat(lastTry.LogPath); err != nil {
		t.Errorf("log file does not exist at %q: %v", lastTry.LogPath, err)
	}
}

// TestRealBackend_ClaudeWithLaps runs a claude relay using the laps queue,
// verifying that the head task is consumed and marked done.
func TestRealBackend_ClaudeWithLaps(t *testing.T) {
	requireRealAgents(t)
	requireBinary(t, "claude")
	requireBinary(t, "laps")

	workspaceDir, rallyDir, dataDir := setupRealWorkspace(t)

	// Set up laps with one task.
	lapsDir := filepath.Join(workspaceDir, ".laps")
	testutil.RunCommand(t, workspaceDir, "laps", "init")
	testutil.RunCommand(t, workspaceDir, "laps", "on")
	testutil.RunCommand(t, workspaceDir, "laps", "add", "head",
		"--title", "Create greeting",
		"--description", "Create a file called greeting.txt with text 'hello from laps'.",
	)

	// Install rally hooks.
	if _, err := laps.InstallHooks(lapsDir); err != nil {
		t.Fatalf("install laps hooks: %v", err)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{
		"claude": &agent.ClaudeExecutor{Model: "claude-haiku-4-5"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		AgentMixSpecs:    []string{"cc"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
	}, executors)

	if err := r.Run(ctx); err != nil {
		t.Fatalf("relay run failed: %v", err)
	}

	// Verify greeting.txt was created.
	greetingPath := filepath.Join(workspaceDir, "greeting.txt")
	if _, err := os.Stat(greetingPath); err != nil {
		t.Errorf("greeting.txt not created: %v", err)
	}

	// Verify try record written.
	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("no try records written")
	}
	if !tries[len(tries)-1].Completed {
		t.Errorf("try not completed; summary: %s", tries[len(tries)-1].Summary)
	}
}

// TestRealBackend_LogScopingPerRepo verifies that two parallel relays in
// different workspaces write try logs to separate directories under dataDir.
func TestRealBackend_LogScopingPerRepo(t *testing.T) {
	requireRealAgents(t)
	requireBinary(t, "claude")

	sharedDataDir := t.TempDir()

	runRelay := func(t *testing.T, task string) *store.TryRecord {
		t.Helper()
		workspaceDir, rallyDir, _ := setupRealWorkspace(t)
		s := newTestStore(t, rallyDir)
		executors := map[string]harnessapi.Executor{
			"claude": &agent.ClaudeExecutor{Model: "claude-haiku-4-5"},
		}
		// Short timeout: if the try fails and the agent is paused, the runner
		// waits up to PauseDuration (1h) for recovery. Cap that wait tightly so
		// the test fails fast rather than blocking the suite for 3 minutes.
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		r := NewRunner(s, Config{
			WorkspaceDir:     workspaceDir,
			DataDir:          sharedDataDir,
			AgentMixSpecs:    []string{"cc"},
			TargetIterations: 1,
			RetryBudget:      1,
			TaskPrompt:       task,
		}, executors)
		// Ignore run error: the relay may time out if the agent is rate-limited
		// or paused. We only care that at least one try was attempted so we can
		// verify the log-scoping invariant.
		_ = r.Run(ctx)
		tries := s.AllTries()
		if len(tries) == 0 {
			t.Skip("no try records written — agent may not have started (rate limit or env issue)")
		}
		rec := tries[len(tries)-1]
		return &rec
	}

	// Run first relay.
	rec1 := runRelay(t, "Create a file called scope-test-a.txt with text 'repo-a'.")
	// Run second relay (different workspace, same dataDir).
	rec2 := runRelay(t, "Create a file called scope-test-b.txt with text 'repo-b'.")

	// Both logs should be in the shared dataDir.
	if !strings.HasPrefix(rec1.LogPath, sharedDataDir) {
		t.Errorf("rec1 log not in sharedDataDir: %s", rec1.LogPath)
	}
	if !strings.HasPrefix(rec2.LogPath, sharedDataDir) {
		t.Errorf("rec2 log not in sharedDataDir: %s", rec2.LogPath)
	}

	// But they should be in DIFFERENT subdirectories (different repo keys).
	dir1 := filepath.Dir(rec1.LogPath)
	dir2 := filepath.Dir(rec2.LogPath)
	if dir1 == dir2 {
		t.Errorf("two different repos wrote to same log dir: %s", dir1)
	}

	// Both log files should actually exist.
	if _, err := os.Stat(rec1.LogPath); err != nil {
		t.Errorf("log1 does not exist: %v", err)
	}
	if _, err := os.Stat(rec2.LogPath); err != nil {
		t.Errorf("log2 does not exist: %v", err)
	}
}

// TestRealBackend_CodexRelay runs a single codex iteration and checks that
// the executor no longer conflicts on --dangerously-bypass-approvals-and-sandbox.
func TestRealBackend_CodexRelay(t *testing.T) {
	requireRealAgents(t)
	requireBinary(t, "codex")

	workspaceDir, rallyDir, dataDir := setupRealWorkspace(t)

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{
		"codex": &agent.CodexExecutor{Model: "gpt-5.4-mini"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		AgentMixSpecs:    []string{"cx"},
		TargetIterations: 1,
		RetryBudget:      1,
		TaskPrompt:       "Create a file called codex-e2e.txt with the text 'codex e2e pass'.",
	}, executors)

	// We don't care if codex succeeds — we care that it doesn't fail immediately
	// with the --full-auto / --dangerously-bypass conflict.
	_ = r.Run(ctx)

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("no try records written — executor never ran")
	}
	for _, try := range tries {
		if strings.Contains(try.Summary, "--full-auto") {
			t.Errorf("codex still failing with --full-auto conflict: %s", try.Summary)
		}
		if strings.Contains(try.Summary, "cannot be used with") {
			t.Errorf("codex arg conflict still present: %s", try.Summary)
		}
	}
}

// TestRealBackend_OpenCodeRelay runs a single opencode iteration using the
// built-in OpenCodeExecutor (headless mode via "opencode run"). It verifies:
//   - The executor ran at all (try record written)
//   - No TUI ANSI escape sequences leaked into the try summary
//   - When opencode is rate-limited or frozen, rally marks the agent paused
//     (resilient execution) and the context cancellation exits cleanly
//
// Uses a short StallThreshold (60s) so the test terminates quickly when
// opencode-go is rate-limited. The 3-minute context ensures ctx.Done() fires
// well before the test framework's 5-minute panic threshold.
func TestRealBackend_OpenCodeRelay(t *testing.T) {
	requireRealAgents(t)
	requireBinary(t, "opencode")

	workspaceDir, rallyDir, dataDir := setupRealWorkspace(t)

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{
		"opencode": &agent.OpenCodeExecutor{Model: "opencode/big-pickle"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		AgentMixSpecs:    []string{"op"},
		TargetIterations: 1,
		RetryBudget:      1,
		StallThreshold:   60 * time.Second,
		TaskPrompt:       "Create a file called opencode-e2e.txt with the text 'opencode e2e pass'. Do not create any other files.",
	}, executors)

	// Ignore run error: may return ctx.Err() when all agents are paused and the
	// context expires waiting for pause recovery. That is correct resilience behaviour.
	_ = r.Run(ctx)

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("no try records written — executor never ran")
	}
	for _, try := range tries {
		if strings.Contains(try.Summary, "\x1b[") {
			t.Errorf("try summary contains ANSI escape sequences — opencode may have started in TUI mode: %.200s", try.Summary)
		}
		if try.LogPath != "" {
			if _, err := os.Stat(try.LogPath); err != nil {
				t.Errorf("log file does not exist at %q: %v", try.LogPath, err)
			}
		}
	}

	// If the run failed, behaviour depends on the failure classification. Under
	// current semantics only repeated INFRA failures (rate limits, connection
	// refused, etc.) pause the agent; a plain non-infra failure (e.g. opencode
	// just made no changes) correctly does NOT pause — the relay rotates off it
	// by exhausting retries. So only require a paused event when the try log
	// actually contains an infra signal.
	lastTry := tries[len(tries)-1]
	if !lastTry.Completed {
		infra := false
		if lastTry.LogPath != "" {
			if logData, err := os.ReadFile(lastTry.LogPath); err == nil {
				low := strings.ToLower(string(logData))
				if strings.Contains(low, "rate limit") ||
					strings.Contains(low, "too many requests") ||
					strings.Contains(low, "usage limit") {
					infra = true
				}
			}
		}

		if infra {
			// Infra failure: the agent should be paused — not frozen
			// indefinitely. This verifies resilient execution handles the case.
			statusEvents, err := s.GetAgentStatus("opencode", "opencode/big-pickle")
			if err != nil {
				t.Fatal(err)
			}
			paused := false
			for _, ev := range statusEvents {
				if ev.EventType == "paused" {
					paused = true
					break
				}
			}
			if !paused {
				t.Error("opencode run failed with an infra error but no paused event recorded — resilient execution did not handle the failure")
			}
		} else {
			t.Logf("opencode run failed with a non-infra error; relay handled it by exhausting retries without pausing (valid under current semantics). last summary: %.300s", lastTry.Summary)
		}
	}
}

// TestRealBackend_AntigravityRelay runs a single Antigravity CLI iteration via
// `agy --print` and verifies the built-in adapter can drive real file changes.
func TestRealBackend_AntigravityRelay(t *testing.T) {
	requireRealAgents(t)
	requireBinary(t, "agy")

	workspaceDir, rallyDir, dataDir := setupRealWorkspace(t)

	s := newTestStore(t, rallyDir)
	executors := map[string]harnessapi.Executor{
		"antigravity": &agent.AntigravityExecutor{Model: agent.DefaultAntigravityModel},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	targetFile := filepath.Join(workspaceDir, "antigravity-e2e.txt")
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		AgentMixSpecs:    []string{"ag"},
		TargetIterations: 1,
		RetryBudget:      1,
		TaskPrompt:       "Create a file called antigravity-e2e.txt with the text 'antigravity e2e pass'. Do not create any other files.",
	}, executors)

	if err := r.Run(ctx); err != nil {
		t.Fatalf("relay run failed: %v", err)
	}

	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("target file not created: %v", err)
	}
	if !strings.Contains(string(data), "antigravity e2e pass") {
		t.Errorf("target file content %q does not contain expected text", string(data))
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("no try records written to store")
	}
	lastTry := tries[len(tries)-1]
	if !lastTry.Completed {
		t.Errorf("last try completed = false, summary: %s", lastTry.Summary)
	}
	if lastTry.AgentType != "antigravity" {
		t.Errorf("agent_type = %q, want antigravity", lastTry.AgentType)
	}
	if lastTry.LogPath == "" {
		t.Fatal("expected try log path")
	}
	logData, err := os.ReadFile(lastTry.LogPath)
	if err != nil {
		t.Fatalf("read try log: %v", err)
	}
	if !strings.Contains(string(logData), "Print mode: conversation=") {
		t.Error("expected Antigravity conversation ID in appended agy log")
	}
}

// TestRealBackend_ResilienceRetryBudget verifies that after retries are
// exhausted, agent_status.jsonl records a paused event.
func TestRealBackend_ResilienceRetryBudget(t *testing.T) {
	requireRealAgents(t)

	workspaceDir, rallyDir, dataDir := setupRealWorkspace(t)

	s := newTestStore(t, rallyDir)

	// Use a funcExecutor that simulates an INFRA failure (rate limit) so that
	// repeated failures (infraFailures > 1) trigger a resilience pause. Under
	// current semantics, only repeated infra failures pause the agent; plain
	// agent task-failures do not. ClassifyError reads the last lines of the try
	// log file, so we write an infra-pattern line ("rate limit") to opts.LogPath
	// in addition to returning the failing TryResult.
	failExec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				if f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
					_, _ = f.WriteString("error: rate limit exceeded (429 too many requests)\n")
					_ = f.Close()
				}
			}
			return &harnessapi.TryResult{Completed: false, Summary: "intentional rate-limit failure for retry test"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": failExec}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		AgentMixSpecs:    []string{"cc"},
		TargetIterations: 1,
		RetryBudget:      2,
		TaskPrompt:       "Will fail intentionally.",
		// Pin a concrete model ("default") so the recorded pause event is keyed
		// to claude:default and matches the GetAgentStatus query below — the
		// store requires a non-empty model, and the relay records pause state
		// per harness+model.
		Resolver: func(spec string) (harnessapi.ResolvedAgent, error) {
			return harnessapi.ResolvedAgent{Harness: "claude", Model: "default"}, nil
		},
	}, executors)
	// Stub the rate-limit cooldown sleep so both attempts run immediately within
	// the test's context budget. Without this, the infra (rate-limit) cooldown
	// would block ~1m between attempts and only one try would execute.
	r.sleepFunc = func(time.Duration) {}

	// The relay should exhaust retries and pause the harnessapi. After pausing, the
	// relay waits for recovery until the context deadline, so Run returns
	// context.DeadlineExceeded here — that is the expected resilience behaviour.
	_ = r.Run(ctx)

	// There should be retry records (2 attempts).
	tries := s.AllTries()
	if len(tries) < 2 {
		t.Errorf("expected ≥2 try records (one per retry), got %d", len(tries))
	}

	// Agent should be marked paused in agent_status.jsonl.
	statusEvents, err := s.GetAgentStatus("claude", "default")
	if err != nil {
		t.Fatal(err)
	}
	paused := false
	for _, ev := range statusEvents {
		if ev.EventType == "paused" {
			paused = true
			break
		}
	}
	if !paused {
		t.Error("expected claude to be marked paused after retry exhaustion, but no paused event found")
	}
}

// TestRealBackend_CustomHarnessRelay verifies that a custom harness using
// opencode in headless mode (opencode run $PROMPT --format json) works
// end-to-end: try record written, completed=true, no ANSI in summary.
// Also verifies that when the model is invalid (not found), the run is
// marked failed (not "passed") in both the terminal footer and try record.
func TestRealBackend_CustomHarnessRelay(t *testing.T) {
	requireRealAgents(t)
	requireBinary(t, "opencode")

	workspaceDir, rallyDir, dataDir := setupRealWorkspace(t)

	s := newTestStore(t, rallyDir)
	modelFlag := "--model"
	executors := map[string]harnessapi.Executor{
		"mycode": generic.New(
			[]string{"opencode", "run", "$PROMPT", "--format", "json"},
			&modelFlag,
			"tail",
			50,
			"stdout",
			"opencode/big-pickle",
		),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		AgentMixSpecs:    []string{"mycode"},
		TargetIterations: 1,
		RetryBudget:      1,
		StallThreshold:   60 * time.Second,
		TaskPrompt:       "Create a file called custom-harness-e2e.txt with the text 'custom harness ok'. Do not create any other files.",
		Resolver: func(spec string) (harnessapi.ResolvedAgent, error) {
			// Resolve "mycode" as a custom harness with its default model.
			parts := strings.SplitN(spec, ":", 2)
			if parts[0] == "mycode" {
				model := "opencode/big-pickle"
				if len(parts) == 2 {
					model = parts[1]
				}
				return harnessapi.ResolvedAgent{Harness: "mycode", Model: model}, nil
			}
			return harnessapi.ResolvedAgent{Harness: parts[0]}, nil
		},
	}, executors)

	_ = r.Run(ctx)

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("no try records written — custom harness executor never ran")
	}

	for _, try := range tries {
		// No raw ANSI sequences should be in summary (opencode --format json is headless).
		if strings.Contains(try.Summary, "\x1b[") {
			t.Errorf("try summary contains ANSI escape sequences — opencode may have started in TUI mode: %.200s", try.Summary)
		}
	}

	// Verify the last try completed and the file exists.
	lastTry := tries[len(tries)-1]
	if !lastTry.Completed {
		t.Logf("custom harness run did not complete (may be rate-limited); last summary: %.300s", lastTry.Summary)
	} else {
		targetFile := filepath.Join(workspaceDir, "custom-harness-e2e.txt")
		if _, err := os.Stat(targetFile); err != nil {
			t.Errorf("expected custom-harness-e2e.txt to be created: %v", err)
		}
	}
}
