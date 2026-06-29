package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRunWritesActiveTryMetadataBeforeExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var activeAtExecutor progress.RunState
	var executorErr error
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			rs, err := progress.LoadRunState(workspaceDir)
			if err != nil {
				executorErr = fmt.Errorf("load run-state in executor: %w", err)
				return nil, executorErr
			}
			activeAtExecutor = *rs
			if opts.LogPath != "" {
				if err := os.WriteFile(opts.LogPath, []byte("executor log\n"), 0o644); err != nil {
					executorErr = fmt.Errorf("write try log: %w", err)
					return nil, executorErr
				}
			}
			if err := os.WriteFile(filepath.Join(workspaceDir, "active-try.txt"), []byte("changed"), 0o644); err != nil {
				executorErr = fmt.Errorf("write workspace file: %w", err)
				return nil, executorErr
			}
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, map[string]agent.Executor{"claude": exec})
	r.out = io.Discard

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if executorErr != nil {
		t.Fatalf("executor setup failed: %v", executorErr)
	}

	if activeAtExecutor.RunID != "relay-1-run-1" {
		t.Fatalf("RunID visible to executor = %q, want relay-1-run-1", activeAtExecutor.RunID)
	}
	if activeAtExecutor.ActiveRelayID != 1 {
		t.Fatalf("ActiveRelayID = %d, want 1", activeAtExecutor.ActiveRelayID)
	}
	if activeAtExecutor.ActiveRunID != 1 {
		t.Fatalf("ActiveRunID = %d, want 1", activeAtExecutor.ActiveRunID)
	}
	if activeAtExecutor.ActiveTryID != 1 {
		t.Fatalf("ActiveTryID = %d, want 1", activeAtExecutor.ActiveTryID)
	}
	if activeAtExecutor.ActiveLogPath == "" {
		t.Fatal("ActiveLogPath was empty")
	}
	if _, err := time.Parse(time.RFC3339, activeAtExecutor.ActiveStartedAt); err != nil {
		t.Fatalf("ActiveStartedAt = %q, want RFC3339: %v", activeAtExecutor.ActiveStartedAt, err)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if tries[0].ID != activeAtExecutor.ActiveTryID {
		t.Fatalf("try ID = %d, active try ID = %d", tries[0].ID, activeAtExecutor.ActiveTryID)
	}
	if tries[0].LogPath != activeAtExecutor.ActiveLogPath {
		t.Fatalf("try LogPath = %q, active LogPath = %q", tries[0].LogPath, activeAtExecutor.ActiveLogPath)
	}
	rs, err := progress.LoadRunState(workspaceDir)
	if err != nil {
		t.Fatalf("LoadRunState after run: %v", err)
	}
	if rs.ActiveRelayID != 0 || rs.ActiveRunID != 0 || rs.ActiveTryID != 0 || rs.ActiveLogPath != "" || rs.ActiveStartedAt != "" {
		t.Fatalf("active metadata left after run: %+v", rs)
	}
}
