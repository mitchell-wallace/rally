package relay

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRunOneRecordsHandoffRequestedOutcome(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
				RunID:   "relay-1-run-1",
				Summary: "blocked cleanly",
				Handoff: &progress.HandoffEntry{
					Summary:       "blocked cleanly",
					Followups:     []string{"follow up"},
					CreatedLapIDs: []string{"lap-followup"},
				},
			}); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "executor summary"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]agent.Executor{"opencode": exec})

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !res.Success {
		t.Fatal("runOne success = false, want true")
	}
	if res.Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffRequested)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0", res.InfraFailures)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("try outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeHandoffRequested)
	}
	if tries[0].Category != "" {
		t.Fatalf("try category = %q, want empty for handoff_requested", tries[0].Category)
	}
}

func TestTryOutcomeForAttemptLifecycleBoundaries(t *testing.T) {
	tests := []struct {
		name              string
		failed            bool
		incomplete        bool
		interrupted       bool
		hasDurableHandoff bool
		want              reliability.TryOutcome
	}{
		{name: "completed", want: reliability.OutcomeCompleted},
		{name: "handoff_requested", hasDurableHandoff: true, want: reliability.OutcomeHandoffRequested},
		{name: "incomplete", failed: true, incomplete: true, want: reliability.OutcomeIncomplete},
		{name: "failed", failed: true, want: reliability.OutcomeFailed},
		{name: "interrupted", failed: true, interrupted: true, want: reliability.OutcomeInterrupted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tryOutcomeForAttempt(tt.failed, tt.incomplete, tt.interrupted, tt.hasDurableHandoff)
			if got != tt.want {
				t.Fatalf("tryOutcomeForAttempt() = %q, want %q", got, tt.want)
			}
		})
	}
}
