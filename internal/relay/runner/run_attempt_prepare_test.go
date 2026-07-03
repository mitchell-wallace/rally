package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestIncompleteRetryPromptGuidance(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Description: "finish lap", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var retryPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644); err != nil {
					return nil, err
				}
				return &harnessapi.TryResult{Completed: true, Summary: "partial"}, nil
			}
			retryPrompt = opts.TaskPrompt
			if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      2,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if !strings.Contains(retryPrompt, incompleteRetryGuidance) {
		t.Fatalf("retry prompt missing incomplete guidance: %q", retryPrompt)
	}
}

func TestIncompleteRetryCarriesFinalizationGuidance(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Description: "finish lap", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var retryPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				_ = os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644)
				return &harnessapi.TryResult{Completed: true, Summary: "partial"}, nil
			}
			retryPrompt = opts.TaskPrompt
			if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
				return nil, err
			}
			_ = os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done"), 0o644)
			return &harnessapi.TryResult{Completed: true, Summary: "done"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      3,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	if !strings.Contains(retryPrompt, incompleteRetryGuidance) {
		t.Fatalf("retry prompt for incomplete_finalization missing guidance: %q", retryPrompt)
	}

	tries := s.AllTries()
	if len(tries) < 2 {
		t.Fatalf("expected at least 2 tries, got %d", len(tries))
	}
	first := tries[0]
	if first.Outcome != reliability.OutcomeIncomplete {
		t.Fatalf("first try Outcome = %q, want %q", first.Outcome, reliability.OutcomeIncomplete)
	}
	if first.Category != "" {
		t.Fatalf("first try Category = %q, want empty because incomplete is a lifecycle outcome", first.Category)
	}
	if !strings.Contains(first.FailReason, "incomplete") {
		t.Fatalf("first try FailReason = %q, want display containing 'incomplete'", first.FailReason)
	}
	if first.FailReason == string(reliability.CategoryIncompleteFinalization) {
		t.Fatalf("first try FailReason should be a display string, not the raw category %q", first.FailReason)
	}
}

func TestLeftoverWorkGuidance_DirtyTree(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Description: "test task", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Create an initial commit so the repo is not empty.
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial")

	// Create a dirty file outside .rally/ (simulating leftover work).
	os.WriteFile(filepath.Join(workspaceDir, "leftover.go"), []byte("package leftover\n"), 0o644)

	s := newTestStore(t, rallyDir)
	var capturedPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			capturedPrompt = harnessapi.BuildPrompt(opts)
			if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
				return nil, err
			}
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

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if !strings.Contains(capturedPrompt, "## Leftover Changes") {
		t.Fatalf("expected leftover-work guidance in prompt when tree is dirty, got:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "uncommitted changes left over") {
		t.Fatalf("expected leftover-work body text in prompt, got:\n%s", capturedPrompt)
	}
}

func TestLeftoverWorkGuidance_CleanTree(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Description: "test task", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Create an initial commit so the repo is not empty, tree is clean.
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial")

	s := newTestStore(t, rallyDir)
	var capturedPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			capturedPrompt = harnessapi.BuildPrompt(opts)
			// Produce a real user-file change so the run is not flagged as
			// "no changes made". The leftover-work check already ran at run
			// start (captured in opts), so this write does not affect it.
			if err := os.WriteFile(filepath.Join(workspaceDir, "result.go"), []byte("package result\n"), 0o644); err != nil {
				return nil, err
			}
			if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
				return nil, err
			}
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

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if strings.Contains(capturedPrompt, "## Leftover Changes") {
		t.Fatalf("expected NO leftover-work guidance for clean tree, got:\n%s", capturedPrompt)
	}
}

func TestLeftoverWorkGuidance_OnlyRallyDirty(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{ID: "lap-1", Title: "test", Description: "test task", Assignee: "senior"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Create an initial commit so the repo is not empty.
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial")

	// Only dirty rally-owned state — these should be excluded by
	// IsWorkspaceDirty. .laps/ is created and churned by the runner itself.
	os.WriteFile(filepath.Join(rallyDir, "summary.jsonl"), []byte("{}\n"), 0o644)

	s := newTestStore(t, rallyDir)
	var capturedPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			capturedPrompt = harnessapi.BuildPrompt(opts)
			// Produce a real user-file change so the run is not flagged as
			// "no changes made". The leftover-work check already ran at run
			// start (captured in opts), so this write does not affect it.
			if err := os.WriteFile(filepath.Join(workspaceDir, "result.go"), []byte("package result\n"), 0o644); err != nil {
				return nil, err
			}
			if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
				return nil, err
			}
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

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if strings.Contains(capturedPrompt, "## Leftover Changes") {
		t.Fatalf("expected NO leftover-work guidance when only .rally/ is dirty, got:\n%s", capturedPrompt)
	}
}
