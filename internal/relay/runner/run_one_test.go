package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRetryWithinRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt < 3 {
				return &harnessapi.TryResult{Completed: false, Summary: "fail"}, nil
			}
			// Create a file so the successful try is not a no-op failure.
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("success-%d.txt", attempt)))
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

	tries := s.AllTries()
	if len(tries) != 3 {
		t.Fatalf("got %d tries, want 3", len(tries))
	}
	for i, tr := range tries {
		if tr.RunID != 1 {
			t.Fatalf("try %d runID = %d, want 1", i, tr.RunID)
		}
		if tr.AttemptNumber != i+1 {
			t.Fatalf("try %d attempt = %d, want %d", i, tr.AttemptNumber, i+1)
		}
	}
	if tries[2].Completed != true {
		t.Fatal("final try should be completed")
	}
	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 1 {
		t.Fatalf("expected 1 completed iteration, got %+v", relays)
	}
}

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

func TestRunOneFreezeRetryResumesAndRecovers(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	freezeCh := make(chan struct{})
	attempt := 0
	var resumeIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			resumeIDs = append(resumeIDs, opts.ResumeSessionID)
			if attempt == 1 {
				<-freezeCh
				return &harnessapi.TryResult{Completed: false, Summary: "freeze", SessionID: "sess-freeze"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, map[string]harnessapi.Executor{"claude": exec})

	controllerCount := 0
	r.stallControllerFactory = func(logPath string) reliability.StallController {
		_ = logPath
		controllerCount++
		if controllerCount == 1 {
			triggered := false
			return &fakeStallController{
				check: func(context.Context) (bool, error) {
					if triggered {
						return false, nil
					}
					triggered = true
					close(freezeCh)
					return true, nil
				},
			}
		}
		return &fakeStallController{}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	freezeCalls := 0
	recoveredCalls := 0
	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "relay run", Prompt: "freeze test"},
		nil,
		nil,
		false,
		false,
		func() { freezeCalls++ },
		func() { recoveredCalls++ },
		io.Discard,
	)
	success, addressed, interrupted := res.Success, res.Addressed, res.Interrupted
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	if !success {
		t.Fatal("expected runOne success after freeze retry")
	}
	if addressed {
		t.Fatal("message should not be marked addressed")
	}
	if interrupted {
		t.Fatal("runOne should not report interruption")
	}
	if got, want := resumeIDs, []string{"", "sess-freeze"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ResumeSessionIDs = %v, want %v", got, want)
	}
	if freezeCalls != 1 {
		t.Fatalf("freeze callback count = %d, want 1", freezeCalls)
	}
	if recoveredCalls != 1 {
		t.Fatalf("freeze recovered callback count = %d, want 1", recoveredCalls)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("tries = %d, want 2", len(tries))
	}
	if tries[0].Completed {
		t.Fatal("first try should be incomplete after freeze")
	}
	if !tries[1].Completed {
		t.Fatal("second try should complete successfully")
	}
}

func TestResumeRetryPreservesRunState(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "test"}, nil }
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

func TestFreshStartRetryClearsRunState(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "test"}, nil }
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
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "test"}, nil }
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var runStateAtAttempt2 *progress.RunState
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
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "test"}, nil }
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var runStateAtAttempt2 *progress.RunState
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

func TestStallRecovery_VerifyRoleExcluded(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	stallCh := make(chan struct{})
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			os.WriteFile(filepath.Join(workspaceDir, "fix.txt"), []byte("trivial fix"), 0o644)
			runGit(t, workspaceDir, "add", "fix.txt")
			runGit(t, workspaceDir, "commit", "-m", "trivial fix", "--no-verify")
			<-stallCh
			return &harnessapi.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
	}, map[string]harnessapi.Executor{"claude": exec})

	r.stallControllerFactory = func(string) reliability.StallController {
		triggered := false
		return &fakeStallController{
			check: func(context.Context) (bool, error) {
				if triggered {
					return false, nil
				}
				triggered = true
				close(stallCh)
				return true, nil
			},
		}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "verify task", Prompt: "check correctness", Assignee: "verify", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
	if success {
		t.Fatal("expected failure for stalled VERIFY run despite committed files")
	}
}

func TestStallRecovery_ImplementationRoleRecovers(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	stallCh := make(chan struct{})
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			os.WriteFile(filepath.Join(workspaceDir, "impl.txt"), []byte("implementation"), 0o644)
			runGit(t, workspaceDir, "add", "impl.txt")
			runGit(t, workspaceDir, "commit", "-m", "implementation work", "--no-verify")
			<-stallCh
			return &harnessapi.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
	}, map[string]harnessapi.Executor{"claude": exec})

	r.stallControllerFactory = func(string) reliability.StallController {
		triggered := false
		return &fakeStallController{
			check: func(context.Context) (bool, error) {
				if triggered {
					return false, nil
				}
				triggered = true
				close(stallCh)
				return true, nil
			},
		}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "senior task", Prompt: "implement feature", Assignee: "senior", LapID: "lap-1", IsLapsBacked: true, LapsRemaining: 1},
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
		t.Fatal("expected stall recovery success for SENIOR run with committed files")
	}
}

func TestStallRecovery_VerifyStalledWithCommits_StaysFailed(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	freezeCh := make(chan struct{})
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			// Create a file so there are changes to auto-commit
			f, _ := os.Create(filepath.Join(workspaceDir, "verify-fix.txt"))
			f.WriteString("trivial fix")
			f.Close()
			<-freezeCh
			return &harnessapi.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1, // only 1 attempt
	}, map[string]harnessapi.Executor{"claude": exec})

	controllerCount := 0
	r.stallControllerFactory = func(logPath string) reliability.StallController {
		controllerCount++
		if controllerCount == 1 {
			triggered := false
			return &fakeStallController{
				check: func(context.Context) (bool, error) {
					if triggered {
						return false, nil
					}
					triggered = true
					close(freezeCh)
					return true, nil
				},
			}
		}
		return &fakeStallController{}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "verify run", Prompt: "verify test", Assignee: "verify"},
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
	if success {
		t.Fatal("expected runOne to fail for stalled VERIFY even with commits")
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if tries[0].Completed {
		t.Fatal("stalled VERIFY try should not be auto-completed")
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected auto-commit hash to be present")
	}
}

func TestStallRecovery_ImplementationStalledWithCommits_Recovers(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	freezeCh := make(chan struct{})
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			f, _ := os.Create(filepath.Join(workspaceDir, "impl-fix.txt"))
			f.WriteString("implementation fix")
			f.Close()
			<-freezeCh
			return &harnessapi.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
	}, map[string]harnessapi.Executor{"claude": exec})

	controllerCount := 0
	r.stallControllerFactory = func(logPath string) reliability.StallController {
		controllerCount++
		if controllerCount == 1 {
			triggered := false
			return &fakeStallController{
				check: func(context.Context) (bool, error) {
					if triggered {
						return false, nil
					}
					triggered = true
					close(freezeCh)
					return true, nil
				},
			}
		}
		return &fakeStallController{}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		harnessapi.ResolvedAgent{Harness: "claude"},
		runTask{Name: "impl run", Prompt: "impl test", Assignee: "senior"},
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
		t.Fatal("expected runOne to succeed for stalled implementation with commits")
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if !tries[0].Completed {
		t.Fatal("stalled implementation try with commits should be auto-completed")
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected auto-commit hash to be present")
	}
}

func TestRunOneLapPinIgnoresStaleSummaryEntriesForSameRunID(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:         "relay-1-run-1",
		Summary:       "stale prior relay entry",
		LapsCompleted: []string{"stale-lap"},
	}); err != nil {
		t.Fatalf("AppendRunEntry error = %v", err)
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

func TestRunBenchesOpencodeUsageLimitQuotaScopeNotAgentError(t *testing.T) {
	tests := []struct {
		name           string
		category       reliability.FailureCategory
		routeSpecs     map[string][]string
		wantScope      string
		wantBenched    []ResilienceKey
		wantNotBenched []ResilienceKey
	}{
		{
			name:     "zai usage limit benches zai provider scope",
			category: reliability.CategoryUsageLimit,
			routeSpecs: map[string][]string{
				"default": {
					"opencode:zai-coding-plan/glm-5.2",
					"opencode:zai-coding-plan/glm-5.1",
					"opencode:opencode-go/kimi",
				},
			},
			wantScope: "opencode:zai-coding-plan",
			wantBenched: []ResilienceKey{
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.2"},
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.1"},
			},
			wantNotBenched: []ResilienceKey{
				{Harness: "opencode", Model: "opencode-go/kimi"},
			},
		},
		{
			name:     "opencode-go usage limit benches opencode-go provider scope",
			category: reliability.CategoryUsageLimit,
			routeSpecs: map[string][]string{
				"default": {
					"opencode:opencode-go/kimi",
					"opencode:opencode-go/qwen",
					"opencode:zai-coding-plan/glm-5.2",
				},
			},
			wantScope: "opencode:opencode-go",
			wantBenched: []ResilienceKey{
				{Harness: "opencode", Model: "opencode-go/kimi"},
				{Harness: "opencode", Model: "opencode-go/qwen"},
			},
			wantNotBenched: []ResilienceKey{
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.2"},
			},
		},
		{
			name:     "agent error does not bench provider scope",
			category: reliability.CategoryAgentError,
			routeSpecs: map[string][]string{
				"default": {
					"opencode:zai-coding-plan/glm-5.2",
					"opencode:zai-coding-plan/glm-5.1",
					"opencode:opencode-go/kimi",
				},
			},
			wantNotBenched: []ResilienceKey{
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.2"},
				{Harness: "opencode", Model: "zai-coding-plan/glm-5.1"},
				{Harness: "opencode", Model: "opencode-go/kimi"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldHeadPull := headPullLap
			oldQueueSize := queueSize
			claimed := false
			headPullLap = func(context.Context, string) (laps.Lap, error) {
				if claimed {
					return laps.NoLap, nil
				}
				claimed = true
				return laps.Lap{ID: "lap-opencode-limit", Title: "opencode limit", Assignee: "senior"}, nil
			}
			queueSize = func(context.Context, string) (int, error) { return 1, nil }
			defer func() {
				headPullLap = oldHeadPull
				queueSize = oldQueueSize
			}()

			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)
			runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

			s := newTestStore(t, rallyDir)
			execCalls := 0
			exec := &funcExecutor{
				fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					execCalls++
					ev := &reliability.FailureEvidence{Category: tt.category}
					if tt.category == reliability.CategoryUsageLimit {
						ev.ResetAfter = time.Hour
					}
					return &harnessapi.TryResult{Completed: false, Summary: "failed", Evidence: ev}, nil
				},
			}

			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          t.TempDir(),
				RouteSpecs:       tt.routeSpecs,
				TargetIterations: 1,
				RetryBudget:      1,
				LapsEnabled:      true,
				Resolver:         testResolver,
			}, map[string]harnessapi.Executor{"opencode": exec})

			if err := r.Run(context.Background()); err != nil {
				t.Fatalf("Run error = %v", err)
			}
			if execCalls != 1 {
				t.Fatalf("executor calls = %d, want 1", execCalls)
			}

			resilience := NewResilience(s)
			for _, key := range tt.wantBenched {
				if st, _ := resilience.GetState(key); st != StateBenched {
					t.Errorf("state(%s) = %s, want benched", key, st)
				}
				events, err := s.GetAgentStatus(key.Harness, key.Model)
				if err != nil {
					t.Fatalf("GetAgentStatus(%s): %v", key, err)
				}
				foundBench := false
				for _, event := range events {
					if event.EventType != "benched" {
						continue
					}
					foundBench = true
					if event.QuotaScope != tt.wantScope {
						t.Errorf("quota_scope(%s) = %q, want %q", key, event.QuotaScope, tt.wantScope)
					}
					if event.ResetAt == "" {
						t.Errorf("reset_at(%s) is empty on benched event", key)
					}
				}
				if !foundBench {
					t.Errorf("no benched event recorded for %s", key)
				}
			}
			for _, key := range tt.wantNotBenched {
				if st, _ := resilience.GetState(key); st == StateBenched {
					t.Errorf("state(%s) = benched, want not benched", key)
				}
			}
			if tt.category == reliability.CategoryAgentError {
				if got := countAgentStatusEvents(s, "benched"); got != 0 {
					t.Fatalf("benched events = %d, want 0 for agent_error", got)
				}
			}
		})
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

func TestLapAttemptRecordedInTryRecord(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			progress.RecordLap(workspaceDir, "lap-1")
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
		t.Fatal("expected success")
	}

	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	attempts := tries[0].LapsAttempted
	if len(attempts) == 0 {
		t.Fatal("expected laps_attempted to be recorded on TryRecord")
	}
	found := false
	for _, a := range attempts {
		if a.LapID == "lap-1" {
			found = true
			if a.Timestamp == "" {
				t.Fatal("expected non-empty timestamp in lap attempt")
			}
			break
		}
	}
	if !found {
		t.Fatalf("laps_attempted = %v, want lap-1 present", attempts)
	}
}

// --- Leftover-work commit guidance tests (task 2.4-2.5) ---

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
