package relay

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/testutil"
)

func TestRunnerUsesRealLapsHeadTask(t *testing.T) {
	workspaceDir := testutil.SetupLapsFixtureProject(t)
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("mkdir .rally: %v", err)
	}

	testutil.RunCommand(t, workspaceDir, "laps", "add", "head",
		"--title", "Implement auth",
		"--description", "Add login and session handling.",
		"--assignee", "alice",
	)

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
		LapsEnabled:      true,
		TaskPrompt:       "fallback prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskName != "Implement auth" {
		t.Errorf("task name = %q, want %q", receivedTaskName, "Implement auth")
	}
	if receivedTaskPrompt != "Add login and session handling." {
		t.Errorf("task prompt = %q, want %q", receivedTaskPrompt, "Add login and session handling.")
	}
	if receivedRequirements != "Assignee: alice" {
		t.Errorf("task requirements = %q, want %q", receivedRequirements, "Assignee: alice")
	}
}
