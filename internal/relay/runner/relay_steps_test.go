package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestAgentCyclingDeterminism(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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

func TestRunnerSameHarnessAdvanceUsesRotateModel(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var executedModels []string
	exec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			f, _ := os.OpenFile(filepath.Join(workspaceDir, fmt.Sprintf("change-%d.txt", len(executedModels))), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString(opts.Model)
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:model-a:1", "op:model-b:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "rotate",
	}, map[string]agent.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := exec.rotateCalls; len(got) != 1 || got[0] != "model-b" {
		t.Fatalf("RotateModel calls = %v, want [model-b]", got)
	}
	if got, want := executedModels, []string{"model-a", "model-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}
}

func TestRunnerCrossHarnessAdvanceDoesNotRotate(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	opExec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "opencode.txt"), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString("opencode")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}
	codexExec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "codex.txt"), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString("codex")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:model-a:1", "cx:model-b:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "cross harness",
	}, map[string]agent.Executor{
		"opencode": opExec,
		"codex":    codexExec,
	})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(opExec.rotateCalls) != 0 {
		t.Fatalf("RotateModel calls = %v, want none for cross-harness advance", opExec.rotateCalls)
	}
}

func TestRunnerRotateModelErrorFallsBackToExecution(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	dataDir := t.TempDir()
	var executedModels []string
	exec := &funcExecutor{
		rotateSupported: true,
		rotateErr:       fmt.Errorf("rotate failed"),
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			f, _ := os.OpenFile(filepath.Join(workspaceDir, fmt.Sprintf("change-%d.txt", len(executedModels))), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString(opts.Model)
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      dataDir,
		RouteSpecs: map[string][]string{
			"default": {"op:model-a:1", "op:model-b:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "fallback",
	}, map[string]agent.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := exec.rotateCalls; len(got) != 1 || got[0] != "model-b" {
		t.Fatalf("RotateModel calls = %v, want [model-b]", got)
	}
	if got, want := executedModels, []string{"model-a", "model-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}

	logData, err := os.ReadFile(relayLogPath(dataDir, workspaceDir, 1))
	if err != nil {
		t.Fatalf("read relay log: %v", err)
	}
	if !strings.Contains(string(logData), "rotate fallback for opencode: rotate failed") {
		t.Fatalf("relay log = %q, want rotate fallback message", string(logData))
	}
}

func TestRunnerDoesNotCreateRepoRelayLogDir(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, ".gitignore"), []byte("state/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	dataDir := t.TempDir()
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "change.txt"), []byte("ok\n"), 0o644); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		RouteSpecs:       map[string][]string{"default": {"op:model:1"}},
		TargetIterations: 1,
		Resolver:         testResolver,
		TaskPrompt:       "no repo relay logs",
	}, map[string]agent.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if _, err := os.Stat(relayLogPath(dataDir, workspaceDir, 1)); err != nil {
		t.Fatalf("expected data-dir relay log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rallyDir, "relays")); !os.IsNotExist(err) {
		t.Fatalf(".rally/relays should not be created when .rally/.gitignore only ignores state/, stat err=%v", err)
	}
}

func TestFailureCascadeMultipleInfraIncrements(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      3,
		Resolver:         cheapTestResolver,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}
	r.sleepFunc = func(time.Duration) {}

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) < 3 {
		t.Fatalf("got %d tries, want at least 3", len(tries))
	}

	status, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
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

func TestFailureCascadeSingleInfraDoesNotIncrement(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "infra"}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         cheapTestResolver,
	}, map[string]agent.Executor{"opencode": exec})

	_ = r.Run(context.Background())

	if got := countAgentStatusEvents(s, "paused", "frozen"); got != 0 {
		t.Fatalf("cascade events = %d, want 0", got)
	}
}

func TestFailureCascadeAgentErrorDoesNotIncrement(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "agent failed"}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      2,
		Resolver:         cheapTestResolver,
	}, map[string]agent.Executor{"opencode": exec})

	_ = r.Run(context.Background())

	if got := countAgentStatusEvents(s, "paused", "frozen"); got != 0 {
		t.Fatalf("cascade events = %d, want 0", got)
	}
}

func countAgentStatusEvents(s *store.Store, eventTypes ...string) int {
	wanted := map[string]bool{}
	for _, eventType := range eventTypes {
		wanted[eventType] = true
	}
	count := 0
	for _, event := range s.AllAgentStatus() {
		if wanted[event.EventType] {
			count++
		}
	}
	return count
}

func TestGracefulStop(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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

func TestRelayScopedMessageIncludedInAllRuns(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      3,
		Resolver:         cheapTestResolver,
	}, executors)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	// Verify agent is paused after 3 retries exhausted
	cheapKey := ResilienceKey{Harness: "opencode", Model: cheapTestModel}
	st, _ := NewResilience(s).GetState(cheapKey)
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
		if err := resilience.RecordHourlyFailure(cheapKey, 1); err != nil {
			t.Fatalf("RecordHourlyFailure %d failed: %v", i+1, err)
		}
	}

	// Verify agent is now frozen
	st, _ = resilience.GetState(cheapKey)
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after 5 hourly retries, got %s", st)
	}

	// Verify a "frozen" event was recorded
	events, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
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
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	st, _ := resilience.GetState(ResilienceKey{Harness: "claude", Model: "test"})
	if st != StatePaused {
		t.Fatalf("expected StatePaused after pause, got %s", st)
	}

	if err := resilience.UnpauseAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1); err != nil {
		t.Fatalf("UnpauseAgent failed: %v", err)
	}

	st, _ = resilience.GetState(ResilienceKey{Harness: "claude", Model: "test"})
	if st != StateActive {
		t.Fatalf("expected StateActive after unpause, got %s", st)
	}
}

func TestFailedRunDoesNotCountIteration(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      3,
		Resolver:         cheapTestResolver,
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

	st, _ := NewResilience(s).GetState(ResilienceKey{Harness: "opencode", Model: cheapTestModel})
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after hourly retry exhaustion, got %s", st)
	}
}

func TestHourlyRetryWithOtherAgentActive(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resilience := &Resilience{
		Store:         s,
		PauseDuration: time.Hour,
		NowFunc:       func() time.Time { return baseTime },
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	resilience.NowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude", Model: "test"},
			{Harness: "claude", Model: "test"},
			{Harness: "codex", Model: "test"},
		},
	}

	picked, nextRunIndex, isHourlyRetry, err := resilience.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("SelectActiveAgent failed: %v", err)
	}
	if picked.Harness != "claude" {
		t.Fatalf("expected claude (hourly retry), got %s", picked.Harness)
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
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.FreezeAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent claude failed: %v", err)
	}
	if err := resilience.FreezeAgent(ResilienceKey{Harness: "codex", Model: "test"}, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent codex failed: %v", err)
	}

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude", Model: "test"},
			{Harness: "codex", Model: "test"},
		},
	}

	_, _, _, err := resilience.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error from SelectActiveAgent")
	}
	if err.Error() != "all agents frozen" {
		t.Fatalf("expected 'all agents frozen' error, got %q", err.Error())
	}
}

func TestRunnerRouteIntegration_AssigneesQuotasFreezeAndRoleFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	agentsDir := filepath.Join(rallyDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	if err := os.WriteFile(filepath.Join(agentsDir, "SENIOR.md"), []byte("Senior route guidance."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "junior.md"), []byte("Junior route guidance."), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)

	type execution struct {
		persona          string
		roleInstructions string
		taskPrompt       string
	}

	var executions []execution
	failSeniorCheapAttempts := 3
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.Persona == "opencode" && opts.Model == cheapTestModel && opts.RoleInstructions == "Senior route guidance." && failSeniorCheapAttempts > 0 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
				}
				failSeniorCheapAttempts--
				return &agent.TryResult{Completed: false, Summary: "simulated senior cheap-model failure"}, nil
			}

			executions = append(executions, execution{
				persona:          opts.Persona,
				roleInstructions: opts.RoleInstructions,
				taskPrompt:       opts.TaskPrompt,
			})
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}
	executors := map[string]agent.Executor{
		"opencode": exec,
		"codex":    exec,
	}

	oldHeadPull := headPullLap
	headPullCalls := 0
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		headPullCalls++
		switch headPullCalls {
		case 1, 2:
			return laps.Lap{Title: "senior task", Description: "work senior item", Assignee: "SENIOR"}, nil
		case 3, 4:
			return laps.Lap{Title: "junior task", Description: "work junior item", Assignee: "JUNIOR"}, nil
		default:
			return laps.Lap{Title: "default task", Description: "work default item"}, nil
		}
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": []string{"cx:1"},
			"SENIOR":  []string{"op:dsf", "cx:1"},
			"JUNIOR":  []string{"cx:2"},
		},
		TargetIterations: 4,
		RetryBudget:      3,
		LapsEnabled:      true,
		Instructions:     "Base instructions.",
		Resolver:         cheapTestResolver,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	wantPersonas := []string{"codex", "codex", "codex", "codex"}
	wantRoleInstructions := []string{
		"Senior route guidance.",
		"Junior route guidance.",
		"Junior route guidance.",
		"",
	}
	wantPrompts := []string{
		"work senior item",
		"work junior item",
		"work junior item",
		"work default item",
	}

	if len(executions) != len(wantPersonas) {
		t.Fatalf("executions = %d, want %d", len(executions), len(wantPersonas))
	}
	for i := range executions {
		if executions[i].persona != wantPersonas[i] {
			t.Fatalf("execution %d persona = %q, want %q", i+1, executions[i].persona, wantPersonas[i])
		}
		if executions[i].roleInstructions != wantRoleInstructions[i] {
			t.Fatalf("execution %d role instructions = %q, want %q", i+1, executions[i].roleInstructions, wantRoleInstructions[i])
		}
		if executions[i].taskPrompt != wantPrompts[i] {
			t.Fatalf("execution %d task prompt = %q, want %q", i+1, executions[i].taskPrompt, wantPrompts[i])
		}
	}

	st, _ := r.resilience.GetState(ResilienceKey{Harness: "opencode", Model: cheapTestModel})
	if st != StatePaused {
		t.Fatalf("cheap model state = %s, want %s after simulated freeze", st, StatePaused)
	}
}

func TestRunnerNoBackendUsesDefaultRouteAndFallbackPrompt(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedPersona string
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedPersona = opts.Persona
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"codex": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		RouteSpecs:        map[string][]string{"default": []string{"cx:1"}},
		TargetIterations:  1,
		LapsEnabled:       false,
		FreeRunPromptFile: fallbackFile,
		Resolver:          testResolver,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedPersona != "codex" {
		t.Fatalf("persona = %q, want codex", receivedPersona)
	}
	if receivedTaskPrompt != "Fallback prompt content." {
		t.Fatalf("task prompt = %q, want fallback prompt", receivedTaskPrompt)
	}
}

// --- Phase 15: Verification — execution paths ---

func writeRelayScript(t *testing.T, dir, name, scriptContent string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestE2E_FullConfig_NamedModelsAndFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Custom fallback instructions."), 0o644)

	configContent := fmt.Sprintf(`schema_version = 2

[defaults]
iterations = 2
mix = "cc:opus"

[harness.cc.models]
opus = "claude-opus-4-7"

[fallback]
instructions_file = %q
`, fallbackFile)
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if cfg.Defaults.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", cfg.Defaults.Iterations)
	}
	if cfg.Defaults.Mix != "cc:opus" {
		t.Fatalf("mix = %q, want 'cc:opus'", cfg.Defaults.Mix)
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return agent.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	var capturedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedModel = opts.Model
			capturedTaskPrompt = opts.TaskPrompt
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	mixSpecs := strings.Fields(cfg.Defaults.Mix)
	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     mixSpecs,
		TargetIterations:  cfg.Defaults.Iterations,
		Resolver:          resolver,
		FreeRunPromptFile: cfg.FreeRun.PromptFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "claude-opus-4-7" {
		t.Errorf("expected model 'claude-opus-4-7' from named resolution, got %q", capturedModel)
	}
	if capturedTaskPrompt != "Custom fallback instructions." {
		t.Errorf("expected fallback instructions as task prompt, got %q", capturedTaskPrompt)
	}

	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 2 {
		t.Fatalf("expected 2 completed iterations, got %+v", relays)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagSet(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := "--model"
	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return agent.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		if spec == "droid" {
			return agent.ResolvedAgent{Harness: "droid"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "--model") || !strings.Contains(content, "droid-v1") {
		t.Errorf("expected '--model droid-v1' in args, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_BareAliasNoModel(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := "--model"
	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid" {
			return agent.ResolvedAgent{Harness: "droid"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "--model") || strings.Contains(content, "droid-v1") {
		t.Errorf("expected no model in args for bare alias, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagEmpty(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := ""
	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return agent.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "droid-v1") {
		t.Errorf("expected positional model 'droid-v1' in args, got %q", content)
	}
	if strings.Contains(content, "--model") {
		t.Errorf("expected no '--model' flag, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagUnset_InfoNote(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: nil,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return agent.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "droid-v1") {
		t.Errorf("expected no model in args when model_flag unset, got %q", content)
	}
}

func TestE2E_BackwardsCompat_RootModelFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	configContent := `claude_model = "root-sonnet"
codex_model = "root-codex"
data_dir = "/tmp/data"
run_hooks_on_autocommit = true
`
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if len(cfg.DeprecationNotes) != 2 {
		t.Fatalf("expected 2 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}

	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("ResolveAgent cc: %v", err)
	}
	if resolved.Model != "root-sonnet" {
		t.Errorf("expected model 'root-sonnet' from root-level field, got %q", resolved.Model)
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return agent.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedModel = opts.Model
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
		AgentMixSpecs:    []string{"cc"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "root-sonnet" {
		t.Errorf("expected model 'root-sonnet' reaching executor, got %q", capturedModel)
	}
}

func TestE2E_DefaultsSection_NoDeprecation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	configContent := `schema_version = 2

[defaults]
claude_model = "defaults-opus"
iterations = 1
mix = "cc"
`
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if len(cfg.DeprecationNotes) != 0 {
		t.Fatalf("expected 0 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}

	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("ResolveAgent cc: %v", err)
	}
	if resolved.Model != "defaults-opus" {
		t.Errorf("expected model 'defaults-opus' from [defaults], got %q", resolved.Model)
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return agent.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedModel = opts.Model
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
		AgentMixSpecs:    strings.Fields(cfg.Defaults.Mix),
		TargetIterations: cfg.Defaults.Iterations,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "defaults-opus" {
		t.Errorf("expected model 'defaults-opus' reaching executor, got %q", capturedModel)
	}
}

// --- Phase 11: Verification ---

func TestE2E_CheapRotationOpencodeGLMToKimi(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var modelsExecuted []string
	exec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			modelsExecuted = append(modelsExecuted, opts.Model)
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("change-%s.txt", opts.Model)))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:glm:1", "op:kimi:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "rotate",
	}, map[string]agent.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := exec.rotateCalls; len(got) != 1 || got[0] != "kimi" {
		t.Fatalf("RotateModel calls = %v, want [kimi]", got)
	}
	if got, want := modelsExecuted, []string{"glm", "kimi"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}
}

func TestE2E_ClaudeRateLimitWaitAndResume(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var capturedSessionIDs []string
	var sleptDurations []time.Duration
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt == 1 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("sending to claude...\nerror 429 Too Many Requests\nretry-after: 1\n"), 0o644)
				}
				return &agent.TryResult{Completed: false, Summary: "rate limit hit", SessionID: "sess-rate"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
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
		RetryBudget:      3,
	}, executors)
	r.sleepFunc = func(d time.Duration) { sleptDurations = append(sleptDurations, d) }

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(capturedSessionIDs) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(capturedSessionIDs))
	}
	if capturedSessionIDs[0] != "" {
		t.Fatalf("attempt 1 ResumeSessionID = %q, want empty", capturedSessionIDs[0])
	}
	if capturedSessionIDs[1] != "sess-rate" {
		t.Fatalf("attempt 2 ResumeSessionID = %q, want sess-rate", capturedSessionIDs[1])
	}
	if len(sleptDurations) != 1 || sleptDurations[0] != 1*time.Second {
		t.Fatalf("slept durations = %v, want [1s]", sleptDurations)
	}
}

func TestE2E_SimulatedFreezeGracefulKillResumeRecovery(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	freezeCh := make(chan struct{})
	attempt := 0
	var resumeIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			resumeIDs = append(resumeIDs, opts.ResumeSessionID)
			if attempt == 1 {
				<-freezeCh
				return &agent.TryResult{Completed: false, Summary: "freeze", SessionID: "sess-freeze"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      3,
	}, map[string]agent.Executor{"claude": exec})

	controllerCount := 0
	r.stallControllerFactory = func(logPath string) reliability.StallController {
		_ = logPath
		controllerCount++
		if controllerCount == 1 {
			triggered := false
			return &fakeStallController{
				check: func(context.Context) (bool, error) {
					if triggered {
						return false, nil
					}
					triggered = true
					close(freezeCh)
					return true, nil
				},
			}
		}
		return &fakeStallController{}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got, want := resumeIDs, []string{"", "sess-freeze"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ResumeSessionIDs = %v, want %v", got, want)
	}
	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 1 {
		t.Fatalf("expected 1 completed iteration, got %+v", relays)
	}
}

func TestE2E_LivenessProbeClearsFreezeFlag(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	probeCalls := 0
	exec := &funcExecutor{
		probeSupported: true,
		probeFn: func(ctx context.Context) (bool, error) {
			probeCalls++
			return true, nil
		},
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			time.Sleep(50 * time.Millisecond)
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cx:1"},
		TargetIterations: 1,
		RetryBudget:      3,
		LivenessProbe:    true,
		StallThreshold:   50 * time.Millisecond,
	}, map[string]agent.Executor{"codex": exec})

	r.stallControllerFactory = func(string) reliability.StallController {
		probe := r.buildLivenessProbe(exec)
		return &fakeStallController{
			check: func(ctx context.Context) (bool, error) {
				if probe == nil {
					t.Fatal("expected liveness probe to be built for codex")
				}
				if probe.Check(ctx) {
					return false, nil
				}
				return true, nil
			},
		}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = 30 * time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if attempt != 1 {
		t.Fatalf("attempts = %d, want 1 after successful probe", attempt)
	}
	if probeCalls == 0 {
		t.Fatal("expected liveness probe to run")
	}
	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 1 {
		t.Fatalf("expected 1 completed iteration, got %+v", relays)
	}
}

func TestE2E_LivenessProbeFailureConfirmsFreeze(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	freezeCh := make(chan struct{})
	attempt := 0
	probeCalls := 0
	var resumeIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		probeSupported:  true,
		probeFn: func(ctx context.Context) (bool, error) {
			probeCalls++
			return false, nil
		},
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			resumeIDs = append(resumeIDs, opts.ResumeSessionID)
			if attempt == 1 {
				<-freezeCh
				return &agent.TryResult{Completed: false, Summary: "freeze", SessionID: "sess-probe"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cx:1"},
		TargetIterations: 1,
		RetryBudget:      3,
		LivenessProbe:    true,
	}, map[string]agent.Executor{"codex": exec})

	triggered := false
	r.stallControllerFactory = func(string) reliability.StallController {
		probe := r.buildLivenessProbe(exec)
		return &fakeStallController{
			check: func(ctx context.Context) (bool, error) {
				if triggered {
					return false, nil
				}
				if probe == nil {
					t.Fatal("expected liveness probe to be built for codex")
				}
				if probe.Check(ctx) {
					return false, nil
				}
				triggered = true
				close(freezeCh)
				return true, nil
			},
		}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if probeCalls == 0 {
		t.Fatal("expected liveness probe to run")
	}
	if got, want := resumeIDs, []string{"", "sess-probe"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ResumeSessionIDs = %v, want %v", got, want)
	}
}

func TestE2E_ErrorPatternStrategies(t *testing.T) {
	tests := []struct {
		name           string
		agentSpec      string
		harness        string
		logContent     string
		wantNoOp       bool
		wantWaitResume bool
		wantAttempts   int
		wantCompleted  bool
	}{
		{
			name:          "codex limit warning no-op",
			agentSpec:     "cx:1",
			harness:       "codex",
			logContent:    "warning: limit warning reached\ncompletion generated\n",
			wantNoOp:      true,
			wantAttempts:  1,
			wantCompleted: true,
		},
		{
			name:           "claude rate limit wait resume",
			agentSpec:      "cc:1",
			harness:        "claude",
			logContent:     "sending to claude...\nerror 429 Too Many Requests\nretry-after: 1\n",
			wantWaitResume: true,
			wantAttempts:   2,
			wantCompleted:  true,
		},
		{
			name:          "antigravity gemini-cli exit status 1 resumes retry",
			agentSpec:     "ag:1",
			harness:       "antigravity",
			logContent:    "running gemini-cli...\nprovider error\nexit status 1\n",
			wantAttempts:  2,
			wantCompleted: true,
		},
		{
			name:          "unknown failure fresh restart",
			agentSpec:     "cc:1",
			harness:       "claude",
			logContent:    "some unexpected error\nsegfault\n",
			wantAttempts:  2,
			wantCompleted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)

			s := newTestStore(t, rallyDir)
			attempt := 0
			exec := &funcExecutor{
				resumeSupported: true,
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					attempt++
					if attempt == 1 && opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte(tt.logContent), 0o644)
					}
					if attempt < tt.wantAttempts {
						return &agent.TryResult{Completed: false, Summary: "fail", SessionID: "sess-1"}, nil
					}
					f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
					f.WriteString("changed")
					f.Close()
					return &agent.TryResult{Completed: true, Summary: "success"}, nil
				},
			}
			executors := map[string]agent.Executor{tt.harness: exec}

			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          t.TempDir(),
				AgentMixSpecs:    []string{tt.agentSpec},
				TargetIterations: 1,
				RetryBudget:      3,
				Resolver:         testResolver,
			}, executors)
			r.sleepFunc = func(time.Duration) {}

			err := r.Run(context.Background())

			if err != nil {
				t.Fatalf("run failed: %v", err)
			}

			tries := s.AllTries()
			if len(tries) != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d", len(tries), tt.wantAttempts)
			}
			if tries[len(tries)-1].Completed != tt.wantCompleted {
				t.Fatalf("final try Completed = %v, want %v", tries[len(tries)-1].Completed, tt.wantCompleted)
			}
		})
	}
}

func TestE2E_ErrorPatternRotateAdvancesRoute(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var executed []string
	opencodeExec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executed = append(executed, "opencode:"+opts.Model)
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("some output\nerror: API bad request from provider\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "rotate"}, nil
		},
	}
	codexExec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executed = append(executed, "codex:"+opts.Model)
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:glm:1", "cx:gpt-5:1"},
		},
		TargetIterations: 1,
		Resolver:         testResolver,
		TaskPrompt:       "rotate",
	}, map[string]agent.Executor{
		"opencode": opencodeExec,
		"codex":    codexExec,
	})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got, want := executed, []string{"opencode:glm", "codex:gpt-5"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed route = %v, want %v", got, want)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("tries = %d, want 2", len(tries))
	}
	if tries[0].AttemptNumber != 1 || tries[1].AttemptNumber != 1 {
		t.Fatalf("attempt numbers = [%d %d], want [1 1] after route advance", tries[0].AttemptNumber, tries[1].AttemptNumber)
	}
}

func TestE2E_RunStateClearedAtRelayStart(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	if err := progress.SaveRunState(workspaceDir, &progress.RunState{RunID: "old-run", HandoffState: 1, RecordedLaps: []string{"lap-old"}}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
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

	rs, err := progress.LoadRunState(workspaceDir)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if rs.RunID != "" {
		t.Fatalf("expected run-state cleared at relay start, got RunID=%q", rs.RunID)
	}
	if rs.HandoffState != 0 {
		t.Fatalf("expected HandoffState=0 after relay start, got %d", rs.HandoffState)
	}
	if len(rs.RecordedLaps) != 0 {
		t.Fatalf("expected RecordedLaps empty after relay start, got %v", rs.RecordedLaps)
	}
}

func TestE2E_WindowsFreezeDisabledRetryBudgetExhaustion(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      2,
		Resolver:         cheapTestResolver,
	}, executors)
	// Simulate Windows path: stall controller disabled
	r.stallControllerFactory = func(string) reliability.StallController { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx)
	}()

	deadline := time.After(2 * time.Second)
	for {
		tries := s.AllTries()
		status, err := s.GetAgentStatus("opencode", cheapTestModel)
		if err != nil {
			t.Fatal(err)
		}
		foundPause := false
		for _, e := range status {
			if e.EventType == "paused" {
				foundPause = true
				break
			}
		}
		if len(tries) == 2 && foundPause {
			cancel()
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for retry-budget exhaustion without freeze detection")
		case <-time.After(10 * time.Millisecond):
		}
	}

	err := <-done
	if err == nil || err != context.Canceled {
		t.Fatalf("Run() error = %v, want context.Canceled after stopping paused-agent wait", err)
	}

	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("expected 2 tries (retry budget exhausted), got %d", len(tries))
	}
	status, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
	foundPause := false
	for _, e := range status {
		if e.EventType == "paused" {
			foundPause = true
			break
		}
	}
	if !foundPause {
		t.Fatal("expected agent paused after retry budget exhaustion")
	}
}

// TestProbationIncompletePromotesToActive is an end-to-end test exercising the
// full Run loop: an agent starts in probation (frozen > FreezeDuration ago),
// produces an incomplete result (laps-backed run with file changes but no
// finalization), and should be promoted back to active — not re-frozen.
//
// This catches the bug where the probation branch in Run() compared
// failReason == "incomplete run", but runOne's ClassifyError had already
// overwritten failReason to the classified string before returning. The fix
// checks failureClass == reliability.FailureIncomplete instead.
func TestProbationIncompletePromotesToActive(t *testing.T) {
	oldHeadPull := headPullLap
	pullCount := 0
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		pullCount++
		if pullCount == 1 {
			return laps.Lap{ID: "lap-1", Title: "probation test", Assignee: "senior"}, nil
		}
		return laps.NoLap, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)

	// The executor writes a unique file per attempt (dirty tree) but never
	// calls laps done → incomplete. Each attempt produces its own new dirty
	// path ensures the leftover-aware delta still classifies it incomplete.
	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			_ = os.WriteFile(filepath.Join(workspaceDir, fmt.Sprintf("partial-%d.txt", attempt)), []byte("partial"), 0o644)
			return &agent.TryResult{Completed: true, Summary: "made progress but did not finalize"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

	// Set up the agent as frozen long enough ago that it decays to probation.
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	frozenAt := baseTime // frozen 6 hours before "now"
	nowTime := baseTime.Add(6 * time.Hour)

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, executors)
	r.sleepFunc = func(time.Duration) {}
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		FreezeDuration:            5 * time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return nowTime },
	}

	// Seed a frozen event old enough to trigger probation.
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "opencode",
		Model:     cheapTestModel,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	// Confirm setup: agent should be in probation before the run.
	key := ResilienceKey{Harness: "opencode", Model: cheapTestModel}
	st, _ := r.resilience.GetState(key)
	if st != StateProbation {
		t.Fatalf("setup: expected probation, got %s", st)
	}

	_ = r.Run(context.Background())

	// After the incomplete probation run, agent should be active (promoted),
	// not re-frozen.
	st, _ = r.resilience.GetState(key)
	if st != StateActive {
		t.Fatalf("expected active after probation incomplete, got %s", st)
	}

	// Verify the try was recorded as incomplete (not completed).
	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	lastTry := tries[len(tries)-1]
	if lastTry.Completed {
		t.Fatal("incomplete try should be failed")
	}

	// Verify that no frozen event was written after the probation run
	// (an "active"/"unfrozen" event should appear instead of another "frozen").
	events, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
	lastEventType := ""
	for _, e := range events {
		lastEventType = e.EventType
	}
	if lastEventType == "frozen" {
		t.Fatalf("last event should NOT be frozen after incomplete probation; got events: %v", events)
	}
}
