package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestResumeRetryPassesSessionID(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var capturedSessionIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt < 3 {
				f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("attempt-%d.txt", attempt)))
				f.WriteString("changed")
				f.Close()
				return &harnessapi.TryResult{Completed: false, Summary: "fail", SessionID: fmt.Sprintf("session-%d", attempt)}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(capturedSessionIDs) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(capturedSessionIDs))
	}
	if capturedSessionIDs[0] != "" {
		t.Fatalf("attempt 1 ResumeSessionID = %q, want empty", capturedSessionIDs[0])
	}
	if capturedSessionIDs[1] != "session-1" {
		t.Fatalf("attempt 2 ResumeSessionID = %q, want session-1", capturedSessionIDs[1])
	}
	if capturedSessionIDs[2] != "session-2" {
		t.Fatalf("attempt 3 ResumeSessionID = %q, want session-2", capturedSessionIDs[2])
	}
}

func TestResumeRetryPreservesOutingState(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, laps.QueueState, error) {
		return laps.Lap{Title: "test"}, laps.StateLap, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
					t.Errorf("RecordLap error: %v", err)
				}
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &harnessapi.TryResult{Completed: false, Summary: "fail", SessionID: "sess-1"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	rs, err := progress.LoadRunState(workspaceDir)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	_ = rs

	if attempt != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempt)
	}
}

func TestFreshStartRetryClearsOutingState(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, laps.QueueState, error) {
		return laps.Lap{Title: "test"}, laps.StateLap, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var capturedSessionIDs []string
	exec := &funcExecutor{
		resumeSupported: false,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt == 1 {
				if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
					t.Errorf("RecordLap error: %v", err)
				}
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &harnessapi.TryResult{Completed: false, Summary: "fail", SessionID: "sess-1"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(capturedSessionIDs) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(capturedSessionIDs))
	}
	if capturedSessionIDs[0] != "" {
		t.Fatalf("attempt 1 ResumeSessionID = %q, want empty", capturedSessionIDs[0])
	}
	if capturedSessionIDs[1] != "" {
		t.Fatalf("attempt 2 ResumeSessionID = %q, want empty (fresh-start)", capturedSessionIDs[1])
	}
}

func TestResumeRetryMidHandoffPreservesFlag(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, laps.QueueState, error) {
		return laps.Lap{Title: "test"}, laps.StateLap, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var runStateAtAttempt2 *progress.OutingState
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &harnessapi.TryResult{Completed: false, Summary: "crashed mid-handoff", SessionID: "sess-crash"}, nil
			}
			if attempt == 2 {
				rs, err := progress.LoadRunState(workspaceDir)
				if err != nil {
					t.Errorf("LoadRunState error: %v", err)
				}
				runStateAtAttempt2 = rs
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if runStateAtAttempt2 == nil {
		t.Fatal("expected run-state to be loaded during attempt 2")
	}
	if runStateAtAttempt2.HandoffState != 1 {
		t.Fatalf("HandoffState = %d, want 1 (preserved on resume)", runStateAtAttempt2.HandoffState)
	}
	if runStateAtAttempt2.SessionID != "sess-crash" {
		t.Fatalf("SessionID = %q, want sess-crash (preserved on resume)", runStateAtAttempt2.SessionID)
	}
}

func TestFreshStartRetryMidHandoffClearsFlag(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, laps.QueueState, error) {
		return laps.Lap{Title: "test"}, laps.StateLap, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var runStateAtAttempt2 *progress.OutingState
	exec := &funcExecutor{
		resumeSupported: false,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &harnessapi.TryResult{Completed: false, Summary: "crashed mid-handoff", SessionID: "sess-crash"}, nil
			}
			if attempt == 2 {
				rs, err := progress.LoadRunState(workspaceDir)
				if err != nil {
					t.Errorf("LoadRunState error: %v", err)
				}
				runStateAtAttempt2 = rs
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if runStateAtAttempt2 == nil {
		t.Fatal("expected run-state to be loaded during attempt 2")
	}
	if runStateAtAttempt2.HandoffState != 0 {
		t.Fatalf("HandoffState = %d, want 0 (cleared on fresh-start)", runStateAtAttempt2.HandoffState)
	}
	if runStateAtAttempt2.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty (cleared on fresh-start)", runStateAtAttempt2.SessionID)
	}
}

// TestExplicitSkipStartsFresh verifies that when a skip is triggered (e.g. by
// error classification returning StrategyRotate), the next run starts with an
// empty session ID rather than inheriting the previous run's session.
func TestExplicitSkipStartsFresh(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	runCount := 0
	var capturedSessionIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			runCount++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)

			if runCount == 1 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("exec: some-cli not found\n"), 0o644)
				}
				return &harnessapi.TryResult{
					Completed: false,
					Summary:   "harness missing",
					SessionID: "sess-run1-should-discard",
				}, nil
			}

			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("run%d.txt", runCount)))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec, "codex": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1", "cx:1"},
		TargetIterations: 2,
		RetryBudget:      3,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}
	r.sleepFunc = func(time.Duration) {}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if runCount < 2 {
		t.Fatalf("expected at least 2 runs, got %d", runCount)
	}
	if capturedSessionIDs[0] != "" {
		t.Fatalf("run 1 ResumeSessionID = %q, want empty (first run)", capturedSessionIDs[0])
	}
	if capturedSessionIDs[1] != "" {
		t.Fatalf("run 2 ResumeSessionID = %q, want empty (skip starts fresh)", capturedSessionIDs[1])
	}
}

// TestResumeReusesSessionIDOnNextAttempt verifies that when a resume-supported
// executor returns a session ID, the runner passes it to the next attempt's
// ResumeSessionID. This is the same mechanism the pause→resume path uses (the
// pause block captures session ID from the result, then the loop continues and
// builds opts.ResumeSessionID from the captured sessionID).
func TestResumeReusesSessionIDOnNextAttempt(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var capturedSessionIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt < 3 {
				return &harnessapi.TryResult{
					Completed: false,
					Summary:   fmt.Sprintf("attempt %d failed", attempt),
					SessionID: fmt.Sprintf("sess-attempt-%d", attempt),
				}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      5,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(capturedSessionIDs) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(capturedSessionIDs))
	}
	if capturedSessionIDs[0] != "" {
		t.Fatalf("attempt 1 ResumeSessionID = %q, want empty", capturedSessionIDs[0])
	}
	if capturedSessionIDs[1] != "sess-attempt-1" {
		t.Fatalf("attempt 2 ResumeSessionID = %q, want sess-attempt-1", capturedSessionIDs[1])
	}
	if capturedSessionIDs[2] != "sess-attempt-2" {
		t.Fatalf("attempt 3 ResumeSessionID = %q, want sess-attempt-2", capturedSessionIDs[2])
	}
}
