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
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestFailureCascadeMultipleInfraIncrements(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      3,
		Resolver:         cheapTestResolver,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}
	r.sleepFunc = func(time.Duration) {}

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) < 3 {
		t.Fatalf("got %d tries, want at least 3", len(tries))
	}

	status, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
	foundPause := false
	for _, e := range status {
		if e.EventType == "paused" {
			foundPause = true
			break
		}
	}
	if !foundPause {
		t.Fatal("expected agent paused event")
	}
}

func TestFailureCascadeSingleInfraDoesNotIncrement(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "infra"}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})

	_ = r.Run(context.Background())

	if got := countAgentStatusEvents(s, "paused", "frozen"); got != 0 {
		t.Fatalf("cascade events = %d, want 0", got)
	}
}

func TestFailureCascadeAgentErrorDoesNotIncrement(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "agent failed"}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      2,
		Resolver:         cheapTestResolver,
	}, map[string]harnessapi.Executor{"opencode": exec})

	_ = r.Run(context.Background())

	if got := countAgentStatusEvents(s, "paused", "frozen"); got != 0 {
		t.Fatalf("cascade events = %d, want 0", got)
	}
}

func countAgentStatusEvents(s *store.Store, eventTypes ...string) int {
	wanted := map[string]bool{}
	for _, eventType := range eventTypes {
		wanted[eventType] = true
	}
	count := 0
	for _, event := range s.AllAgentStatus() {
		if wanted[event.EventType] {
			count++
		}
	}
	return count
}

func TestFreezeCascade(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      3,
		Resolver:         cheapTestResolver,
	}, executors)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	// Verify agent is paused after 3 retries exhausted
	cheapKey := ResilienceKey{Harness: "opencode", Model: cheapTestModel}
	st, _ := NewResilience(s).GetState(cheapKey)
	if st != StatePaused {
		t.Fatalf("expected agent paused after retry exhaustion, got %s", st)
	}

	// Simulate 5 hourly retries failing with progressively advancing times
	counter := 0
	resilience := &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc: func() time.Time {
			counter++
			return time.Date(2026, 1, 1, counter, 0, 0, 0, time.UTC)
		},
	}

	for i := 0; i < 5; i++ {
		if err := resilience.RecordHourlyFailure(cheapKey, 1); err != nil {
			t.Fatalf("RecordHourlyFailure %d failed: %v", i+1, err)
		}
	}

	// Verify agent is now frozen
	st, _ = resilience.GetState(cheapKey)
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after 5 hourly retries, got %s", st)
	}

	// Verify a "frozen" event was recorded
	events, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
	foundFrozen := false
	for _, e := range events {
		if e.EventType == "frozen" {
			foundFrozen = true
			break
		}
	}
	if !foundFrozen {
		t.Fatal("expected frozen event in agent status")
	}
}

func TestAgentUnfreeze(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	st, _ := resilience.GetState(ResilienceKey{Harness: "claude", Model: "test"})
	if st != StatePaused {
		t.Fatalf("expected StatePaused after pause, got %s", st)
	}

	if err := resilience.UnpauseAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1); err != nil {
		t.Fatalf("UnpauseAgent failed: %v", err)
	}

	st, _ = resilience.GetState(ResilienceKey{Harness: "claude", Model: "test"})
	if st != StateActive {
		t.Fatalf("expected StateActive after unpause, got %s", st)
	}
}

func TestFailedRunDoesNotCountIteration(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      3,
		Resolver:         cheapTestResolver,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	relays := s.AllRelays()
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relays))
	}
	if relays[0].CompletedIterations != 0 {
		t.Fatalf("expected 0 completed iterations after failed run, got %d", relays[0].CompletedIterations)
	}

	st, _ := NewResilience(s).GetState(ResilienceKey{Harness: "opencode", Model: cheapTestModel})
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after hourly retry exhaustion, got %s", st)
	}
}

func TestHourlyRetryWithOtherAgentActive(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resilience := &Resilience{
		Store:         s,
		PauseDuration: time.Hour,
		NowFunc:       func() time.Time { return baseTime },
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	resilience.NowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }

	mix := AgentMix{
		Cycle: []harnessapi.ResolvedAgent{
			{Harness: "claude", Model: "test"},
			{Harness: "claude", Model: "test"},
			{Harness: "codex", Model: "test"},
		},
	}

	picked, nextRunIndex, isHourlyRetry, err := resilience.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("SelectActiveAgent failed: %v", err)
	}
	if picked.Harness != "claude" {
		t.Fatalf("expected claude (hourly retry), got %s", picked.Harness)
	}
	if nextRunIndex != 1 {
		t.Fatalf("expected nextRunIndex 1, got %d", nextRunIndex)
	}
	if !isHourlyRetry {
		t.Fatal("expected isHourlyRetry=true")
	}
}

func TestE2E_SimulatedFreezeGracefulKillResumeRecovery(t *testing.T) {
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
		RetryBudget:      3,
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

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got, want := resumeIDs, []string{"", "sess-freeze"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ResumeSessionIDs = %v, want %v", got, want)
	}
	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 1 {
		t.Fatalf("expected 1 completed iteration, got %+v", relays)
	}
}

func TestE2E_LivenessProbeClearsFreezeFlag(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	probeCalls := 0
	exec := &funcExecutor{
		probeSupported: true,
		probeFn: func(ctx context.Context) (bool, error) {
			probeCalls++
			return true, nil
		},
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			time.Sleep(50 * time.Millisecond)
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cx:1"},
		TargetIterations: 1,
		RetryBudget:      3,
		LivenessProbe:    true,
		StallThreshold:   50 * time.Millisecond,
	}, map[string]harnessapi.Executor{"codex": exec})

	r.stallControllerFactory = func(string) reliability.StallController {
		probe := r.buildLivenessProbe(exec)
		return &fakeStallController{
			check: func(ctx context.Context) (bool, error) {
				if probe == nil {
					t.Fatal("expected liveness probe to be built for codex")
				}
				if probe.Check(ctx) {
					return false, nil
				}
				return true, nil
			},
		}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = 30 * time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if attempt != 1 {
		t.Fatalf("attempts = %d, want 1 after successful probe", attempt)
	}
	if probeCalls == 0 {
		t.Fatal("expected liveness probe to run")
	}
	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 1 {
		t.Fatalf("expected 1 completed iteration, got %+v", relays)
	}
}

func TestE2E_LivenessProbeFailureConfirmsFreeze(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	freezeCh := make(chan struct{})
	attempt := 0
	probeCalls := 0
	var resumeIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		probeSupported:  true,
		probeFn: func(ctx context.Context) (bool, error) {
			probeCalls++
			return false, nil
		},
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			resumeIDs = append(resumeIDs, opts.ResumeSessionID)
			if attempt == 1 {
				<-freezeCh
				return &harnessapi.TryResult{Completed: false, Summary: "freeze", SessionID: "sess-probe"}, nil
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
		AgentMixSpecs:    []string{"cx:1"},
		TargetIterations: 1,
		RetryBudget:      3,
		LivenessProbe:    true,
	}, map[string]harnessapi.Executor{"codex": exec})

	triggered := false
	r.stallControllerFactory = func(string) reliability.StallController {
		probe := r.buildLivenessProbe(exec)
		return &fakeStallController{
			check: func(ctx context.Context) (bool, error) {
				if triggered {
					return false, nil
				}
				if probe == nil {
					t.Fatal("expected liveness probe to be built for codex")
				}
				if probe.Check(ctx) {
					return false, nil
				}
				triggered = true
				close(freezeCh)
				return true, nil
			},
		}
	}

	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if probeCalls == 0 {
		t.Fatal("expected liveness probe to run")
	}
	if got, want := resumeIDs, []string{"", "sess-probe"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ResumeSessionIDs = %v, want %v", got, want)
	}
}

func TestE2E_ErrorPatternStrategies(t *testing.T) {
	tests := []struct {
		name           string
		agentSpec      string
		harness        string
		logContent     string
		wantNoOp       bool
		wantWaitResume bool
		wantAttempts   int
		wantCompleted  bool
	}{
		{
			name:          "codex limit warning no-op",
			agentSpec:     "cx:1",
			harness:       "codex",
			logContent:    "warning: limit warning reached\ncompletion generated\n",
			wantNoOp:      true,
			wantAttempts:  1,
			wantCompleted: true,
		},
		{
			name:           "claude rate limit wait resume",
			agentSpec:      "cc:1",
			harness:        "claude",
			logContent:     "sending to claude...\nerror 429 Too Many Requests\nretry-after: 1\n",
			wantWaitResume: true,
			wantAttempts:   2,
			wantCompleted:  true,
		},
		{
			name:          "antigravity gemini-cli exit status 1 resumes retry",
			agentSpec:     "ag:1",
			harness:       "antigravity",
			logContent:    "running gemini-cli...\nprovider error\nexit status 1\n",
			wantAttempts:  2,
			wantCompleted: true,
		},
		{
			name:          "unknown failure fresh restart",
			agentSpec:     "cc:1",
			harness:       "claude",
			logContent:    "some unexpected error\nsegfault\n",
			wantAttempts:  2,
			wantCompleted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
					if attempt == 1 && opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte(tt.logContent), 0o644)
					}
					if attempt < tt.wantAttempts {
						return &harnessapi.TryResult{Completed: false, Summary: "fail", SessionID: "sess-1"}, nil
					}
					f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
					f.WriteString("changed")
					f.Close()
					return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
				},
			}
			executors := map[string]harnessapi.Executor{tt.harness: exec}

			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          t.TempDir(),
				AgentMixSpecs:    []string{tt.agentSpec},
				TargetIterations: 1,
				RetryBudget:      3,
				Resolver:         testResolver,
			}, executors)
			r.sleepFunc = func(time.Duration) {}

			err := r.Run(context.Background())

			if err != nil {
				t.Fatalf("run failed: %v", err)
			}

			tries := s.AllTries()
			if len(tries) != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d", len(tries), tt.wantAttempts)
			}
			if tries[len(tries)-1].Completed != tt.wantCompleted {
				t.Fatalf("final try Completed = %v, want %v", tries[len(tries)-1].Completed, tt.wantCompleted)
			}
		})
	}
}

func TestE2E_WindowsFreezeDisabledRetryBudgetExhaustion(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"opencode": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      2,
		Resolver:         cheapTestResolver,
	}, executors)
	// Simulate Windows path: stall controller disabled
	r.stallControllerFactory = func(string) reliability.StallController { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx)
	}()

	deadline := time.After(2 * time.Second)
	for {
		tries := s.AllTries()
		status, err := s.GetAgentStatus("opencode", cheapTestModel)
		if err != nil {
			t.Fatal(err)
		}
		foundPause := false
		for _, e := range status {
			if e.EventType == "paused" {
				foundPause = true
				break
			}
		}
		if len(tries) == 2 && foundPause {
			cancel()
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for retry-budget exhaustion without freeze detection")
		case <-time.After(10 * time.Millisecond):
		}
	}

	err := <-done
	if err == nil || err != context.Canceled {
		t.Fatalf("Run() error = %v, want context.Canceled after stopping paused-agent wait", err)
	}

	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("expected 2 tries (retry budget exhausted), got %d", len(tries))
	}
	status, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
	foundPause := false
	for _, e := range status {
		if e.EventType == "paused" {
			foundPause = true
			break
		}
	}
	if !foundPause {
		t.Fatal("expected agent paused after retry budget exhaustion")
	}
}

func TestProbationIncompletePromotesToActive(t *testing.T) {
	oldHeadPull := headPullLap
	pullCount := 0
	headPullLap = func(context.Context, string) (laps.Lap, laps.QueueState, error) {
		pullCount++
		if pullCount == 1 {
			return laps.Lap{ID: "lap-1", Title: "probation test", Assignee: "senior"}, laps.StateLap, nil
		}
		return laps.NoLap, laps.StateEmpty, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)

	// The executor writes a unique file per attempt (dirty tree) but never
	// calls laps done → incomplete. Each attempt produces its own new dirty
	// path ensures the leftover-aware delta still classifies it incomplete.
	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			_ = os.WriteFile(filepath.Join(workspaceDir, fmt.Sprintf("partial-%d.txt", attempt)), []byte("partial"), 0o644)
			return &harnessapi.TryResult{Completed: true, Summary: "made progress but did not finalize"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"opencode": exec}

	// Set up the agent as frozen long enough ago that it decays to probation.
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	frozenAt := baseTime // frozen 6 hours before "now"
	nowTime := baseTime.Add(6 * time.Hour)

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, executors)
	r.sleepFunc = func(time.Duration) {}
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		FreezeDuration:            5 * time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return nowTime },
	}

	// Seed a frozen event old enough to trigger probation.
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "opencode",
		Model:     cheapTestModel,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	// Confirm setup: agent should be in probation before the run.
	key := ResilienceKey{Harness: "opencode", Model: cheapTestModel}
	st, _ := r.resilience.GetState(key)
	if st != StateProbation {
		t.Fatalf("setup: expected probation, got %s", st)
	}

	_ = r.Run(context.Background())

	// After the incomplete probation run, agent should be active (promoted),
	// not re-frozen.
	st, _ = r.resilience.GetState(key)
	if st != StateActive {
		t.Fatalf("expected active after probation incomplete, got %s", st)
	}

	// Verify the try was recorded as incomplete (not completed).
	tries := s.AllTries()
	if len(tries) == 0 {
		t.Fatal("expected at least one try")
	}
	lastTry := tries[len(tries)-1]
	if lastTry.Completed {
		t.Fatal("incomplete try should be failed")
	}

	// Verify that no frozen event was written after the probation run
	// (an "active"/"unfrozen" event should appear instead of another "frozen").
	events, err := s.GetAgentStatus("opencode", cheapTestModel)
	if err != nil {
		t.Fatal(err)
	}
	lastEventType := ""
	for _, e := range events {
		lastEventType = e.EventType
	}
	if lastEventType == "frozen" {
		t.Fatalf("last event should NOT be frozen after incomplete probation; got events: %v", events)
	}
}
