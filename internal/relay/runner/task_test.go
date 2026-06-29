package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestInstructionsPassedToExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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

func TestLapsHeadTaskPassedToExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedTaskName string
	var receivedRequirements string
	var receivedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskName = opts.TaskName
			receivedRequirements = opts.TaskRequirements
			receivedTaskPrompt = opts.TaskPrompt
			if err := progress.RecordLap(workspaceDir, "lap-42"); err != nil {
				return nil, err
			}
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{
			ID:          "lap-42",
			Title:       "Implement auth",
			Description: "Add login and session handling.",
			Assignee:    "alice",
		}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		TaskPrompt:       "fallback prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskName != "Implement auth" {
		t.Errorf("expected task name from lap, got %q", receivedTaskName)
	}
	if receivedTaskPrompt != "Add login and session handling." {
		t.Errorf("expected task prompt from lap, got %q", receivedTaskPrompt)
	}
	if receivedRequirements != "Lap ID: lap-42\nAssignee: alice" {
		t.Errorf("expected lap requirements, got %q", receivedRequirements)
	}
}

func TestLapsInstructionsFileUsed(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	instructionsFile := filepath.Join(workspaceDir, "custom_laps.md")
	os.WriteFile(instructionsFile, []byte("Custom laps instructions."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:         workspaceDir,
		DataDir:              t.TempDir(),
		AgentMixSpecs:        []string{"cc:1"},
		TargetIterations:     1,
		LapsEnabled:          true,
		Instructions:         "Default instructions.",
		LapsInstructionsFile: instructionsFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Custom laps instructions." {
		t.Errorf("expected laps instructions, got %q", receivedInstructions)
	}
}

func TestLapsInstructionsFileFallsBackToDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:         workspaceDir,
		DataDir:              t.TempDir(),
		AgentMixSpecs:        []string{"cc:1"},
		TargetIterations:     1,
		LapsEnabled:          true,
		Instructions:         "Default instructions.",
		LapsInstructionsFile: filepath.Join(workspaceDir, "nonexistent.md"),
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Default instructions." {
		t.Errorf("expected default instructions when laps file missing, got %q", receivedInstructions)
	}
}

func TestLapsInstructionsNotUsedInNoBackendMode(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	instructionsFile := filepath.Join(workspaceDir, "custom_laps.md")
	os.WriteFile(instructionsFile, []byte("Custom laps instructions."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:         workspaceDir,
		DataDir:              t.TempDir(),
		AgentMixSpecs:        []string{"cc:1"},
		TargetIterations:     1,
		LapsEnabled:          false,
		Instructions:         "Default instructions.",
		TaskPrompt:           "Do some work.",
		LapsInstructionsFile: instructionsFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Default instructions." {
		t.Errorf("expected default instructions in no-backend mode, got %q", receivedInstructions)
	}
}

func TestLapsInstructionsUnconfiguredUsesDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		Instructions:     "Default instructions.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Default instructions." {
		t.Errorf("expected default instructions when no laps file configured, got %q", receivedInstructions)
	}
}

func TestRoleInstructionsLoadedForAssignee(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	agentsDir := filepath.Join(rallyDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	if err := os.WriteFile(filepath.Join(agentsDir, "alice.md"), []byte("Role-specific guidance."), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)
	var receivedRoleInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedRoleInstructions = opts.RoleInstructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work", Assignee: "ALICE"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		Instructions:     "Default instructions.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedRoleInstructions != "Role-specific guidance." {
		t.Fatalf("role instructions = %q, want %q", receivedRoleInstructions, "Role-specific guidance.")
	}
}

func TestRoleInstructionsMissingFileIsSilent(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedRoleInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedRoleInstructions = opts.RoleInstructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work", Assignee: "missing"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		Instructions:     "Default instructions.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedRoleInstructions != "" {
		t.Fatalf("role instructions = %q, want empty string", receivedRoleInstructions)
	}
}

func TestRoleInstructionsSkippedInNoBackendMode(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	agentsDir := filepath.Join(rallyDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	if err := os.WriteFile(filepath.Join(agentsDir, "alice.md"), []byte("Role-specific guidance."), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)
	var receivedRoleInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedRoleInstructions = opts.RoleInstructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
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
		LapsEnabled:      false,
		Instructions:     "Default instructions.",
		TaskPrompt:       "Do some work.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedRoleInstructions != "" {
		t.Fatalf("role instructions = %q, want empty string", receivedRoleInstructions)
	}
}

func TestFallbackInstructionsUsedInNoBackendMode(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       false,
		FreeRunPromptFile: fallbackFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != "Fallback prompt content." {
		t.Errorf("expected fallback file content as task prompt, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsIgnoredWhenCLIPromptProvided(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       false,
		TaskPrompt:        "CLI prompt",
		FreeRunPromptFile: fallbackFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != "CLI prompt" {
		t.Errorf("expected CLI prompt to take precedence over fallback, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsIgnoredInLapsMode(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "configured prompt"}, nil }
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       true,
		TaskPrompt:        "configured prompt",
		FreeRunPromptFile: fallbackFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != "configured prompt" {
		t.Errorf("expected configured prompt, fallback should be ignored in laps mode, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsMissingFileUsesBuiltInDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       false,
		FreeRunPromptFile: filepath.Join(workspaceDir, "nonexistent.md"),
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != builtInDefaultFreeRunPrompt {
		t.Errorf("expected built-in default fallback, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsUnconfiguredUsesBuiltInDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
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
		LapsEnabled:      false,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != builtInDefaultFreeRunPrompt {
		t.Errorf("expected built-in default fallback, got %q", receivedTaskPrompt)
	}
}

func TestBuildRecentContext_PerSummaryTruncation(t *testing.T) {
	longSummary := strings.Repeat("abcdefghij", 20)

	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: true, Summary: longSummary},
		{RunID: 2, AgentType: "codex", Completed: false, Summary: longSummary},
	}

	result := buildRecentContext(tries, 50, 0)
	for _, tr := range tries {
		if !strings.Contains(result, fmt.Sprintf("Run %d (%s)", tr.RunID, tr.AgentType)) {
			t.Errorf("expected mention of run %d", tr.RunID)
		}
	}
	if !strings.Contains(result, "... [truncated] ...") {
		t.Errorf("expected per-summary truncation marker in result: %q", result)
	}
}

func TestBuildRecentContext_OverallTruncation(t *testing.T) {
	mediumSummary := strings.Repeat("x", 200)
	tries := []store.TryRecord{}
	for i := 1; i <= 20; i++ {
		tries = append(tries, store.TryRecord{
			RunID: i, AgentType: "claude", Completed: true, Summary: mediumSummary,
		})
	}

	result := buildRecentContext(tries, 0, 500)
	if !strings.Contains(result, "... [truncated] ...") {
		t.Errorf("expected overall truncation marker in result: %q", result)
	}
}

func TestPromptBudget_PerSummaryTruncation(t *testing.T) {
	longSummary := strings.Repeat("x", 500)
	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: false, Summary: longSummary},
	}
	result := buildRecentContext(tries, 100, 0)
	if !strings.Contains(result, "... [truncated] ...") {
		t.Fatal("expected truncation marker in output")
	}
	if len(result) >= 500 {
		t.Fatalf("expected output shorter than full summary, got %d chars", len(result))
	}
	headSize := 100 / 2
	tailSize := 100 - headSize
	if !strings.Contains(result, longSummary[:headSize]) {
		t.Fatal("expected head of summary preserved")
	}
	if !strings.Contains(result, longSummary[len(longSummary)-tailSize:]) {
		t.Fatal("expected tail of summary preserved")
	}
}

func TestPromptBudget_ShortSummariesPassThrough(t *testing.T) {
	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: true, Summary: "short"},
		{RunID: 2, AgentType: "opencode", Completed: false, Summary: "also short"},
	}
	result := buildRecentContext(tries, 1000, 0)
	if strings.Contains(result, "... [truncated] ...") {
		t.Fatal("short summaries should not be truncated")
	}
	if !strings.Contains(result, "summary=short") {
		t.Fatal("expected first summary present")
	}
	if !strings.Contains(result, "summary=also short") {
		t.Fatal("expected second summary present")
	}
}

func TestBuildRecentContextCancelledUsesOutcome(t *testing.T) {
	tries := []store.TryRecord{
		{
			RunID:              1,
			AgentType:          "codex",
			Completed:          false,
			Outcome:            reliability.OutcomeCancelled,
			CancellationSource: "graceful_stop",
			Summary:            "operator stopped the run",
		},
	}

	result := buildRecentContext(tries, 1000, 0)
	if !strings.Contains(result, "outcome=cancelled source=graceful_stop") {
		t.Fatalf("expected cancelled outcome/source in recent context, got: %q", result)
	}
	if strings.Contains(result, "completed=false") {
		t.Fatalf("cancelled recent context should not look like a generic failed try, got: %q", result)
	}
}

func TestPromptBudget_CountHonored(t *testing.T) {
	var tries []store.TryRecord
	for i := 1; i <= 3; i++ {
		tries = append(tries, store.TryRecord{RunID: i, AgentType: "claude", Completed: true, Summary: fmt.Sprintf("try %d", i)})
	}
	result := buildRecentContext(tries, 0, 0)
	for i := 1; i <= 3; i++ {
		if !strings.Contains(result, fmt.Sprintf("try %d", i)) {
			t.Fatalf("expected try %d in output", i)
		}
	}
}

func TestPromptBudget_OverallLimit(t *testing.T) {
	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: false, Summary: strings.Repeat("a", 200)},
		{RunID: 2, AgentType: "claude", Completed: false, Summary: strings.Repeat("b", 200)},
	}
	result := buildRecentContext(tries, 0, 200)
	if !strings.Contains(result, "... [truncated] ...") {
		t.Fatal("expected overall truncation marker")
	}
	if len(result) > 250 {
		t.Fatalf("expected output near 200 chars, got %d", len(result))
	}
	headSize := 200 / 2
	if !strings.HasPrefix(result[:headSize], "Run 1") {
		t.Fatal("expected head of overall context to start with first try")
	}
	if !strings.Contains(result[len(result)-headSize:], strings.Repeat("b", 10)) {
		t.Fatal("expected tail of overall context to contain second summary")
	}
}
