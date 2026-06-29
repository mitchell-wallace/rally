package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/telemetry"
)

// A run that never finalizes writes a stub summary.jsonl after the run loop.
// When the run produced no commit to fold it into, the end-of-relay failover
// must commit that leftover and record a RallyDiagnostic so it is visible.
func TestRelay_LeftoverSummaryFailoverCommitsAndEmitsDiagnostic(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			// Commits work but never finalizes (no laps wrapup), so a stub
			// summary.jsonl is written after the run loop and left uncommitted.
			os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("x"), 0o644)
			runGit(t, workspaceDir, "add", ".")
			runGit(t, workspaceDir, "commit", "-m", "work: done", "--no-verify")
			return &agent.TryResult{Completed: true}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, map[string]agent.Executor{"claude": exec})
	sink := &capturingSink{}
	r.SetTelemetry(sink)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if subject := gitSubject(t, workspaceDir, "HEAD"); subject != "rally: commit leftover summary" {
		t.Fatalf("HEAD subject = %q, want the failover commit", subject)
	}
	if dirty := strings.TrimSpace(runGit(t, workspaceDir, "status", "--porcelain")); dirty != "" {
		t.Errorf("working tree should be clean after the failover, got:\n%s", dirty)
	}

	var found *capturedEvent
	for i := range sink.events {
		if strings.Contains(sink.events[i].msg, "summary.jsonl") {
			found = &sink.events[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a RallyDiagnostic for the leftover summary; events=%v", sink.events)
	}
	if found.evt.Level != telemetry.LevelWarning {
		t.Errorf("failover event level = %q, want warning", found.evt.Level)
	}
}
