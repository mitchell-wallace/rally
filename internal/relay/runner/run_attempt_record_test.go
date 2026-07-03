package runner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestLapAttemptRecordedInTryRecord(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			progress.RecordLap(workspaceDir, "lap-1")
			os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("done"), 0o644)
			runGit(t, workspaceDir, "add", "work.txt")
			runGit(t, workspaceDir, "commit", "-m", "completed work", "--no-verify")
			return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
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
	}, map[string]harnessapi.Executor{"opencode": exec})

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "pinned task", Prompt: "do work", Assignee: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		io.Discard,
	)
	success := res.Success
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !success {
		t.Fatal("expected success")
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	attempts := tries[0].LapsAttempted
	if len(attempts) == 0 {
		t.Fatal("expected laps_attempted to be recorded on TryRecord")
	}
	found := false
	for _, a := range attempts {
		if a.LapID == "lap-1" {
			found = true
			if a.Timestamp == "" {
				t.Fatal("expected non-empty timestamp in lap attempt")
			}
			break
		}
	}
	if !found {
		t.Fatalf("laps_attempted = %v, want lap-1 present", attempts)
	}
}
