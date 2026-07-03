package runner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

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
