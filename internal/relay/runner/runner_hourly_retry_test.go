package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestHourlyRetryBudgetHonored(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	callCount := 0
	execExecutor := &funcExecutor{
		fn: func(ctxRun context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			callCount++
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("argument list too long\n"), 0o644)
			}
			if callCount >= 3 {
				cancel() // exit the relay loop
			}
			return &agent.TryResult{Completed: false, Summary: "infra fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": execExecutor}

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         cheapTestResolver,
	}, executors)
	r.sleepFunc = func(time.Duration) {}
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   func() time.Time { return baseTime.Add(2 * time.Hour) },
	}

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "opencode",
		Model:     cheapTestModel,
		EventType: "paused",
		Timestamp: baseTime.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	_ = r.Run(ctx)

	if callCount != 3 {
		t.Fatalf("expected 3 attempts for hourly retry, got %d", callCount)
	}
}

func TestHourlyRetryTransientFailureDoesNotBurnFreezeLife(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)

	callCount := 0
	execExecutor := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			callCount++
			if callCount < 3 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("argument list too long\n"), 0o644)
				}
				return &agent.TryResult{Completed: false, Summary: "infra fail"}, nil
			}
			_ = os.WriteFile(filepath.Join(workspaceDir, "foo.txt"), []byte("bar"), 0o644)
			c := exec.Command("git", "add", "foo.txt")
			c.Dir = workspaceDir
			_ = c.Run()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": execExecutor}

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         cheapTestResolver,
	}, executors)
	r.sleepFunc = func(time.Duration) {}
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   func() time.Time { return baseTime.Add(2 * time.Hour) },
	}

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "opencode",
		Model:     cheapTestModel,
		EventType: "paused",
		Timestamp: baseTime.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	_ = r.Run(context.Background())

	if callCount != 3 {
		t.Fatalf("expected 3 attempts, got %d", callCount)
	}

	st, _ := r.resilience.GetState(ResilienceKey{Harness: "opencode", Model: cheapTestModel})
	if st != StateActive {
		t.Fatalf("expected agent active after hourly retry success, got %v", st)
	}

	for _, e := range s.AllAgentStatus() {
		if e.EventType == "retry_failed" {
			t.Fatal("expected no retry_failed event when hourly retry succeeds within budget")
		}
	}
}
