package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestIncompleteRunLeavesChangesUncommitted(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "changed but did not finalize"}, nil
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

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].Completed {
		t.Fatal("incomplete try should be failed")
	}
	if tries[0].FailReason != "incomplete: file changes without finalization" {
		t.Fatalf("FailReason = %q, want incomplete classification", tries[0].FailReason)
	}
	if tries[0].CommitHash != "" {
		t.Fatalf("CommitHash = %q, want empty for incomplete try", tries[0].CommitHash)
	}
	status := runGit(t, workspaceDir, "status", "--porcelain", "partial.txt")
	if !strings.Contains(status, "partial.txt") {
		t.Fatalf("partial.txt should remain uncommitted, status=%q", status)
	}
}

func TestIncompleteDoesNotCountTowardFailureCascade(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			_ = os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644)
			return &harnessapi.TryResult{Completed: true, Summary: "partial"}, nil
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

	_ = r.Run(context.Background())

	if got := countAgentStatusEvents(s, "paused", "frozen"); got != 0 {
		t.Fatalf("cascade events = %d, want 0", got)
	}
}

// TestIncompleteLeftoverAware_NoOpInheritingLeftovers verifies that a no-op try
// inheriting uncommitted leftovers from a prior failed try is NOT classified as
// incomplete. The try produced no changes of its own, so the incomplete class
// should not apply.
func TestIncompleteLeftoverAware_NoOpInheritingLeftovers(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial")

	if err := os.WriteFile(filepath.Join(workspaceDir, "leftover.txt"), []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "no-op"}, nil
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

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].FailReason == "incomplete: file changes without finalization" {
		t.Fatalf("no-op try inheriting leftovers should NOT be incomplete, got FailReason=%q", tries[0].FailReason)
	}
}

// TestIncompleteLeftoverAware_OwnUnfinalizedChanges verifies that a try that
// adds its own unfinalized changes IS classified as incomplete.
func TestIncompleteLeftoverAware_OwnUnfinalizedChanges(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "new.txt"), []byte("new"), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "changed but did not finalize"}, nil
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

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].FailReason != "incomplete: file changes without finalization" {
		t.Fatalf("FailReason = %q, want incomplete classification", tries[0].FailReason)
	}
}

// TestIncompleteLeftoverAware_TouchingInheritedLeftover verifies that a try
// that stages an inherited leftover path has that change attributed to it,
// making the try incomplete if not finalized.
func TestIncompleteLeftoverAware_TouchingInheritedLeftover(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial")

	if err := os.WriteFile(filepath.Join(workspaceDir, "leftover.txt"), []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			cmd := exec.Command("git", "-C", workspaceDir, "add", "leftover.txt")
			if err := cmd.Run(); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "staged leftover but did not finalize"}, nil
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

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].FailReason != "incomplete: file changes without finalization" {
		t.Fatalf("FailReason = %q, want incomplete (staging inherited leftover attributes it)", tries[0].FailReason)
	}
}

// TestIncompleteLeftoverAware_NoChangeNoFinalize verifies that a try with no
// changes and no finalization is a normal agent-class failure, not incomplete.
func TestIncompleteLeftoverAware_NoChangeNoFinalize(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "did nothing"}, nil
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

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].FailReason == "incomplete: file changes without finalization" {
		t.Fatalf("no-change try should NOT be incomplete, got FailReason=%q", tries[0].FailReason)
	}
	if tries[0].Completed {
		t.Fatal("no-change try should be failed")
	}
}
