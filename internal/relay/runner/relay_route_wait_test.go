package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/laps"
	relaycore "github.com/mitchell-wallace/rally/internal/relay"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestAgentCyclingDeterminism(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	s := newTestStore(t, rallyDir)
	var agents []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			agents = append(agents, opts.Persona)
			changeCounter++
			// Append unique content so each try produces a distinct change.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec, "codex": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1", "cx:1"},
		TargetIterations: 4,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	want := []string{"claude", "codex", "claude", "codex"}
	if len(agents) != len(want) {
		t.Fatalf("got %d tries, want %d", len(agents), len(want))
	}
	for i, w := range want {
		if agents[i] != w {
			t.Fatalf("try %d agent = %q, want %q", i, agents[i], w)
		}
	}
}

func TestRunnerSameHarnessAdvanceUsesRotateModel(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var executedModels []string
	exec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			f, _ := os.OpenFile(filepath.Join(workspaceDir, fmt.Sprintf("change-%d.txt", len(executedModels))), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString(opts.Model)
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:model-a:1", "op:model-b:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "rotate",
	}, map[string]harnessapi.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := exec.rotateCalls; len(got) != 1 || got[0] != "model-b" {
		t.Fatalf("RotateModel calls = %v, want [model-b]", got)
	}
	if got, want := executedModels, []string{"model-a", "model-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}
}

func TestRunnerCrossHarnessAdvanceDoesNotRotate(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	opExec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "opencode.txt"), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString("opencode")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}
	codexExec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "codex.txt"), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString("codex")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:model-a:1", "cx:model-b:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "cross harness",
	}, map[string]harnessapi.Executor{
		"opencode": opExec,
		"codex":    codexExec,
	})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(opExec.rotateCalls) != 0 {
		t.Fatalf("RotateModel calls = %v, want none for cross-harness advance", opExec.rotateCalls)
	}
}

func TestRunnerRotateModelErrorFallsBackToExecution(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	dataDir := t.TempDir()
	var executedModels []string
	exec := &funcExecutor{
		rotateSupported: true,
		rotateErr:       fmt.Errorf("rotate failed"),
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			f, _ := os.OpenFile(filepath.Join(workspaceDir, fmt.Sprintf("change-%d.txt", len(executedModels))), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString(opts.Model)
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      dataDir,
		RouteSpecs: map[string][]string{
			"default": {"op:model-a:1", "op:model-b:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "fallback",
	}, map[string]harnessapi.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := exec.rotateCalls; len(got) != 1 || got[0] != "model-b" {
		t.Fatalf("RotateModel calls = %v, want [model-b]", got)
	}
	if got, want := executedModels, []string{"model-a", "model-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}

	logData, err := os.ReadFile(relayLogPath(dataDir, workspaceDir, 1))
	if err != nil {
		t.Fatalf("read relay log: %v", err)
	}
	if !strings.Contains(string(logData), "rotate fallback for opencode: rotate failed") {
		t.Fatalf("relay log = %q, want rotate fallback message", string(logData))
	}
}

func TestAllAgentsFrozenEndsRelay(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.FreezeAgent(ResilienceKey{Harness: "claude", Model: "test"}, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent claude failed: %v", err)
	}
	if err := resilience.FreezeAgent(ResilienceKey{Harness: "codex", Model: "test"}, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent codex failed: %v", err)
	}

	mix := AgentMix{
		Cycle: []harnessapi.ResolvedAgent{
			{Harness: "claude", Model: "test"},
			{Harness: "codex", Model: "test"},
		},
	}

	_, _, _, err := resilience.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error from SelectActiveAgent")
	}
	if err.Error() != "all agents frozen" {
		t.Fatalf("expected 'all agents frozen' error, got %q", err.Error())
	}
}

func TestRunnerRouteIntegration_AssigneesQuotasFreezeAndRoleFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	agentsDir := filepath.Join(rallyDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	if err := os.WriteFile(filepath.Join(agentsDir, "SENIOR.md"), []byte("Senior route guidance."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "junior.md"), []byte("Junior route guidance."), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)

	type execution struct {
		persona          string
		roleInstructions string
		taskPrompt       string
	}

	var executions []execution
	failSeniorCheapAttempts := 3
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			if opts.Persona == "opencode" && opts.Model == cheapTestModel && opts.RoleInstructions == "Senior route guidance." && failSeniorCheapAttempts > 0 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
				}
				failSeniorCheapAttempts--
				return &harnessapi.TryResult{Completed: false, Summary: "simulated senior cheap-model failure"}, nil
			}

			executions = append(executions, execution{
				persona:          opts.Persona,
				roleInstructions: opts.RoleInstructions,
				taskPrompt:       opts.TaskPrompt,
			})
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{
		"opencode": exec,
		"codex":    exec,
	}

	oldHeadPull := headPullLap
	headPullCalls := 0
	headPullLap = func(context.Context, string) (laps.Lap, laps.QueueState, error) {
		headPullCalls++
		switch headPullCalls {
		case 1, 2:
			return laps.Lap{Title: "senior task", Description: "work senior item", Assignee: "SENIOR"}, laps.StateLap, nil
		case 3, 4:
			return laps.Lap{Title: "junior task", Description: "work junior item", Assignee: "JUNIOR"}, laps.StateLap, nil
		default:
			return laps.Lap{Title: "default task", Description: "work default item"}, laps.StateLap, nil
		}
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": []string{"cx:1"},
			"SENIOR":  []string{"op:dsf", "cx:1"},
			"JUNIOR":  []string{"cx:2"},
		},
		TargetIterations: 4,
		RetryBudget:      3,
		LapsEnabled:      true,
		Instructions:     "Base instructions.",
		Resolver:         cheapTestResolver,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	wantPersonas := []string{"codex", "codex", "codex", "codex"}
	wantRoleInstructions := []string{
		"Senior route guidance.",
		"Junior route guidance.",
		"Junior route guidance.",
		"",
	}
	wantPrompts := []string{
		"work senior item",
		"work junior item",
		"work junior item",
		"work default item",
	}

	if len(executions) != len(wantPersonas) {
		t.Fatalf("executions = %d, want %d", len(executions), len(wantPersonas))
	}
	for i := range executions {
		if executions[i].persona != wantPersonas[i] {
			t.Fatalf("execution %d persona = %q, want %q", i+1, executions[i].persona, wantPersonas[i])
		}
		if executions[i].roleInstructions != wantRoleInstructions[i] {
			t.Fatalf("execution %d role instructions = %q, want %q", i+1, executions[i].roleInstructions, wantRoleInstructions[i])
		}
		if executions[i].taskPrompt != wantPrompts[i] {
			t.Fatalf("execution %d task prompt = %q, want %q", i+1, executions[i].taskPrompt, wantPrompts[i])
		}
	}

	st, _ := r.resilience.GetState(ResilienceKey{Harness: "opencode", Model: cheapTestModel})
	if st != StatePaused {
		t.Fatalf("cheap model state = %s, want %s after simulated freeze", st, StatePaused)
	}
}

func TestRunnerTerminalQueueStatesEndRelay(t *testing.T) {
	tests := []struct {
		name       string
		state      laps.QueueState
		endReason  string
		logSnippet string
	}{
		{
			name:       "empty",
			state:      laps.StateEmpty,
			endReason:  relaycore.EndReasonQueueEmpty,
			logSnippet: "completed: laps queue empty",
		},
		{
			name:       "complete",
			state:      laps.StateComplete,
			endReason:  relaycore.EndReasonQueueComplete,
			logSnippet: "completed: laps queue complete",
		},
		{
			name:       "held",
			state:      laps.StateHeld,
			endReason:  relaycore.EndReasonQueueHeld,
			logSnippet: "stopped: head lap is held",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			rallyDir := store.RallyDir(workspaceDir)
			os.MkdirAll(rallyDir, 0o755)
			initRepo(t, workspaceDir)

			s := newTestStore(t, rallyDir)
			dataDir := t.TempDir()
			oldHeadPull := headPullLap
			headPullLap = func(context.Context, string) (laps.Lap, laps.QueueState, error) {
				return laps.NoLap, tt.state, nil
			}
			defer func() { headPullLap = oldHeadPull }()

			exec := &funcExecutor{
				fn: func(context.Context, harnessapi.RunOptions) (*harnessapi.TryResult, error) {
					t.Fatal("executor should not run for terminal queue state")
					return nil, nil
				},
			}
			r := NewRunner(s, Config{
				WorkspaceDir:     workspaceDir,
				DataDir:          dataDir,
				RouteSpecs:       map[string][]string{"default": {"cx:1"}},
				TargetIterations: 1,
				LapsEnabled:      true,
				Resolver:         testResolver,
			}, map[string]harnessapi.Executor{"codex": exec})

			if err := r.Run(context.Background()); err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			relays := s.AllRelays()
			if len(relays) != 1 {
				t.Fatalf("relays = %d, want 1", len(relays))
			}
			if relays[0].EndReason != tt.endReason {
				t.Fatalf("EndReason = %q, want %q", relays[0].EndReason, tt.endReason)
			}
			if relays[0].EndedAt == "" {
				t.Fatal("EndedAt is empty, want terminal relay")
			}

			logData, err := os.ReadFile(relayLogPath(dataDir, workspaceDir, relays[0].ID))
			if err != nil {
				t.Fatalf("read relay log: %v", err)
			}
			if !strings.Contains(string(logData), tt.logSnippet) {
				t.Fatalf("relay log = %q, want %q", string(logData), tt.logSnippet)
			}
		})
	}
}

func TestRunnerNoBackendUsesDefaultRouteAndFallbackPrompt(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedPersona string
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			receivedPersona = opts.Persona
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &harnessapi.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"codex": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		RouteSpecs:        map[string][]string{"default": []string{"cx:1"}},
		TargetIterations:  1,
		LapsEnabled:       false,
		FreeRunPromptFile: fallbackFile,
		Resolver:          testResolver,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedPersona != "codex" {
		t.Fatalf("persona = %q, want codex", receivedPersona)
	}
	if receivedTaskPrompt != "Fallback prompt content." {
		t.Fatalf("task prompt = %q, want fallback prompt", receivedTaskPrompt)
	}
}

func TestE2E_CheapRotationOpencodeGLMToKimi(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var modelsExecuted []string
	exec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			modelsExecuted = append(modelsExecuted, opts.Model)
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("change-%s.txt", opts.Model)))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:glm:1", "op:kimi:1"},
		},
		TargetIterations: 2,
		Resolver:         testResolver,
		TaskPrompt:       "rotate",
	}, map[string]harnessapi.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := exec.rotateCalls; len(got) != 1 || got[0] != "kimi" {
		t.Fatalf("RotateModel calls = %v, want [kimi]", got)
	}
	if got, want := modelsExecuted, []string{"glm", "kimi"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}
}

func TestE2E_ClaudeRateLimitWaitAndResume(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var capturedSessionIDs []string
	var sleptDurations []time.Duration
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt == 1 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("sending to claude...\nerror 429 Too Many Requests\nretry-after: 1\n"), 0o644)
				}
				return &harnessapi.TryResult{Completed: false, Summary: "rate limit hit", SessionID: "sess-rate"}, nil
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
		RetryBudget:      3,
	}, executors)
	r.sleepFunc = func(d time.Duration) { sleptDurations = append(sleptDurations, d) }

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(capturedSessionIDs) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(capturedSessionIDs))
	}
	if capturedSessionIDs[0] != "" {
		t.Fatalf("attempt 1 ResumeSessionID = %q, want empty", capturedSessionIDs[0])
	}
	if capturedSessionIDs[1] != "sess-rate" {
		t.Fatalf("attempt 2 ResumeSessionID = %q, want sess-rate", capturedSessionIDs[1])
	}
	if len(sleptDurations) != 1 || sleptDurations[0] != 1*time.Second {
		t.Fatalf("slept durations = %v, want [1s]", sleptDurations)
	}
}

func TestE2E_ErrorPatternRotateAdvancesRoute(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var executed []string
	opencodeExec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			executed = append(executed, "opencode:"+opts.Model)
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("some output\nerror: API bad request from provider\n"), 0o644)
			}
			return &harnessapi.TryResult{Completed: false, Summary: "rotate"}, nil
		},
	}
	codexExec := &funcExecutor{
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			executed = append(executed, "codex:"+opts.Model)
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": {"op:glm:1", "cx:gpt-5:1"},
		},
		TargetIterations: 1,
		Resolver:         testResolver,
		TaskPrompt:       "rotate",
	}, map[string]harnessapi.Executor{
		"opencode": opencodeExec,
		"codex":    codexExec,
	})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got, want := executed, []string{"opencode:glm", "codex:gpt-5"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed route = %v, want %v", got, want)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("tries = %d, want 2", len(tries))
	}
	if tries[0].AttemptNumber != 1 || tries[1].AttemptNumber != 1 {
		t.Fatalf("attempt numbers = [%d %d], want [1 1] after route advance", tries[0].AttemptNumber, tries[1].AttemptNumber)
	}
}
