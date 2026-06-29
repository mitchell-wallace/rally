package runner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/gitx"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func installOperatorKeyboard(t *testing.T) *os.File {
	t.Helper()
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create keyboard pipe: %v", err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = writer.Close()
		_ = reader.Close()
	})
	return writer
}

func sendOperatorAction(t *testing.T, input *os.File, action keyboard.Action) {
	t.Helper()
	var b byte
	switch action {
	case keyboard.ActionQuit:
		b = 0x03
	case keyboard.ActionSkip:
		b = 0x13
	case keyboard.ActionPause:
		b = 0x10
	case keyboard.ActionStop:
		b = 0x18
	default:
		t.Fatalf("unsupported keyboard action %v", action)
	}
	if _, err := input.Write([]byte{b, b}); err != nil {
		t.Fatalf("write keyboard action %v: %v", action, err)
	}
}

func awaitRunError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

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

func TestRunOneOperatorCancellationPersistsCancelledNotFailed(t *testing.T) {
	tests := []struct {
		name            string
		action          keyboard.Action
		source          string
		cleanExit       bool
		wantInterrupted bool
		wantSkipFlag    bool
		wantStopFlag    bool
	}{
		{
			name:         "ctrl+s skip",
			action:       keyboard.ActionSkip,
			source:       "skip",
			wantSkipFlag: true,
		},
		{
			name:            "quit-now overrides harness error",
			action:          keyboard.ActionQuit,
			source:          "quit_now",
			wantInterrupted: true,
			wantStopFlag:    true,
		},
		{
			// Task 2.3: operator cancellation must win even when the
			// subprocess exits cleanly (Completed=true, nil error) right
			// after the SIGINT — e.g. the agent happened to finish in the
			// drain window. The attempt must still persist as cancelled, not
			// as a completed success.
			name:            "quit-now overrides clean exit",
			action:          keyboard.ActionQuit,
			source:          "quit_now",
			cleanExit:       true,
			wantInterrupted: true,
			wantStopFlag:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts int32
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					atomic.AddInt32(&attempts, 1)
					<-ctx.Done()
					if tt.cleanExit {
						return &agent.TryResult{
							Completed: true,
							Summary:   "agent finished cleanly after SIGINT",
						}, nil
					}
					return &agent.TryResult{
						Completed:     false,
						Summary:       "operator cancelled the try",
						RemainingWork: "none",
					}, errors.New("harness cleanup failed")
				},
			}
			r, s, _ := newTimeoutTestRunner(t, exec, Config{RetryBudget: 3})
			sink := &capturingSink{}
			r.SetTelemetry(sink)

			input := installOperatorKeyboard(t)
			done := driveRunOneTaskAsync(t, r, runTimeoutTask())
			waitForAttempts(t, &attempts, 1)
			sendOperatorAction(t, input, tt.action)

			res := awaitRunOne(t, done)
			if res.Success {
				t.Fatal("cancelled runOne result must not be marked successful")
			}
			if res.Outcome != reliability.OutcomeCancelled {
				t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeCancelled)
			}
			if res.Interrupted != tt.wantInterrupted {
				t.Fatalf("run interrupted = %v, want %v", res.Interrupted, tt.wantInterrupted)
			}
			if res.Category != "" {
				t.Fatalf("run category = %q, want empty for operator cancellation", res.Category)
			}
			if res.InfraFailures != 0 {
				t.Fatalf("infra failures = %d, want 0 for operator cancellation", res.InfraFailures)
			}
			if got := atomic.LoadInt32(&attempts); got != 1 {
				t.Fatalf("executor attempts = %d, want 1: cancelled attempts must not retry", got)
			}
			if r.skipFlag.Load() != tt.wantSkipFlag {
				t.Fatalf("skipFlag = %v, want %v", r.skipFlag.Load(), tt.wantSkipFlag)
			}
			if r.stopFlag.Load() != tt.wantStopFlag {
				t.Fatalf("stopFlag = %v, want %v", r.stopFlag.Load(), tt.wantStopFlag)
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("persisted tries = %d, want 1", len(tries))
			}
			try := tries[0]
			if try.Completed {
				t.Fatal("cancelled try Completed = true, want false")
			}
			if try.Outcome != reliability.OutcomeCancelled {
				t.Fatalf("try outcome = %q, want %q", try.Outcome, reliability.OutcomeCancelled)
			}
			if try.CancellationSource != tt.source {
				t.Fatalf("try cancellation source = %q, want %q", try.CancellationSource, tt.source)
			}
			if try.FailReason != "cancelled ("+tt.source+")" {
				t.Fatalf("try fail reason = %q, want cancelled source", try.FailReason)
			}
			if try.Category != "" {
				t.Fatalf("try category = %q, want empty", try.Category)
			}
			if try.AttemptNumber != 1 {
				t.Fatalf("try attempt = %d, want 1", try.AttemptNumber)
			}

			if len(sink.failures) != 0 {
				t.Fatalf("operator cancellation emitted failure telemetry: %#v", sink.failures)
			}
			if len(sink.events) != 0 {
				t.Fatalf("operator cancellation emitted diagnostic events: %#v", sink.events)
			}
			log := findTryLogByOutcome(t, sink, string(reliability.OutcomeCancelled))
			if got := log["cancellation_source"]; got != tt.source {
				t.Fatalf("try log cancellation_source = %#v, want %q", got, tt.source)
			}
			if got := log["completed"]; got != false {
				t.Fatalf("try log completed = %#v, want false", got)
			}
			for _, key := range []string{"failure_category", "failure_class", "fail_reason"} {
				if _, found := log[key]; found {
					t.Fatalf("cancelled try log must not carry %s: %#v", key, log)
				}
			}
			span := findTrySpanByOutcome(t, sink, string(reliability.OutcomeCancelled))
			wantTag(t, span.tags, "cancellation_source", tt.source)
			wantNoTag(t, span.tags, "failure_category")
			if got := span.data["cancellation_source"]; got != tt.source {
				t.Fatalf("try span cancellation_source = %#v, want %q", got, tt.source)
			}
			if got := span.data["completed"]; got != false {
				t.Fatalf("try span completed = %#v, want false", got)
			}
		})
	}
}

func TestRunGracefulStopCancellationPersistsAndStopsRelay(t *testing.T) {
	var attempts int32
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			atomic.AddInt32(&attempts, 1)
			<-ctx.Done()
			return &agent.TryResult{Completed: false, Summary: "operator stopped the relay"}, ctx.Err()
		},
	}
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 3,
		RetryBudget:      3,
	})
	sink := &capturingSink{}
	r.SetTelemetry(sink)

	input := installOperatorKeyboard(t)
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	waitForAttempts(t, &attempts, 1)
	sendOperatorAction(t, input, keyboard.ActionStop)

	if err := awaitRunError(t, done); err != nil {
		t.Fatalf("Run error = %v, want nil after graceful stop", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor attempts = %d, want 1: graceful stop must halt the relay", got)
	}
	if !r.stopFlag.Load() {
		t.Fatal("stopFlag = false, want true after graceful stop")
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("persisted tries = %d, want 1", len(tries))
	}
	try := tries[0]
	if try.Outcome != reliability.OutcomeCancelled {
		t.Fatalf("try outcome = %q, want %q", try.Outcome, reliability.OutcomeCancelled)
	}
	if try.CancellationSource != "graceful_stop" {
		t.Fatalf("try cancellation source = %q, want graceful_stop", try.CancellationSource)
	}
	if try.Completed {
		t.Fatal("graceful-stop try Completed = true, want false")
	}
	if try.Category != "" {
		t.Fatalf("try category = %q, want empty", try.Category)
	}

	relays := s.AllRelays()
	if len(relays) != 1 {
		t.Fatalf("relays = %d, want 1", len(relays))
	}
	if relays[0].CompletedIterations >= relays[0].TargetIterations {
		t.Fatalf("relay completed %d/%d iterations; graceful stop should halt before completion", relays[0].CompletedIterations, relays[0].TargetIterations)
	}
	if got := countAgentStatusEvents(s, "paused", "frozen", "retry_failed", "benched"); got != 0 {
		t.Fatalf("operator cancellation wrote %d resilience status event(s), want 0", got)
	}
	if len(sink.failures) != 0 {
		t.Fatalf("graceful stop emitted failure telemetry: %#v", sink.failures)
	}
	if len(sink.events) != 0 {
		t.Fatalf("graceful stop emitted diagnostic events: %#v", sink.events)
	}
	log := findTryLogByOutcome(t, sink, string(reliability.OutcomeCancelled))
	if got := log["cancellation_source"]; got != "graceful_stop" {
		t.Fatalf("try log cancellation_source = %#v, want graceful_stop", got)
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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					rs, err := progress.LoadRunState(workspaceDir)
					if err != nil {
						return nil, err
					}
					rs.RecordedLaps = tt.recordedLaps
					if err := progress.SaveRunState(workspaceDir, rs); err != nil {
						return nil, err
					}
					return &agent.TryResult{Completed: true, Summary: "completed a different lap"}, nil
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
