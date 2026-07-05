package runner

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestApplyStallRecoveryByRoleWritePolicy(t *testing.T) {
	tests := []struct {
		role        string
		wantSuccess bool
	}{
		{role: "intern", wantSuccess: true},
		{role: "junior", wantSuccess: true},
		{role: "senior", wantSuccess: true},
		{role: "custom-implementer", wantSuccess: true},
		{role: "verify", wantSuccess: false},
		{role: "qa", wantSuccess: false},
		{role: "review", wantSuccess: false},
		{role: "architect", wantSuccess: false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			state := &runOneState{stallMarked: true}
			attempt := &runAttemptState{
				failed:     true,
				commitHash: "abc123",
				attempt:    1,
			}
			var log bytes.Buffer

			applyStallRecovery(
				&store.RelayRecord{ID: 1},
				0,
				runTask{Assignee: tt.role},
				state,
				attempt,
				&log,
			)

			if state.success != tt.wantSuccess {
				t.Fatalf("state.success = %v, want %v; log=%s", state.success, tt.wantSuccess, log.String())
			}
			if attempt.failed == tt.wantSuccess {
				t.Fatalf("attempt.failed = %v, want %v; log=%s", attempt.failed, !tt.wantSuccess, log.String())
			}
		})
	}
}

func TestClassifyInitialFailureNoChangesRoleWritePolicy(t *testing.T) {
	tests := []struct {
		name           string
		role           string
		completed      bool
		wantFailed     bool
		wantFailReason string
	}{
		{
			name:           "verify completed with no changes is not demoted",
			role:           "verify",
			completed:      true,
			wantFailed:     false,
			wantFailReason: "",
		},
		{
			name:           "implementation completed with no changes still fails",
			role:           "senior",
			completed:      true,
			wantFailed:     true,
			wantFailReason: "no changes made",
		},
		{
			name:           "verify incomplete still fails",
			role:           "verify",
			completed:      false,
			wantFailed:     true,
			wantFailReason: "agent error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
			state := &runOneState{}
			attempt := &runAttemptState{
				result:  &harnessapi.TryResult{Completed: tt.completed},
				runtime: time.Minute,
			}

			r.classifyInitialFailure(runTask{Assignee: tt.role}, state, attempt)

			if attempt.failed != tt.wantFailed {
				t.Fatalf("attempt.failed = %v, want %v", attempt.failed, tt.wantFailed)
			}
			if state.failReason != tt.wantFailReason {
				t.Fatalf("failReason = %q, want %q", state.failReason, tt.wantFailReason)
			}
		})
	}
}

func TestRunOneLapPinIgnoresStaleSummaryEntriesForSameOutingID(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	if err := progress.AppendOutingEntry(workspaceDir, progress.OutingEntry{
		OutingID:      "relay-1-run-1",
		Summary:       "stale prior relay entry",
		LapsCompleted: []string{"stale-lap"},
	}); err != nil {
		t.Fatalf("AppendOutingEntry error = %v", err)
	}

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "current.txt"), []byte("done"), 0o644); err != nil {
				return nil, err
			}
			rs, err := progress.LoadRunState(workspaceDir)
			if err != nil {
				return nil, err
			}
			rs.RecordedLaps = []string{"current-lap"}
			if err := progress.SaveRunState(workspaceDir, rs); err != nil {
				return nil, err
			}
			return &harnessapi.TryResult{Completed: true, Summary: "current lap done"}, nil
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
		runTask{Name: "pinned task", Prompt: "do work", Assignee: "senior", LapID: "current-lap", IsLapsBacked: true, LapsRemaining: 1},
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
		t.Fatalf("runOne success = false, fail reason = %q", res.FailReason)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if tries[0].FailReason == "multi_lap_consumed" || tries[0].FailReason == "wrong_lap_consumed" {
		t.Fatalf("unexpected lap pin mismatch: %q", tries[0].FailReason)
	}
	if got, want := tries[0].RecordedLaps, []string{"current-lap"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordedLaps = %v, want %v", got, want)
	}
}

func TestLapPinWrongLapWarningInRunOne(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"other-lap"}
			progress.SaveRunState(workspaceDir, rs)
			return &harnessapi.TryResult{Completed: true, Summary: "wrong lap"}, nil
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
		t.Fatal("expected warning-only success for wrong-lap consumption")
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].FailReason != "wrong_lap_consumed" {
		t.Fatalf("FailReason = %q, want %q", tries[0].FailReason, "wrong_lap_consumed")
	}
}

func TestLapPinMultiLapWarningInRunOne(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"lap-1", "lap-2"}
			progress.SaveRunState(workspaceDir, rs)
			return &harnessapi.TryResult{Completed: true, Summary: "multi lap"}, nil
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
		t.Fatal("expected warning-only success for multi-lap consumption")
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].FailReason != "multi_lap_consumed" {
		t.Fatalf("FailReason = %q, want %q", tries[0].FailReason, "multi_lap_consumed")
	}
}

func TestLapPinMismatchCompletesWhenPinnedLapAlreadyDone(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	lapsDir := filepath.Join(workspaceDir, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir .laps: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lapsDir, "laps.json"), []byte(`{"version":2,"tasks":[{"id":"lap-1","isDone":true},{"id":"other-lap","isDone":false}]}`), 0o644); err != nil {
		t.Fatalf("write laps state: %v", err)
	}

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"other-lap"}
			progress.SaveRunState(workspaceDir, rs)
			return &harnessapi.TryResult{Completed: true, Summary: "wrong lap but pinned done"}, nil
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
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !res.Success {
		t.Fatal("expected already-complete pinned lap mismatch to complete the run")
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if !tries[0].Completed {
		t.Fatal("try should be recorded as completed")
	}
	if tries[0].Outcome != reliability.OutcomeCompleted {
		t.Fatalf("Outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeCompleted)
	}
	if tries[0].FailReason != "wrong_lap_consumed" {
		t.Fatalf("FailReason = %q, want %q", tries[0].FailReason, "wrong_lap_consumed")
	}
	if tries[0].Category != "" {
		t.Fatalf("Category = %q, want empty", tries[0].Category)
	}
}

func TestLapPinMismatchClearsFailureClass(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	callCount := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			callCount++
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			if callCount >= 3 {
				rs, _ := progress.LoadRunState(workspaceDir)
				rs.RecordedLaps = []string{"wrong-lap"}
				progress.SaveRunState(workspaceDir, rs)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "failed"}, nil
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

	res, _ := r.runOne(
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
	failureClass := res.FailureClass

	if failureClass != reliability.FailureAgent {
		t.Fatalf("failureClass = %v, want FailureAgent", failureClass)
	}
}

func TestLapPinNormalPassThroughInRunOne(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"lap-1"}
			progress.SaveRunState(workspaceDir, rs)
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
		t.Fatal("expected success for normal single-lap pass-through")
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	if tries[0].FailReason != "" {
		t.Fatalf("FailReason = %q, want empty", tries[0].FailReason)
	}
	if tries[0].LapID != "lap-1" {
		t.Fatalf("LapID = %q, want %q", tries[0].LapID, "lap-1")
	}
	if len(tries[0].RecordedLaps) != 1 || tries[0].RecordedLaps[0] != "lap-1" {
		t.Fatalf("RecordedLaps = %v, want [lap-1]", tries[0].RecordedLaps)
	}
}

// TestRunOneHonorsExecutorEvidence verifies the live runner path wires
// TryResult.Evidence into ClassifyError so executor evidence participates in
// real classification. The executor reports a non-infra category while the try
// log also carries an infra-matching ("fork/exec") line: evidence must win over
// the text-pattern fallback, classify as FailureAgent, and — critically — NOT
// increment the infra freeze counter.
func TestRunOneHonorsExecutorEvidence(t *testing.T) {
	tests := []struct {
		name     string
		category reliability.FailureCategory
	}{
		{name: "usage_limit", category: reliability.CategoryUsageLimit},
		{name: "invalid_model", category: reliability.CategoryInvalidModel},
		{name: "auth_or_proxy", category: reliability.CategoryAuthOrProxy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			s := newTestStore(t, rallyDir)
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					// Log tail would classify as harness_launch (infra) on its own.
					if opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte("fork/exec /bin/agent: failed\n"), 0o644)
					}
					return &harnessapi.TryResult{
						Completed: false,
						Summary:   "failed",
						Evidence:  &reliability.FailureEvidence{Category: tt.category},
					}, nil
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
				runTask{Name: "task", Prompt: "do work", Assignee: "senior"},
				nil,
				nil,
				false,
				false,
				nil,
				nil,
				io.Discard,
			)
			failureClass, infraFailures := res.FailureClass, res.InfraFailures
			if err != nil {
				t.Fatalf("runOne error = %v", err)
			}
			if failureClass != reliability.FailureAgent {
				t.Fatalf("failureClass = %v, want FailureAgent (evidence %s must win over infra text pattern)", failureClass, tt.category)
			}
			if infraFailures != 0 {
				t.Fatalf("infraFailures = %d, want 0 (%s must not increment the freeze counter)", infraFailures, tt.category)
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("tries = %d, want 1", len(tries))
			}
			if tries[0].Category != string(tt.category) {
				t.Fatalf("Category = %q, want %q", tries[0].Category, tt.category)
			}
			if tries[0].Outcome != reliability.OutcomeFailed {
				t.Fatalf("Outcome = %q, want %q for categorized failure", tries[0].Outcome, reliability.OutcomeFailed)
			}
			if !strings.Contains(tries[0].FailReason, reliability.CategoryDisplayLabel(tt.category)) {
				t.Fatalf("FailReason = %q, want display label containing %q", tries[0].FailReason, reliability.CategoryDisplayLabel(tt.category))
			}
		})
	}
}

func TestRunOneEvidenceBeatsIncompleteClassification(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec /bin/agent: failed\n"), 0o644)
			}
			// Produce an unfinalized task-file change so runOne computes the
			// incomplete context; executor evidence must still take priority.
			_ = os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("dirty\n"), 0o644)
			return &harnessapi.TryResult{
				Completed: false,
				Summary:   "failed",
				Evidence:  &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit},
			}, nil
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
		runTask{Name: "lap task", Prompt: "do work", Assignee: "senior", IsLapsBacked: true},
		nil,
		nil,
		false,
		false,
		nil,
		nil,
		io.Discard,
	)
	failureClass, infraFailures := res.FailureClass, res.InfraFailures
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if failureClass != reliability.FailureAgent {
		t.Fatalf("failureClass = %v, want FailureAgent (executor evidence must beat incomplete context)", failureClass)
	}
	if infraFailures != 0 {
		t.Fatalf("infraFailures = %d, want 0", infraFailures)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if tries[0].Category != string(reliability.CategoryUsageLimit) {
		t.Fatalf("Category = %q, want %q (stronger evidence must beat incomplete classification)", tries[0].Category, reliability.CategoryUsageLimit)
	}
	if !strings.Contains(tries[0].FailReason, "usage limit") {
		t.Fatalf("FailReason = %q, want display label containing 'usage limit'", tries[0].FailReason)
	}
}

// TestRunOneTerminalCategorySingleAttempt verifies the attempt-loop
// short-circuit (design Decision 5 item 1): usage_limit and auth_or_proxy are
// agent-class, so the freeze counter never bounds them — the loop break is what
// caps them at exactly one attempt even when the retry budget is larger. The
// agent_error control proves the cap is category-specific: an ordinary agent
// error still loops the full budget. The terminal cases also assert runOne
// surfaces the resolved category + reset evidence (Decision 5 item 2).
func TestRunOneTerminalCategorySingleAttempt(t *testing.T) {
	const budget = 5
	resetAfter := 3 * time.Hour

	tests := []struct {
		name         string
		category     reliability.FailureCategory
		wantAttempts int
		wantReset    bool
	}{
		{name: "usage_limit short-circuits", category: reliability.CategoryUsageLimit, wantAttempts: 1, wantReset: true},
		{name: "auth_or_proxy short-circuits", category: reliability.CategoryAuthOrProxy, wantAttempts: 1, wantReset: false},
		{name: "agent_error loops the budget", category: reliability.CategoryAgentError, wantAttempts: budget, wantReset: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			s := newTestStore(t, rallyDir)
			callCount := 0
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					callCount++
					if opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte("failed\n"), 0o644)
					}
					ev := &reliability.FailureEvidence{Category: tt.category}
					if tt.wantReset {
						ev.ResetAfter = resetAfter
					}
					return &harnessapi.TryResult{Completed: false, Summary: "failed", Evidence: ev}, nil
				},
			}

			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          t.TempDir(),
				AgentMixSpecs:    []string{"op:dsf"},
				TargetIterations: 1,
				RetryBudget:      budget,
				LapsEnabled:      true,
				Resolver:         cheapTestResolver,
			}, map[string]harnessapi.Executor{"opencode": exec})
			// sleepFunc is a no-op so any (unexpected) wait+resume cooldown does
			// not slow the test; the assertion is on attempt count.
			r.sleepFunc = func(time.Duration) {}

			res, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				harnessapi.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
				runTask{Name: "task", Prompt: "do work", Assignee: "senior"},
				nil,
				nil,
				false,
				false,
				nil,
				nil,
				io.Discard,
			)
			success, failureClass, failureCategory, resetEvidence, infraFailures := res.Success, res.FailureClass, res.Category, res.ResetEvidence, res.InfraFailures
			if err != nil {
				t.Fatalf("runOne error = %v", err)
			}
			if success {
				t.Fatal("expected runOne to report failure")
			}
			if callCount != tt.wantAttempts {
				t.Fatalf("executor called %d times, want %d (retry budget was %d)", callCount, tt.wantAttempts, budget)
			}
			if tries := s.AllTries(); len(tries) != tt.wantAttempts {
				t.Fatalf("recorded %d tries, want %d", len(tries), tt.wantAttempts)
			}

			// Surfaced contract: category always propagates; the terminal cases
			// stay agent-class (no freeze) and carry their reset evidence.
			if failureCategory != tt.category {
				t.Fatalf("surfaced failureCategory = %q, want %q", failureCategory, tt.category)
			}
			if failureClass != reliability.FailureAgent {
				t.Fatalf("failureClass = %v, want FailureAgent", failureClass)
			}
			if infraFailures != 0 {
				t.Fatalf("infraFailures = %d, want 0 (agent-class must not freeze)", infraFailures)
			}
			if tt.wantReset {
				if resetEvidence == nil {
					t.Fatal("expected reset evidence surfaced for usage_limit, got nil")
				} else if resetEvidence.ResetAfter != resetAfter {
					t.Fatalf("surfaced ResetAfter = %v, want %v", resetEvidence.ResetAfter, resetAfter)
				}
			}
		})
	}
}

func TestRunOneLapPinMismatchIsWarningOnly(t *testing.T) {
	tests := []struct {
		name         string
		recordedLaps []string
		wantReason   string
	}{
		{name: "wrong lap", recordedLaps: []string{"lap-2"}, wantReason: "wrong_lap_consumed"},
		{name: "multiple laps", recordedLaps: []string{"lap-1", "lap-2"}, wantReason: "multi_lap_consumed"},
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
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					rs, err := progress.LoadRunState(workspaceDir)
					if err != nil {
						return nil, err
					}
					rs.RecordedLaps = tt.recordedLaps
					if err := progress.SaveRunState(workspaceDir, rs); err != nil {
						return nil, err
					}
					return &harnessapi.TryResult{Completed: true, Summary: "completed a different lap"}, nil
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
				runTask{Name: "pinned task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
				t.Fatal("run success = false, want warning-only mismatch to resolve successfully")
			}
			if res.Outcome != reliability.OutcomeCompleted {
				t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeCompleted)
			}
			if res.FailReason != tt.wantReason {
				t.Fatalf("run FailReason = %q, want %q", res.FailReason, tt.wantReason)
			}
			if res.Category != "" {
				t.Fatalf("run category = %q, want empty for lap-pin mismatch", res.Category)
			}
			if res.FailureClass != reliability.FailureAgent {
				t.Fatalf("run failure class = %q, want %q", res.FailureClass, reliability.FailureAgent)
			}
			if res.InfraFailures != 0 {
				t.Fatalf("infra failures = %d, want 0", res.InfraFailures)
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("tries = %d, want 1", len(tries))
			}
			try := tries[0]
			if !try.Completed {
				t.Fatal("try completed = false, want true for warning-only mismatch")
			}
			if try.Outcome != reliability.OutcomeCompleted {
				t.Fatalf("try outcome = %q, want %q", try.Outcome, reliability.OutcomeCompleted)
			}
			if try.FailReason != tt.wantReason {
				t.Fatalf("try FailReason = %q, want %q", try.FailReason, tt.wantReason)
			}
			if try.Category != "" {
				t.Fatalf("try category = %q, want empty for lap-pin mismatch", try.Category)
			}
			if try.LapID != "lap-1" {
				t.Fatalf("try lap id = %q, want lap-1", try.LapID)
			}
			if try.ResolvedRoute != "senior" {
				t.Fatalf("try resolved route = %q, want senior", try.ResolvedRoute)
			}
			if try.LapAssignee != "senior" {
				t.Fatalf("try lap assignee = %q, want senior", try.LapAssignee)
			}
			if try.AttemptNumber != 1 {
				t.Fatalf("try attempt = %d, want 1", try.AttemptNumber)
			}
			if try.StartedAt == "" || try.EndedAt == "" {
				t.Fatalf("try timestamps incomplete: started=%q ended=%q", try.StartedAt, try.EndedAt)
			}
			if len(try.RecordedLaps) != len(tt.recordedLaps) {
				t.Fatalf("try recorded laps = %v, want %v", try.RecordedLaps, tt.recordedLaps)
			}
			for i := range tt.recordedLaps {
				if try.RecordedLaps[i] != tt.recordedLaps[i] {
					t.Fatalf("try recorded laps = %v, want %v", try.RecordedLaps, tt.recordedLaps)
				}
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
