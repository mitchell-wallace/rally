package relay

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/gitx"
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
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
	if res.LapID != "lap-1" {
		t.Fatalf("run lap id = %q, want lap-1", res.LapID)
	}
	if !res.DirtyHandoff {
		t.Fatal("run dirty handoff = false, want true")
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
	if tries[0].ResolvedRoute != "senior" {
		t.Fatalf("resolved route = %q, want senior", tries[0].ResolvedRoute)
	}
	if tries[0].LapAssignee != "senior" {
		t.Fatalf("lap assignee = %q, want senior", tries[0].LapAssignee)
	}
	if !tries[0].DirtyHandoff {
		t.Fatal("dirty handoff = false, want true")
	}
	if len(tries[0].HandoffCreatedLapIDs) != 1 || tries[0].HandoffCreatedLapIDs[0] != "lap-followup" {
		t.Fatalf("handoff created lap ids = %v, want [lap-followup]", tries[0].HandoffCreatedLapIDs)
	}
	if tries[0].CommitHash != "" {
		t.Fatalf("commit hash = %q, want empty dirty-handoff auto-commit suppression", tries[0].CommitHash)
	}
	if tries[0].Category != "" {
		t.Fatalf("try category = %q, want empty for handoff_requested", tries[0].Category)
	}
	if dirty, _ := gitx.IsWorkspaceDirty(workspaceDir); !dirty {
		t.Fatal("workspace dirty = false, want dirty handoff left uncommitted")
	}
}

func TestRunOneCleanHandoffDoesNotSetDirtyRecoveryMetadata(t *testing.T) {
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
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
	if !res.Success || res.Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("run outcome success=%v outcome=%q, want success handoff_requested", res.Success, res.Outcome)
	}
	if res.DirtyHandoff {
		t.Fatal("run dirty handoff = true, want false")
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if tries[0].DirtyHandoff {
		t.Fatal("dirty handoff = true, want false")
	}
	if len(tries[0].HandoffCreatedLapIDs) != 1 || tries[0].HandoffCreatedLapIDs[0] != "lap-followup" {
		t.Fatalf("handoff created lap ids = %v, want [lap-followup]", tries[0].HandoffCreatedLapIDs)
	}
	if dirty, _ := gitx.IsWorkspaceDirty(workspaceDir); dirty {
		t.Fatal("workspace dirty = true, want clean handoff to leave tree clean")
	}
}

func TestRunOneRetryThenDirtyHandoffUsesRunScopedDirtyDetection(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	attempts := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempts++
			if attempts == 1 {
				if err := os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("partial\n"), 0o644); err != nil {
					return nil, err
				}
				return &agent.TryResult{Completed: false, Summary: "partial"}, nil
			}
			if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
				RunID:   "relay-1-run-1",
				Summary: "blocked after retry",
				Handoff: &progress.HandoffEntry{
					Summary:       "blocked after retry",
					Followups:     []string{"follow up"},
					CreatedLapIDs: []string{"lap-followup"},
				},
			}); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "handoff recorded"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
	if !res.Success || res.Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("run outcome success=%v outcome=%q, want success handoff_requested", res.Success, res.Outcome)
	}
	if !res.DirtyHandoff {
		t.Fatal("run dirty handoff = false, want true")
	}

	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("tries = %d, want 2", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeIncomplete {
		t.Fatalf("try 1 outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeIncomplete)
	}
	if tries[0].DirtyHandoff {
		t.Fatal("try 1 dirty handoff = true, want false")
	}
	if tries[1].Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("try 2 outcome = %q, want %q", tries[1].Outcome, reliability.OutcomeHandoffRequested)
	}
	if !tries[1].DirtyHandoff {
		t.Fatal("try 2 dirty handoff = false, want true")
	}
	if tries[1].CommitHash != "" {
		t.Fatalf("try 2 commit hash = %q, want empty dirty-handoff auto-commit suppression", tries[1].CommitHash)
	}
	if len(tries[1].HandoffCreatedLapIDs) != 1 || tries[1].HandoffCreatedLapIDs[0] != "lap-followup" {
		t.Fatalf("try 2 handoff created lap ids = %v, want [lap-followup]", tries[1].HandoffCreatedLapIDs)
	}
	if dirty, _ := gitx.IsWorkspaceDirty(workspaceDir); !dirty {
		t.Fatal("workspace dirty = false, want retry-owned dirty handoff left uncommitted")
	}
}

func TestRunOneRecoveryEffectiveAssigneeUsesRecoveryPromptAndKeepsLapAssignee(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(filepath.Join(rallyDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "agents", "senior.md"), []byte("SENIOR ROLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, "agents", "recovery.md"), []byte("RECOVERY ROLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	var gotRoleInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			gotRoleInstructions = opts.RoleInstructions
			if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
				return nil, err
			}
			if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
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
		runTask{Name: "task", Prompt: "do work", Assignee: "senior", EffectiveAssignee: "recovery", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
	if gotRoleInstructions != "RECOVERY ROLE\n" {
		t.Fatalf("role instructions = %q, want recovery override", gotRoleInstructions)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if tries[0].ResolvedRoute != "recovery" {
		t.Fatalf("resolved route = %q, want recovery", tries[0].ResolvedRoute)
	}
	if tries[0].LapAssignee != "senior" {
		t.Fatalf("lap assignee = %q, want original senior", tries[0].LapAssignee)
	}
}

func TestRunOneRecoveryClassificationPersistence(t *testing.T) {
	tests := []struct {
		name         string
		task         runTask
		recordWrapup func(string) error
		wantOutcome  reliability.TryOutcome
		wantClass    string
	}{
		{
			name: "recovery completion records valid classification",
			task: runTask{Name: "task", Prompt: "do work", Assignee: "senior", EffectiveAssignee: "recovery", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
			recordWrapup: func(workspaceDir string) error {
				if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
					return err
				}
				if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
					return err
				}
				return progress.AppendRunEntry(workspaceDir, progress.RunEntry{
					RunID:          "relay-1-run-1",
					Summary:        "done",
					Classification: "course_correct",
				})
			},
			wantOutcome: reliability.OutcomeCompleted,
			wantClass:   "course_correct",
		},
		{
			name: "recovery handoff records valid classification",
			task: runTask{Name: "task", Prompt: "do work", Assignee: "senior", EffectiveAssignee: "recovery", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
			recordWrapup: func(workspaceDir string) error {
				return progress.AppendRunEntry(workspaceDir, progress.RunEntry{
					RunID:          "relay-1-run-1",
					Summary:        "handoff",
					Classification: "discard",
					Handoff: &progress.HandoffEntry{
						Summary:       "handoff",
						Followups:     []string{},
						CreatedLapIDs: []string{},
					},
				})
			},
			wantOutcome: reliability.OutcomeHandoffRequested,
			wantClass:   "discard",
		},
		{
			name: "non-recovery ignores valid classification",
			task: runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
			recordWrapup: func(workspaceDir string) error {
				if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
					return err
				}
				if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
					return err
				}
				return progress.AppendRunEntry(workspaceDir, progress.RunEntry{
					RunID:          "relay-1-run-1",
					Summary:        "done",
					Classification: "continue",
				})
			},
			wantOutcome: reliability.OutcomeCompleted,
		},
		{
			name: "recovery invalid classification is ignored",
			task: runTask{Name: "task", Prompt: "do work", Assignee: "senior", EffectiveAssignee: "recovery", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
			recordWrapup: func(workspaceDir string) error {
				if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
					return err
				}
				if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
					return err
				}
				return progress.AppendRunEntry(workspaceDir, progress.RunEntry{
					RunID:          "relay-1-run-1",
					Summary:        "done",
					Classification: "bogus",
				})
			},
			wantOutcome: reliability.OutcomeCompleted,
		},
		{
			name: "recovery omitted classification is ignored",
			task: runTask{Name: "task", Prompt: "do work", Assignee: "senior", EffectiveAssignee: "recovery", ResolvedRoute: "recovery", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
			recordWrapup: func(workspaceDir string) error {
				if err := os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done\n"), 0o644); err != nil {
					return err
				}
				return progress.RecordLap(workspaceDir, "lap-1")
			},
			wantOutcome: reliability.OutcomeCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
					if err := tt.recordWrapup(workspaceDir); err != nil {
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
				tt.task,
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
			if res.Outcome != tt.wantOutcome {
				t.Fatalf("run outcome = %q, want %q", res.Outcome, tt.wantOutcome)
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("tries = %d, want 1", len(tries))
			}
			if tries[0].RecoveryClassification != tt.wantClass {
				t.Fatalf("RecoveryClassification = %q, want %q", tries[0].RecoveryClassification, tt.wantClass)
			}
		})
	}
}

func TestRunOneIncompleteAndOrdinaryFailedDoNotSetDirtyHandoff(t *testing.T) {
	tests := []struct {
		name        string
		exec        func(string) *funcExecutor
		wantOutcome reliability.TryOutcome
	}{
		{
			name: "incomplete-alone",
			exec: func(workspaceDir string) *funcExecutor {
				return &funcExecutor{fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					if err := os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("partial\n"), 0o644); err != nil {
						return nil, err
					}
					return &agent.TryResult{Completed: false, Summary: "partial"}, nil
				}}
			},
			wantOutcome: reliability.OutcomeIncomplete,
		},
		{
			name: "ordinary-failed",
			exec: func(workspaceDir string) *funcExecutor {
				return &funcExecutor{fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					return &agent.TryResult{Completed: false, Summary: "failed"}, nil
				}}
			},
			wantOutcome: reliability.OutcomeFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			if err := os.MkdirAll(rallyDir, 0o755); err != nil {
				t.Fatal(err)
			}
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			s := newTestStore(t, rallyDir)
			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          t.TempDir(),
				AgentMixSpecs:    []string{"op:dsf"},
				TargetIterations: 1,
				RetryBudget:      1,
				LapsEnabled:      true,
				Resolver:         cheapTestResolver,
			}, map[string]agent.Executor{"opencode": tt.exec(workspaceDir)})

			res, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
				runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
			if res.Success {
				t.Fatal("runOne success = true, want false")
			}
			if res.Outcome != tt.wantOutcome {
				t.Fatalf("run outcome = %q, want %q", res.Outcome, tt.wantOutcome)
			}
			if res.DirtyHandoff {
				t.Fatal("run dirty handoff = true, want false")
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("tries = %d, want 1", len(tries))
			}
			if tries[0].DirtyHandoff {
				t.Fatal("dirty handoff = true, want false")
			}
			if len(tries[0].HandoffCreatedLapIDs) != 0 {
				t.Fatalf("handoff created lap ids = %v, want empty", tries[0].HandoffCreatedLapIDs)
			}
		})
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
