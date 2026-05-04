package relay

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
)

type funcExecutor struct {
	fn func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error)
}

func (f *funcExecutor) Execute(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
	return f.fn(ctx, opts)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Rally Test")
	runGit(t, dir, "config", "user.email", "rally@example.com")
	// Exclude only rally's ephemeral files from git status.
	// Rally's JSONL state files are now committed as durable git-backed state.
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	os.WriteFile(excludePath, []byte(".rally/current_task.md\n.rally/relays/\n"), 0o644)
}

func newTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInstructionsPassedToExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	var receivedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			receivedTaskPrompt = opts.TaskPrompt
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		Instructions:     "Always write tests.",
		TaskPrompt:       "Build user auth.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Always write tests." {
		t.Errorf("expected instructions 'Always write tests.', got %q", receivedInstructions)
	}
	if receivedTaskPrompt != "Build user auth." {
		t.Errorf("expected task prompt 'Build user auth.', got %q", receivedTaskPrompt)
	}
}
func TestAgentCyclingDeterminism(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var agents []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			agents = append(agents, opts.Persona)
			changeCounter++
			// Append unique content so each try produces a distinct change.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec, "codex": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1", "cx:1"},
		TargetIterations: 4,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	want := []string{"claude", "codex", "claude", "codex"}
	if len(agents) != len(want) {
		t.Fatalf("got %d tries, want %d", len(agents), len(want))
	}
	for i, w := range want {
		if agents[i] != w {
			t.Fatalf("try %d agent = %q, want %q", i, agents[i], w)
		}
	}
}

func TestFilesChangedCountUsesCommitDiff(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "one.txt"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, "two.txt"), []byte("two\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	before := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	os.WriteFile(filepath.Join(workspaceDir, "one.txt"), []byte("one changed\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, "two.txt"), []byte("two changed\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "change two files")

	after := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	if got := r.filesChangedCount(nil, before, after, after); got != 2 {
		t.Fatalf("expected 2 changed files from commit diff, got %d", got)
	}
}

func TestRetryWithinRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt < 3 {
				return &agent.TryResult{Completed: false, Summary: "fail"}, nil
			}
			// Create a file so the successful try is not a no-op failure.
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("success-%d.txt", attempt)))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	tries := s.AllTries()
	if len(tries) != 3 {
		t.Fatalf("got %d tries, want 3", len(tries))
	}
	for i, tr := range tries {
		if tr.RunID != 1 {
			t.Fatalf("try %d runID = %d, want 1", i, tr.RunID)
		}
		if tr.AttemptNumber != i+1 {
			t.Fatalf("try %d attempt = %d, want %d", i, tr.AttemptNumber, i+1)
		}
	}
	if tries[2].Completed != true {
		t.Fatal("final try should be completed")
	}
	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 1 {
		t.Fatalf("expected 1 completed iteration, got %+v", relays)
	}
}

func TestRetryExhaustionTriggersPause(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) < 3 {
		t.Fatalf("got %d tries, want at least 3", len(tries))
	}

	status := s.GetAgentStatus("claude")
	foundPause := false
	for _, e := range status {
		if e.EventType == "paused" {
			foundPause = true
			break
		}
	}
	if !foundPause {
		t.Fatal("expected agent paused event")
	}
}

func TestGracefulStop(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			time.Sleep(50 * time.Millisecond)
			changeCounter++
			// Append unique content so the try is not a no-op failure.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "stop %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 5,
	}, executors)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	r.RequestStop()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop in time")
	}

	relays := s.AllRelays()
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relays))
	}
	if relays[0].CompletedIterations >= 5 {
		t.Fatalf("expected < 5 iterations after stop, got %d", relays[0].CompletedIterations)
	}
}

func TestMessageConsumptionPerRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "msg1", Status: "pending", Position: 1})
	s.AddMessage(store.MessageRecord{ID: 2, Body: "msg2", Status: "pending", Position: 2})

	addressed := false
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			addressed = true
			msgAddr := true
			changeCounter++
			// Append unique content so the try is not a no-op failure.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "msg %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if !addressed {
		t.Fatal("expected message to be addressed")
	}
	msgs := s.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	pending := 0
	for _, m := range msgs {
		if m.Status == "pending" {
			pending++
		}
	}
	if pending != 0 {
		t.Fatalf("expected 0 pending messages, got %d", pending)
	}
}

func TestCommitHashTracking_AgentCommitted(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Copy fixture project into workspace
	CopyFixtureProject(t, workspaceDir)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	diffPath, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "diffs", "add-feature.diff"))
	outputPath, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "outputs", "success.json"))
	exec := &agent.FixtureExecutor{
		DiffPath:   diffPath,
		OutputPath: outputPath,
		Dir:        workspaceDir,
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected agent commit hash")
	}
}

func TestCommitHashTracking_AutoCommitted(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			// Create a file but don't commit it
			f, err := os.Create(filepath.Join(workspaceDir, "auto.txt"))
			if err != nil {
				return nil, err
			}
			f.WriteString("auto")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected auto-commit hash")
	}
}

func TestCommitHashTracking_NoChanges(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	// No-op tries (no changes + <3min) are treated as failures and retried up to 3x.
	// With the fix, failed runs don't count toward target, so the relay runs
	// hourly retries after pausing. Initial run has 3 tries; hourly retries have 1 each.
	tries := s.AllTries()
	if len(tries) <= 3 {
		t.Fatalf("expected > 3 tries (initial 3 + hourly retries), got %d", len(tries))
	}
	// First 3 tries should have no commit hash
	for i := 0; i < 3; i++ {
		if tries[i].CommitHash != "" {
			t.Fatalf("try %d expected no commit hash, got %q", i, tries[i].CommitHash)
		}
	}
}

func TestRelayScopedMessageIncludedInAllRuns(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})

	var relayMsgsSeen []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			relayMsgsSeen = append(relayMsgsSeen, opts.RelayMessage)
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 3,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(relayMsgsSeen) != 3 {
		t.Fatalf("expected relay message in 3 runs, got %d", len(relayMsgsSeen))
	}
	for i, msg := range relayMsgsSeen {
		if msg != "relay-msg" {
			t.Fatalf("run %d relay message = %q, want 'relay-msg'", i, msg)
		}
	}
}

func TestRelayScopedMessageAddressed(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})

	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			msgAddr := true
			return &agent.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	relay := s.AllRelays()
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relay))
	}
	foundRelayMsg := false
	for _, id := range relay[0].ConsumedMessageIDs {
		if id == 1 {
			foundRelayMsg = true
		}
	}
	if !foundRelayMsg {
		t.Fatal("expected relay message ID 1 in ConsumedMessageIDs")
	}

	msgs := s.GetMessages()
	for _, m := range msgs {
		if m.ID == 1 && m.Status != "addressed" {
			t.Fatalf("expected relay message addressed, got %s", m.Status)
		}
	}
}

func TestCombinedRelayAndRunScopedMessages(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})
	s.AddMessage(store.MessageRecord{ID: 2, Body: "run-msg-1", Status: "pending", Position: 2})
	s.AddMessage(store.MessageRecord{ID: 3, Body: "run-msg-2", Status: "pending", Position: 3})

	var relayMsgsSeen []string
	var inboxMsgsSeen []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			relayMsgsSeen = append(relayMsgsSeen, opts.RelayMessage)
			inboxMsgsSeen = append(inboxMsgsSeen, opts.InboxMessage)
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			msgAddr := true
			return &agent.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// Relay message seen in both runs
	if len(relayMsgsSeen) != 2 {
		t.Fatalf("expected relay message in 2 runs, got %d", len(relayMsgsSeen))
	}
	for i, msg := range relayMsgsSeen {
		if msg != "relay-msg" {
			t.Fatalf("run %d relay message = %q, want 'relay-msg'", i, msg)
		}
	}

	// Inbox messages: each run gets a different run-scoped message
	if len(inboxMsgsSeen) != 2 {
		t.Fatalf("expected inbox message in 2 runs, got %d", len(inboxMsgsSeen))
	}
	if inboxMsgsSeen[0] != "run-msg-1" {
		t.Fatalf("run 1 inbox = %q, want 'run-msg-1'", inboxMsgsSeen[0])
	}
	if inboxMsgsSeen[1] != "run-msg-2" {
		t.Fatalf("run 2 inbox = %q, want 'run-msg-2'", inboxMsgsSeen[1])
	}

	// All messages should be addressed
	msgs := s.GetMessages()
	for _, m := range msgs {
		if m.Status != "addressed" {
			t.Fatalf("expected message %d to be addressed, got %s", m.ID, m.Status)
		}
	}

	// Relay ConsumedMessageIDs should have all 3 message IDs
	relay := s.AllRelays()
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relay))
	}
	if len(relay[0].ConsumedMessageIDs) != 3 {
		t.Fatalf("expected 3 consumed message IDs, got %v", relay[0].ConsumedMessageIDs)
	}
}

func TestFreezeCascade(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	// Verify agent is paused after 3 retries exhausted
	st, _ := NewResilience(s).getState("claude")
	if st != StatePaused {
		t.Fatalf("expected agent paused after retry exhaustion, got %s", st)
	}

	// Simulate 5 hourly retries failing with progressively advancing times
	counter := 0
	resilience := &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc: func() time.Time {
			counter++
			return time.Date(2026, 1, 1, counter, 0, 0, 0, time.UTC)
		},
	}

	for i := 0; i < 5; i++ {
		if err := resilience.RecordHourlyFailure("claude", 1); err != nil {
			t.Fatalf("RecordHourlyFailure %d failed: %v", i+1, err)
		}
	}

	// Verify agent is now frozen
	st, _ = resilience.getState("claude")
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after 5 hourly retries, got %s", st)
	}

	// Verify a "frozen" event was recorded
	events := s.GetAgentStatus("claude")
	foundFrozen := false
	for _, e := range events {
		if e.EventType == "frozen" {
			foundFrozen = true
			break
		}
	}
	if !foundFrozen {
		t.Fatal("expected frozen event in agent status")
	}
}

func TestAgentUnfreeze(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.PauseAgent("claude", 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	st, _ := resilience.getState("claude")
	if st != StatePaused {
		t.Fatalf("expected StatePaused after pause, got %s", st)
	}

	if err := resilience.UnpauseAgent("claude", 1); err != nil {
		t.Fatalf("UnpauseAgent failed: %v", err)
	}

	st, _ = resilience.getState("claude")
	if st != StateActive {
		t.Fatalf("expected StateActive after unpause, got %s", st)
	}
}

func TestFailedRunDoesNotCountIteration(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	relays := s.AllRelays()
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relays))
	}
	if relays[0].CompletedIterations != 0 {
		t.Fatalf("expected 0 completed iterations after failed run, got %d", relays[0].CompletedIterations)
	}

	st, _ := NewResilience(s).getState("claude")
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after hourly retry exhaustion, got %s", st)
	}
}

func TestHourlyRetryWithOtherAgentActive(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resilience := &Resilience{
		Store:         s,
		PauseDuration: time.Hour,
		NowFunc:       func() time.Time { return baseTime },
	}

	if err := resilience.PauseAgent("claude", 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	resilience.NowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }

	mix, err := ParseAgentMix([]string{"cc:2", "cx:1"})
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	agent, nextRunIndex, isHourlyRetry, err := resilience.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("SelectActiveAgent failed: %v", err)
	}
	if agent != "claude" {
		t.Fatalf("expected claude (hourly retry), got %s", agent)
	}
	if nextRunIndex != 1 {
		t.Fatalf("expected nextRunIndex 1, got %d", nextRunIndex)
	}
	if !isHourlyRetry {
		t.Fatal("expected isHourlyRetry=true")
	}
}

func TestAllAgentsFrozenEndsRelay(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.FreezeAgent("claude", 1); err != nil {
		t.Fatalf("FreezeAgent claude failed: %v", err)
	}
	if err := resilience.FreezeAgent("codex", 1); err != nil {
		t.Fatalf("FreezeAgent codex failed: %v", err)
	}

	mix, err := ParseAgentMix([]string{"cc:1", "cx:1"})
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	_, _, _, err = resilience.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error from SelectActiveAgent")
	}
	if err.Error() != "all agents frozen" {
		t.Fatalf("expected 'all agents frozen' error, got %q", err.Error())
	}
}
