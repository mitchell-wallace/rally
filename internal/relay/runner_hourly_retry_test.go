package relay

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
	rallyDir := filepath.Join(workspaceDir, ".rally")
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
	rallyDir := filepath.Join(workspaceDir, ".rally")
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

func TestResetAgentStatus_ClearsAllStates(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	resilience := NewResilience(s)

	frozenKey := ResilienceKey{Harness: "claude", Model: "opus"}
	pausedKey := ResilienceKey{Harness: "opencode", Model: cheapTestModel}

	if err := resilience.FreezeAgent(frozenKey, 1); err != nil {
		t.Fatalf("FreezeAgent: %v", err)
	}
	if err := resilience.PauseAgent(pausedKey, 1); err != nil {
		t.Fatalf("PauseAgent: %v", err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "codex",
		Model:     "gpt-5",
		EventType: "frozen",
		Timestamp: now.Add(-6 * time.Hour).UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	st, _ := resilience.GetState(frozenKey)
	if st != StateFrozen {
		t.Fatalf("expected frozen, got %s", st)
	}
	st, _ = resilience.GetState(pausedKey)
	if st != StatePaused {
		t.Fatalf("expected paused, got %s", st)
	}
	probationRes := &Resilience{
		Store:          s,
		PauseDuration:  time.Hour,
		FreezeDuration: 5 * time.Hour,
		NowFunc:        func() time.Time { return now },
	}
	st, _ = probationRes.GetState(ResilienceKey{Harness: "codex", Model: "gpt-5"})
	if st != StateProbation {
		t.Fatalf("expected probation, got %s", st)
	}

	if err := s.ResetAgentStatus(); err != nil {
		t.Fatalf("ResetAgentStatus: %v", err)
	}

	st, _ = resilience.GetState(frozenKey)
	if st != StateActive {
		t.Fatalf("expected active after reset for frozen agent, got %s", st)
	}
	st, _ = resilience.GetState(pausedKey)
	if st != StateActive {
		t.Fatalf("expected active after reset for paused agent, got %s", st)
	}
	st, _ = resilience.GetState(ResilienceKey{Harness: "codex", Model: "gpt-5"})
	if st != StateActive {
		t.Fatalf("expected active after reset for probation agent, got %s", st)
	}

	if len(s.AllAgentStatus()) != 0 {
		t.Fatalf("expected 0 agent status events after reset, got %d", len(s.AllAgentStatus()))
	}
}

func TestPerHarnessModelCascadeIsolation(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	resilience := NewResilience(s)
	resilience.HourlyRetriesBeforeFreeze = 3

	busyKey := ResilienceKey{Harness: "opencode", Model: "busy-model"}
	idleKey := ResilienceKey{Harness: "opencode", Model: "idle-model"}

	for i := 0; i < 3; i++ {
		if err := resilience.RecordHourlyFailure(busyKey, 1); err != nil {
			t.Fatalf("RecordHourlyFailure busy: %v", err)
		}
	}

	st, _ := resilience.GetState(busyKey)
	if st != StateFrozen {
		t.Fatalf("expected busy model frozen, got %s", st)
	}

	st, _ = resilience.GetState(idleKey)
	if st != StateActive {
		t.Fatalf("expected idle model still active, got %s", st)
	}

	mix, err := ParseAgentMix([]string{"op:busy-model", "op:idle-model"}, Resolver(cheapTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix: %v", err)
	}
	picked, _, _, err := resilience.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("SelectActiveAgent: %v", err)
	}
	if picked.Model != "idle-model" {
		t.Fatalf("expected idle-model selected (busy is frozen), got %s", picked.Model)
	}
}
