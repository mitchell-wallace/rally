package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRunOneLapDoneRecoveryPromotesFailedAttempt(t *testing.T) {
	tests := []struct {
		name string
		role string
		// result the harness reports after the laps done hook fired for the
		// pinned lap during the attempt
		completed   bool
		summary     string
		wantWasLine string
	}{
		{
			// Implementation role failed as "no changes made" despite the hook.
			name:        "implementation role completed with no changes",
			role:        "senior",
			completed:   true,
			summary:     "completed with no changes",
			wantWasLine: `treating as success (was: no changes made)`,
		},
		{
			// The corpus incidents (rall-3cc4/00ca) were verify laps whose
			// harness exited unsuccessfully after the hook fired.
			name:        "verify role harness failure after hook",
			role:        "verify",
			completed:   false,
			summary:     "harness died after wrapup",
			wantWasLine: `treating as success (was: agent error)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runLapDoneRecoveryPromotionCase(t, tt.role, tt.completed, tt.summary, tt.wantWasLine)
		})
	}
}

func runLapDoneRecoveryPromotionCase(t *testing.T, role string, completed bool, summary, wantWasLine string) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			rs, err := progress.LoadRunState(workspaceDir)
			if err != nil {
				return nil, err
			}
			rs.RecordedLaps = []string{"lap-1"}
			if err := progress.SaveRunState(workspaceDir, rs); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: completed, Summary: summary}, nil
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

	var log bytes.Buffer
	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "pinned task", Prompt: "do work", Assignee: role, LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		&log,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !res.Success || res.Outcome != reliability.OutcomeCompleted {
		t.Fatalf("run outcome = success %v outcome %q, want completed success", res.Success, res.Outcome)
	}
	if !strings.Contains(log.String(), `lap-done recovery: laps done hook fired for pinned lap "lap-1"; `+wantWasLine) {
		t.Fatalf("log missing lap-done recovery line:\n%s", log.String())
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if !tries[0].Completed || tries[0].Outcome != reliability.OutcomeCompleted {
		t.Fatalf("try completed=%v outcome=%q, want completed outcome", tries[0].Completed, tries[0].Outcome)
	}
	if tries[0].FailReason != "" {
		t.Fatalf("FailReason = %q, want empty after lap-done recovery", tries[0].FailReason)
	}
	if got, want := tries[0].RecordedLaps, []string{"lap-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordedLaps = %v, want %v", got, want)
	}
}

func TestRunOneLapDoneRecoveryDoesNotOverrideDifferentLapMismatch(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			rs, err := progress.LoadRunState(workspaceDir)
			if err != nil {
				return nil, err
			}
			rs.RecordedLaps = []string{"other-lap"}
			if err := progress.SaveRunState(workspaceDir, rs); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "completed other lap"}, nil
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

	var log bytes.Buffer
	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "verify pinned task", Prompt: "verify work", Assignee: "verify", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		&log,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !res.Success || res.FailReason != "wrong_lap_consumed" {
		t.Fatalf("run outcome success=%v fail_reason=%q, want warning-only wrong_lap_consumed", res.Success, res.FailReason)
	}
	if strings.Contains(log.String(), "lap-done recovery") {
		t.Fatalf("lap-done recovery fired for different lap:\n%s", log.String())
	}
}

func TestRunOneSkipsRetryWhenPinnedLapAlreadyDone(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	lapsDir := filepath.Join(workspaceDir, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir .laps: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lapsDir, "laps.json"), []byte(`{"version":2,"tasks":[{"id":"lap-1","isDone":false}]}`), 0o644); err != nil {
		t.Fatalf("write laps state: %v", err)
	}

	s := newTestStore(t, rallyDir)
	attempts := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempts++
			if err := os.WriteFile(filepath.Join(lapsDir, "laps.json"), []byte(`{"version":2,"tasks":[{"id":"lap-1","isDone":true}]}`), 0o644); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: false, Summary: "harness failed after hook"}, nil
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

	var log bytes.Buffer
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
		&log,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if !res.Success || res.Outcome != reliability.OutcomeCompleted {
		t.Fatalf("run outcome = success %v outcome %q, want completed success", res.Success, res.Outcome)
	}
	if !strings.Contains(log.String(), `lap "lap-1" already complete; skipping remaining attempts`) {
		t.Fatalf("log missing already-complete retry guard:\n%s", log.String())
	}
	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want only failed attempt recorded", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeFailed {
		t.Fatalf("try outcome = %q, want original failed attempt record", tries[0].Outcome)
	}
}
