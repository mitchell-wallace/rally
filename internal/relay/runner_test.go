package relay

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

type funcExecutor struct {
	fn              func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error)
	resumeSupported bool
	rotateSupported bool
	probeSupported  bool
	probeFn         func(context.Context) (bool, error)
	rotateErr       error
	rotateCalls     []string
}

func (f *funcExecutor) Execute(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
	return f.fn(ctx, opts)
}

func (f *funcExecutor) ResumeSupported() bool        { return f.resumeSupported }
func (f *funcExecutor) RotateSupported() bool        { return f.rotateSupported }
func (f *funcExecutor) LivenessProbeSupported() bool { return f.probeSupported }
func (f *funcExecutor) CharsPerToken() float64       { return 0 }
func (f *funcExecutor) RotateModel(model string) error {
	f.rotateCalls = append(f.rotateCalls, model)
	if !f.rotateSupported {
		return fmt.Errorf("rotate not supported by func executor")
	}
	return f.rotateErr
}
func (f *funcExecutor) ProbeLiveness(ctx context.Context) (bool, error) {
	if f.probeFn != nil {
		return f.probeFn(ctx)
	}
	return false, fmt.Errorf("liveness probe not supported by func executor")
}

type fakeFreezeController struct {
	check func(context.Context) (bool, error)
}

func (f *fakeFreezeController) SetProcessGroupID(int) {}

func (f *fakeFreezeController) Check(ctx context.Context) (bool, error) {
	if f.check == nil {
		return false, nil
	}
	return f.check(ctx)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.name", "Rally Test")
	runGit(t, dir, "config", "user.email", "rally@example.com")
	// Exclude only rally's ephemeral files from git status.
	// Rally's JSONL state files are now committed as durable git-backed state.
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	os.WriteFile(excludePath, []byte(".rally/current_task.md\n.rally/relays/\n"), 0o644)
}

func newTestStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testResolver(spec string) (agent.ResolvedAgent, error) {
	aliases := map[string]string{
		"cc": "claude", "claude": "claude",
		"cx": "codex", "codex": "codex",
		"ge": "gemini", "gemini": "gemini",
		"op": "opencode", "opencode": "opencode",
	}
	parts := strings.SplitN(spec, ":", 2)
	harness, ok := aliases[parts[0]]
	if !ok {
		return agent.ResolvedAgent{}, fmt.Errorf("unknown agent alias %q", parts[0])
	}
	if len(parts) == 2 {
		if _, err := strconv.Atoi(parts[1]); err == nil {
			return agent.ResolvedAgent{Harness: harness}, nil
		}
		return agent.ResolvedAgent{Harness: harness, Model: parts[1]}, nil
	}
	return agent.ResolvedAgent{Harness: harness}, nil
}

func TestInstructionsPassedToExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	var receivedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			receivedTaskPrompt = opts.TaskPrompt
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		Instructions:     "Always write tests.",
		TaskPrompt:       "Build user auth.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Always write tests." {
		t.Errorf("expected instructions 'Always write tests.', got %q", receivedInstructions)
	}
	if receivedTaskPrompt != "Build user auth." {
		t.Errorf("expected task prompt 'Build user auth.', got %q", receivedTaskPrompt)
	}
}

func TestLapsHeadTaskPassedToExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedTaskName string
	var receivedRequirements string
	var receivedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskName = opts.TaskName
			receivedRequirements = opts.TaskRequirements
			receivedTaskPrompt = opts.TaskPrompt
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{
			ID:          "lap-42",
			Title:       "Implement auth",
			Description: "Add login and session handling.",
			Assignee:    "alice",
		}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		TaskPrompt:       "fallback prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskName != "Implement auth" {
		t.Errorf("expected task name from lap, got %q", receivedTaskName)
	}
	if receivedTaskPrompt != "Add login and session handling." {
		t.Errorf("expected task prompt from lap, got %q", receivedTaskPrompt)
	}
	if receivedRequirements != "Lap ID: lap-42\nAssignee: alice" {
		t.Errorf("expected lap requirements, got %q", receivedRequirements)
	}
}

func TestLapsHeadTaskFallsBackToConfiguredPrompt(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedTaskName string
	var receivedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskName = opts.TaskName
			receivedTaskPrompt = opts.TaskPrompt
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.NoLap, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		TaskPrompt:       "fallback prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskName != "relay run" {
		t.Errorf("expected fallback task name, got %q", receivedTaskName)
	}
	if receivedTaskPrompt != "fallback prompt" {
		t.Errorf("expected fallback task prompt, got %q", receivedTaskPrompt)
	}
}
func TestAgentCyclingDeterminism(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var agents []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			agents = append(agents, opts.Persona)
			changeCounter++
			// Append unique content so each try produces a distinct change.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec, "codex": exec}

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
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var executedModels []string
	exec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			f, _ := os.OpenFile(filepath.Join(workspaceDir, fmt.Sprintf("change-%d.txt", len(executedModels))), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString(opts.Model)
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})

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
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	opExec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "opencode.txt"), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString("opencode")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}
	codexExec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "codex.txt"), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString("codex")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
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
	}, map[string]agent.Executor{
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
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	dataDir := t.TempDir()
	var executedModels []string
	exec := &funcExecutor{
		rotateSupported: true,
		rotateErr:       fmt.Errorf("rotate failed"),
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executedModels = append(executedModels, opts.Model)
			f, _ := os.OpenFile(filepath.Join(workspaceDir, fmt.Sprintf("change-%d.txt", len(executedModels))), os.O_CREATE|os.O_WRONLY, 0o644)
			f.WriteString(opts.Model)
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if got := exec.rotateCalls; len(got) != 1 || got[0] != "model-b" {
		t.Fatalf("RotateModel calls = %v, want [model-b]", got)
	}
	if got, want := executedModels, []string{"model-a", "model-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("executed models = %v, want %v", got, want)
	}

	logData, err := os.ReadFile(filepath.Join(workspaceDir, ".rally", "relays", "relay-1.log"))
	if err != nil {
		t.Fatalf("read relay log: %v", err)
	}
	if !strings.Contains(string(logData), "rotate fallback for opencode: rotate failed") {
		t.Fatalf("relay log = %q, want rotate fallback message", string(logData))
	}
}

func TestFilesChangedCountUsesCommitDiff(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "one.txt"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, "two.txt"), []byte("two\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	before := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	os.WriteFile(filepath.Join(workspaceDir, "one.txt"), []byte("one changed\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, "two.txt"), []byte("two changed\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "change two files")

	after := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	if got := r.filesChangedCount(nil, before, after, after); got != 2 {
		t.Fatalf("expected 2 changed files from commit diff, got %d", got)
	}
}

func TestRetryWithinRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt < 3 {
				return &agent.TryResult{Completed: false, Summary: "fail"}, nil
			}
			// Create a file so the successful try is not a no-op failure.
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("success-%d.txt", attempt)))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

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
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var capturedSessionIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt < 3 {
				f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("attempt-%d.txt", attempt)))
				f.WriteString("changed")
				f.Close()
				return &agent.TryResult{Completed: false, Summary: "fail", SessionID: fmt.Sprintf("session-%d", attempt)}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

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
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	freezeCh := make(chan struct{})
	attempt := 0
	var resumeIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			resumeIDs = append(resumeIDs, opts.ResumeSessionID)
			if attempt == 1 {
				<-freezeCh
				return &agent.TryResult{Completed: false, Summary: "freeze", SessionID: "sess-freeze"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, map[string]agent.Executor{"claude": exec})

	controllerCount := 0
	r.freezeControllerFactory = func(logPath string) reliability.FreezeController {
		_ = logPath
		controllerCount++
		if controllerCount == 1 {
			triggered := false
			return &fakeFreezeController{
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
		return &fakeFreezeController{}
	}

	oldInterval := freezeCheckInterval
	freezeCheckInterval = time.Millisecond
	defer func() { freezeCheckInterval = oldInterval }()

	freezeCalls := 0
	recoveredCalls := 0
	success, addressed, interrupted, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "claude"},
		runTask{Name: "relay run", Prompt: "freeze test"},
		nil,
		nil,
		false,
		func() { freezeCalls++ },
		func() { recoveredCalls++ },
		io.Discard,
	)
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

func TestBuildLivenessProbeDisabledByConfig(t *testing.T) {
	r := &Runner{cfg: Config{LivenessProbe: false}}
	exec := &funcExecutor{probeSupported: true}
	if probe := r.buildLivenessProbe(exec); probe != nil {
		t.Fatal("expected liveness probe to be disabled by config")
	}
}

func TestBuildLivenessProbeSkipsUnsupportedAdapter(t *testing.T) {
	r := &Runner{cfg: Config{LivenessProbe: true}}
	exec := &funcExecutor{probeSupported: false}
	if probe := r.buildLivenessProbe(exec); probe != nil {
		t.Fatal("expected unsupported adapter to skip liveness probe")
	}
}

func TestBuildLivenessProbeEnabledForSupportedAdapter(t *testing.T) {
	r := &Runner{cfg: Config{LivenessProbe: true}}
	exec := &funcExecutor{probeSupported: true}
	if probe := r.buildLivenessProbe(exec); probe == nil {
		t.Fatal("expected supported adapter to build liveness probe")
	}
}

func TestResumeRetryPreservesRunState(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
					t.Errorf("RecordLap error: %v", err)
				}
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &agent.TryResult{Completed: false, Summary: "fail", SessionID: "sess-1"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

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
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var capturedSessionIDs []string
	exec := &funcExecutor{
		resumeSupported: false,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt == 1 {
				if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
					t.Errorf("RecordLap error: %v", err)
				}
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &agent.TryResult{Completed: false, Summary: "fail", SessionID: "sess-1"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

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
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var runStateAtAttempt2 *progress.RunState
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &agent.TryResult{Completed: false, Summary: "crashed mid-handoff", SessionID: "sess-crash"}, nil
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
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

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
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	var runStateAtAttempt2 *progress.RunState
	exec := &funcExecutor{
		resumeSupported: false,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := progress.SetHandoff(workspaceDir); err != nil {
					t.Errorf("SetHandoff error: %v", err)
				}
				return &agent.TryResult{Completed: false, Summary: "crashed mid-handoff", SessionID: "sess-crash"}, nil
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
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

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

func TestRetryExhaustionTriggersPause(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	tries := s.AllTries()
	if len(tries) < 3 {
		t.Fatalf("got %d tries, want at least 3", len(tries))
	}

	status := s.GetAgentStatus("claude")
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

func TestGracefulStop(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			time.Sleep(50 * time.Millisecond)
			changeCounter++
			// Append unique content so the try is not a no-op failure.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "stop %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 5,
	}, executors)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	r.RequestStop()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop in time")
	}

	relays := s.AllRelays()
	if len(relays) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relays))
	}
	if relays[0].CompletedIterations >= 5 {
		t.Fatalf("expected < 5 iterations after stop, got %d", relays[0].CompletedIterations)
	}
}

func TestMessageConsumptionPerRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "msg1", Status: "pending", Position: 1})
	s.AddMessage(store.MessageRecord{ID: 2, Body: "msg2", Status: "pending", Position: 2})

	addressed := false
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			addressed = true
			msgAddr := true
			changeCounter++
			// Append unique content so the try is not a no-op failure.
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "msg %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if !addressed {
		t.Fatal("expected message to be addressed")
	}
	msgs := s.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	pending := 0
	for _, m := range msgs {
		if m.Status == "pending" {
			pending++
		}
	}
	if pending != 0 {
		t.Fatalf("expected 0 pending messages, got %d", pending)
	}
}

func TestCommitHashTracking_AgentCommitted(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Copy fixture project into workspace
	CopyFixtureProject(t, workspaceDir)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	diffPath, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "diffs", "add-feature.diff"))
	outputPath, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "outputs", "success.json"))
	exec := &agent.FixtureExecutor{
		DiffPath:   diffPath,
		OutputPath: outputPath,
		Dir:        workspaceDir,
	}
	executors := map[string]agent.Executor{"claude": exec}

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
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected agent commit hash")
	}
}

func TestCommitHashTracking_AutoCommitted(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			// Create a file but don't commit it
			f, err := os.Create(filepath.Join(workspaceDir, "auto.txt"))
			if err != nil {
				return nil, err
			}
			f.WriteString("auto")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

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
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	if tries[0].CommitHash == "" {
		t.Fatal("expected auto-commit hash")
	}
}

func TestCommitHashTracking_NoChanges(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 2,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	// No-op tries (no changes + <3min) are treated as failures and retried up to 3x.
	// With the fix, failed runs don't count toward target, so the relay runs
	// hourly retries after pausing. Initial run has 3 tries; hourly retries have 1 each.
	tries := s.AllTries()
	if len(tries) <= 3 {
		t.Fatalf("expected > 3 tries (initial 3 + hourly retries), got %d", len(tries))
	}
	// First 3 tries should have no commit hash
	for i := 0; i < 3; i++ {
		if tries[i].CommitHash != "" {
			t.Fatalf("try %d expected no commit hash, got %q", i, tries[i].CommitHash)
		}
	}
}

func TestRelayScopedMessageIncludedInAllRuns(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})

	var relayMsgsSeen []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			relayMsgsSeen = append(relayMsgsSeen, opts.RelayMessage)
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 3,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if len(relayMsgsSeen) != 3 {
		t.Fatalf("expected relay message in 3 runs, got %d", len(relayMsgsSeen))
	}
	for i, msg := range relayMsgsSeen {
		if msg != "relay-msg" {
			t.Fatalf("run %d relay message = %q, want 'relay-msg'", i, msg)
		}
	}
}

func TestRelayScopedMessageAddressed(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})

	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			msgAddr := true
			return &agent.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	relay := s.AllRelays()
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relay))
	}
	foundRelayMsg := false
	for _, id := range relay[0].ConsumedMessageIDs {
		if id == 1 {
			foundRelayMsg = true
		}
	}
	if !foundRelayMsg {
		t.Fatal("expected relay message ID 1 in ConsumedMessageIDs")
	}

	msgs := s.GetMessages()
	for _, m := range msgs {
		if m.ID == 1 && m.Status != "addressed" {
			t.Fatalf("expected relay message addressed, got %s", m.Status)
		}
	}
}

func TestCombinedRelayAndRunScopedMessages(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	s.AddMessage(store.MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})
	s.AddMessage(store.MessageRecord{ID: 2, Body: "run-msg-1", Status: "pending", Position: 2})
	s.AddMessage(store.MessageRecord{ID: 3, Body: "run-msg-2", Status: "pending", Position: 3})

	var relayMsgsSeen []string
	var inboxMsgsSeen []string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			relayMsgsSeen = append(relayMsgsSeen, opts.RelayMessage)
			inboxMsgsSeen = append(inboxMsgsSeen, opts.InboxMessage)
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			msgAddr := true
			return &agent.TryResult{Completed: true, MessageAddressed: &msgAddr}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 2,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// Relay message seen in both runs
	if len(relayMsgsSeen) != 2 {
		t.Fatalf("expected relay message in 2 runs, got %d", len(relayMsgsSeen))
	}
	for i, msg := range relayMsgsSeen {
		if msg != "relay-msg" {
			t.Fatalf("run %d relay message = %q, want 'relay-msg'", i, msg)
		}
	}

	// Inbox messages: each run gets a different run-scoped message
	if len(inboxMsgsSeen) != 2 {
		t.Fatalf("expected inbox message in 2 runs, got %d", len(inboxMsgsSeen))
	}
	if inboxMsgsSeen[0] != "run-msg-1" {
		t.Fatalf("run 1 inbox = %q, want 'run-msg-1'", inboxMsgsSeen[0])
	}
	if inboxMsgsSeen[1] != "run-msg-2" {
		t.Fatalf("run 2 inbox = %q, want 'run-msg-2'", inboxMsgsSeen[1])
	}

	// All messages should be addressed
	msgs := s.GetMessages()
	for _, m := range msgs {
		if m.Status != "addressed" {
			t.Fatalf("expected message %d to be addressed, got %s", m.ID, m.Status)
		}
	}

	// Relay ConsumedMessageIDs should have all 3 message IDs
	relay := s.AllRelays()
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay, got %d", len(relay))
	}
	if len(relay[0].ConsumedMessageIDs) != 3 {
		t.Fatalf("expected 3 consumed message IDs, got %v", relay[0].ConsumedMessageIDs)
	}
}

func TestFreezeCascade(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, executors)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.Run(ctx)

	// Verify agent is paused after 3 retries exhausted
	st, _ := NewResilience(s).getState("claude")
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
		if err := resilience.RecordHourlyFailure("claude", 1); err != nil {
			t.Fatalf("RecordHourlyFailure %d failed: %v", i+1, err)
		}
	}

	// Verify agent is now frozen
	st, _ = resilience.getState("claude")
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after 5 hourly retries, got %s", st)
	}

	// Verify a "frozen" event was recorded
	events := s.GetAgentStatus("claude")
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
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.PauseAgent("claude", 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	st, _ := resilience.getState("claude")
	if st != StatePaused {
		t.Fatalf("expected StatePaused after pause, got %s", st)
	}

	if err := resilience.UnpauseAgent("claude", 1); err != nil {
		t.Fatalf("UnpauseAgent failed: %v", err)
	}

	st, _ = resilience.getState("claude")
	if st != StateActive {
		t.Fatalf("expected StateActive after unpause, got %s", st)
	}
}

func TestFailedRunDoesNotCountIteration(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
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

	st, _ := NewResilience(s).getState("claude")
	if st != StateFrozen {
		t.Fatalf("expected agent frozen after hourly retry exhaustion, got %s", st)
	}
}

func TestHourlyRetryWithOtherAgentActive(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)

	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resilience := &Resilience{
		Store:         s,
		PauseDuration: time.Hour,
		NowFunc:       func() time.Time { return baseTime },
	}

	if err := resilience.PauseAgent("claude", 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	resilience.NowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }

	mix, err := ParseAgentMix([]string{"cc:2", "cx:1"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
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

func TestAllAgentsFrozenEndsRelay(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.FreezeAgent("claude", 1); err != nil {
		t.Fatalf("FreezeAgent claude failed: %v", err)
	}
	if err := resilience.FreezeAgent("codex", 1); err != nil {
		t.Fatalf("FreezeAgent codex failed: %v", err)
	}

	mix, err := ParseAgentMix([]string{"cc:1", "cx:1"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	_, _, _, err = resilience.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error from SelectActiveAgent")
	}
	if err.Error() != "all agents frozen" {
		t.Fatalf("expected 'all agents frozen' error, got %q", err.Error())
	}
}

func TestStubEntryOnIncompleteRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "agent stopped early"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
	}, executors)
	r.resilience = &Resilience{
		Store:                     s,
		PauseDuration:             time.Millisecond,
		HourlyRetriesBeforeFreeze: 0,
		NowFunc:                   time.Now,
	}

	_ = r.Run(context.Background())

	pl, err := progress.LoadProgress(workspaceDir)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) == 0 {
		t.Fatal("expected at least one stub entry in progress.yaml")
	}
	found := false
	for _, entry := range pl.RecentRuns {
		if entry.Summary == "agent stopped early" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a stub entry with summary 'agent stopped early', got %v", pl.RecentRuns)
	}

	if _, err := os.Stat(progress.RunStatePath(workspaceDir)); !os.IsNotExist(err) {
		t.Fatal("expected run-state.json to be cleared")
	}
}

func TestAgentMixNamedModels(t *testing.T) {
	mix, err := ParseAgentMix([]string{"op:z", "cc:opus"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 2 {
		t.Fatalf("expected 2 cycle entries, got %d", len(mix.Cycle))
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[0] = %+v, want {opencode z}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "claude", Model: "opus"}) {
		t.Fatalf("cycle[1] = %+v, want {claude opus}", mix.Cycle[1])
	}
	if mix.Weights["opencode"] != 1 {
		t.Fatalf("weights[opencode] = %d, want 1", mix.Weights["opencode"])
	}
	if mix.Weights["claude"] != 1 {
		t.Fatalf("weights[claude] = %d, want 1", mix.Weights["claude"])
	}
	if mix.Label != "op:z cc:opus" {
		t.Fatalf("label = %q, want %q", mix.Label, "op:z cc:opus")
	}
}

func TestAgentMixMixedForms(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc:2", "op:z"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 3 {
		t.Fatalf("expected 3 cycle entries, got %d: %+v", len(mix.Cycle), mix.Cycle)
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[1] = %+v, want {claude}", mix.Cycle[1])
	}
	if mix.Cycle[2] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[2] = %+v, want {opencode z}", mix.Cycle[2])
	}
	if mix.Label != "claude claude op:z" {
		t.Fatalf("label = %q, want %q", mix.Label, "claude claude op:z")
	}
}

func TestAgentMixMixedNamedAndWeighted(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc:2", "op:z", "cc:sonnet"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 2 {
		t.Fatalf("expected 2 cycle entries (1 named opencode + 1 named claude), got %d: %+v", len(mix.Cycle), mix.Cycle)
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude", Model: "sonnet"}) {
		t.Fatalf("cycle[0] = %+v, want {claude sonnet}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[1] = %+v, want {opencode z}", mix.Cycle[1])
	}
}

func TestResumeFromStoredLabelWithNamedModels(t *testing.T) {
	resolver := Resolver(testResolver)

	mix1, err := ParseAgentMix([]string{"op:z", "cc:opus"}, resolver)
	if err != nil {
		t.Fatalf("initial ParseAgentMix failed: %v", err)
	}

	mix2, err := ParseAgentMix(strings.Fields(mix1.Label), resolver)
	if err != nil {
		t.Fatalf("re-parse of label %q failed: %v", mix1.Label, err)
	}

	if len(mix1.Cycle) != len(mix2.Cycle) {
		t.Fatalf("cycle length mismatch: %d vs %d", len(mix1.Cycle), len(mix2.Cycle))
	}
	for i := range mix1.Cycle {
		if mix1.Cycle[i] != mix2.Cycle[i] {
			t.Fatalf("cycle[%d] mismatch: %+v vs %+v", i, mix1.Cycle[i], mix2.Cycle[i])
		}
	}
	if mix1.Label != mix2.Label {
		t.Fatalf("label mismatch: %q vs %q", mix1.Label, mix2.Label)
	}
}

func TestResumeFromStoredLabelWithMixedForms(t *testing.T) {
	resolver := Resolver(testResolver)

	mix1, err := ParseAgentMix([]string{"cc:2", "op:z"}, resolver)
	if err != nil {
		t.Fatalf("initial ParseAgentMix failed: %v", err)
	}

	mix2, err := ParseAgentMix(strings.Fields(mix1.Label), resolver)
	if err != nil {
		t.Fatalf("re-parse of label %q failed: %v", mix1.Label, err)
	}

	if len(mix1.Cycle) != len(mix2.Cycle) {
		t.Fatalf("cycle length mismatch: %d vs %d", len(mix1.Cycle), len(mix2.Cycle))
	}
	for i := range mix1.Cycle {
		if mix1.Cycle[i] != mix2.Cycle[i] {
			t.Fatalf("cycle[%d] mismatch: %+v vs %+v", i, mix1.Cycle[i], mix2.Cycle[i])
		}
	}
}

// TestResumeRoundTripWithRealResolver uses the real config resolver to catch
// the case where resolved model strings look like identifier-like keys (e.g.
// "claude-opus-4-7") and must not be stored literally in the label.
func TestResumeRoundTripWithRealResolver(t *testing.T) {
	cfg := config.V2Config{
		Harnesses: map[string]*config.HarnessConfig{
			"cc": {Models: map[string]string{
				"opus": "claude-opus-4-7",
			}},
			"op": {Models: map[string]string{
				"z": "zai-coding-plan/glm-5.1",
			}},
		},
	}
	resolver := Resolver(cfg.ResolveAgent)

	mix1, err := ParseAgentMix([]string{"cc:opus", "op:z"}, resolver)
	if err != nil {
		t.Fatalf("initial ParseAgentMix failed: %v", err)
	}

	mix2, err := ParseAgentMix(strings.Fields(mix1.Label), resolver)
	if err != nil {
		t.Fatalf("re-parse of label %q failed: %v — label must store short alias, not resolved model string", mix1.Label, err)
	}

	if len(mix1.Cycle) != len(mix2.Cycle) {
		t.Fatalf("cycle length mismatch after resume: %d vs %d", len(mix1.Cycle), len(mix2.Cycle))
	}
	for i := range mix1.Cycle {
		if mix1.Cycle[i] != mix2.Cycle[i] {
			t.Fatalf("cycle[%d] mismatch after resume: %+v vs %+v", i, mix1.Cycle[i], mix2.Cycle[i])
		}
	}
	if mix1.Label != mix2.Label {
		t.Fatalf("label changed after resume: %q -> %q", mix1.Label, mix2.Label)
	}
}

func TestPerHarnessPauseSkipsAllModels(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	if err := resilience.PauseAgent("claude", 1); err != nil {
		t.Fatalf("PauseAgent failed: %v", err)
	}

	mix, err := ParseAgentMix([]string{"cc:opus", "cc:sonnet"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}

	if len(mix.Cycle) != 2 {
		t.Fatalf("expected 2 cycle entries, got %d", len(mix.Cycle))
	}
	if mix.Cycle[0].Model != "opus" || mix.Cycle[1].Model != "sonnet" {
		t.Fatalf("expected opus+sonnet models, got %+v", mix.Cycle)
	}

	_, _, _, err = resilience.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error when all agents are paused")
	}
	if err.Error() != "all agents paused" {
		t.Fatalf("expected 'all agents paused' error, got %q", err.Error())
	}
}

func TestParseAgentMixThirdColonSegmentRejected(t *testing.T) {
	_, err := ParseAgentMix([]string{"cc:opus:2"}, Resolver(testResolver))
	if err == nil {
		t.Fatal("expected error for third colon segment")
	}
	if !strings.Contains(err.Error(), "weight-on-named-model") {
		t.Fatalf("error = %q, want mention of weight-on-named-model", err.Error())
	}
}

func TestParseAgentMixUnresolvedModelError(t *testing.T) {
	strictResolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := testResolver(spec)
		if err != nil {
			return ra, err
		}
		if ra.Model != "" && ra.Model != "z" && ra.Model != "opus" && ra.Model != "sonnet" {
			return agent.ResolvedAgent{}, fmt.Errorf("unknown model %q for harness %q", ra.Model, ra.Harness)
		}
		return ra, nil
	}
	_, err := ParseAgentMix([]string{"cc:unknown_model"}, Resolver(strictResolver))
	if err == nil {
		t.Fatal("expected error for unresolved model name")
	}
	if !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("error = %q, want mention of unknown model", err.Error())
	}
}

func TestParseAgentMixUnknownHarnessError(t *testing.T) {
	_, err := ParseAgentMix([]string{"unknown:foo"}, Resolver(testResolver))
	if err == nil {
		t.Fatal("expected error for unknown harness")
	}
	if !strings.Contains(err.Error(), "unknown agent alias") {
		t.Fatalf("error = %q, want mention of unknown agent alias", err.Error())
	}
}

func TestParseAgentMixBareAlias(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}
	if len(mix.Cycle) != 1 {
		t.Fatalf("expected 1 cycle entry, got %d", len(mix.Cycle))
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Label != "claude" {
		t.Fatalf("label = %q, want %q", mix.Label, "claude")
	}
}

func TestParseAgentMixAllFormsCombined(t *testing.T) {
	mix, err := ParseAgentMix([]string{"cc", "cx:2", "op:z"}, Resolver(testResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix failed: %v", err)
	}
	if len(mix.Cycle) != 4 {
		t.Fatalf("expected 4 cycle entries, got %d: %+v", len(mix.Cycle), mix.Cycle)
	}
	if mix.Cycle[0] != (agent.ResolvedAgent{Harness: "claude"}) {
		t.Fatalf("cycle[0] = %+v, want {claude}", mix.Cycle[0])
	}
	if mix.Cycle[1] != (agent.ResolvedAgent{Harness: "codex"}) {
		t.Fatalf("cycle[1] = %+v, want {codex}", mix.Cycle[1])
	}
	if mix.Cycle[2] != (agent.ResolvedAgent{Harness: "codex"}) {
		t.Fatalf("cycle[2] = %+v, want {codex}", mix.Cycle[2])
	}
	if mix.Cycle[3] != (agent.ResolvedAgent{Harness: "opencode", Model: "z"}) {
		t.Fatalf("cycle[3] = %+v, want {opencode z}", mix.Cycle[3])
	}
}

func TestLapsInstructionsFileUsed(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	instructionsFile := filepath.Join(workspaceDir, "custom_laps.md")
	os.WriteFile(instructionsFile, []byte("Custom laps instructions."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:         workspaceDir,
		DataDir:              t.TempDir(),
		AgentMixSpecs:        []string{"cc:1"},
		TargetIterations:     1,
		LapsEnabled:          true,
		Instructions:         "Default instructions.",
		LapsInstructionsFile: instructionsFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Custom laps instructions." {
		t.Errorf("expected laps instructions, got %q", receivedInstructions)
	}
}

func TestLapsInstructionsFileFallsBackToDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:         workspaceDir,
		DataDir:              t.TempDir(),
		AgentMixSpecs:        []string{"cc:1"},
		TargetIterations:     1,
		LapsEnabled:          true,
		Instructions:         "Default instructions.",
		LapsInstructionsFile: filepath.Join(workspaceDir, "nonexistent.md"),
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Default instructions." {
		t.Errorf("expected default instructions when laps file missing, got %q", receivedInstructions)
	}
}

func TestLapsInstructionsNotUsedInNoBackendMode(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	instructionsFile := filepath.Join(workspaceDir, "custom_laps.md")
	os.WriteFile(instructionsFile, []byte("Custom laps instructions."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:         workspaceDir,
		DataDir:              t.TempDir(),
		AgentMixSpecs:        []string{"cc:1"},
		TargetIterations:     1,
		LapsEnabled:          false,
		Instructions:         "Default instructions.",
		TaskPrompt:           "Do some work.",
		LapsInstructionsFile: instructionsFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Default instructions." {
		t.Errorf("expected default instructions in no-backend mode, got %q", receivedInstructions)
	}
}

func TestLapsInstructionsUnconfiguredUsesDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedInstructions = opts.Instructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		Instructions:     "Default instructions.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedInstructions != "Default instructions." {
		t.Errorf("expected default instructions when no laps file configured, got %q", receivedInstructions)
	}
}

func TestRoleInstructionsLoadedForAssignee(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	agentsDir := filepath.Join(rallyDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	if err := os.WriteFile(filepath.Join(agentsDir, "alice.md"), []byte("Role-specific guidance."), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)
	var receivedRoleInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedRoleInstructions = opts.RoleInstructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work", Assignee: "ALICE"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		Instructions:     "Default instructions.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedRoleInstructions != "Role-specific guidance." {
		t.Fatalf("role instructions = %q, want %q", receivedRoleInstructions, "Role-specific guidance.")
	}
}

func TestRoleInstructionsMissingFileIsSilent(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedRoleInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedRoleInstructions = opts.RoleInstructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.Lap{Title: "task", Description: "do work", Assignee: "missing"}, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      true,
		Instructions:     "Default instructions.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedRoleInstructions != "" {
		t.Fatalf("role instructions = %q, want empty string", receivedRoleInstructions)
	}
}

func TestRoleInstructionsSkippedInNoBackendMode(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	agentsDir := filepath.Join(rallyDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	if err := os.WriteFile(filepath.Join(agentsDir, "alice.md"), []byte("Role-specific guidance."), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTestStore(t, rallyDir)
	var receivedRoleInstructions string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedRoleInstructions = opts.RoleInstructions
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      false,
		Instructions:     "Default instructions.",
		TaskPrompt:       "Do some work.",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedRoleInstructions != "" {
		t.Fatalf("role instructions = %q, want empty string", receivedRoleInstructions)
	}
}

func TestFallbackInstructionsUsedInNoBackendMode(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:             workspaceDir,
		DataDir:                  t.TempDir(),
		AgentMixSpecs:            []string{"cc:1"},
		TargetIterations:         1,
		LapsEnabled:              false,
		FallbackInstructionsFile: fallbackFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != "Fallback prompt content." {
		t.Errorf("expected fallback file content as task prompt, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsIgnoredWhenCLIPromptProvided(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:             workspaceDir,
		DataDir:                  t.TempDir(),
		AgentMixSpecs:            []string{"cc:1"},
		TargetIterations:         1,
		LapsEnabled:              false,
		TaskPrompt:               "CLI prompt",
		FallbackInstructionsFile: fallbackFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != "CLI prompt" {
		t.Errorf("expected CLI prompt to take precedence over fallback, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsIgnoredInLapsMode(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		return laps.NoLap, nil
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir:             workspaceDir,
		DataDir:                  t.TempDir(),
		AgentMixSpecs:            []string{"cc:1"},
		TargetIterations:         1,
		LapsEnabled:              true,
		TaskPrompt:               "configured prompt",
		FallbackInstructionsFile: fallbackFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != "configured prompt" {
		t.Errorf("expected configured prompt, fallback should be ignored in laps mode, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsMissingFileUsesBuiltInDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:             workspaceDir,
		DataDir:                  t.TempDir(),
		AgentMixSpecs:            []string{"cc:1"},
		TargetIterations:         1,
		LapsEnabled:              false,
		FallbackInstructionsFile: filepath.Join(workspaceDir, "nonexistent.md"),
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != builtInDefaultFallback {
		t.Errorf("expected built-in default fallback, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsUnconfiguredUsesBuiltInDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		LapsEnabled:      false,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != builtInDefaultFallback {
		t.Errorf("expected built-in default fallback, got %q", receivedTaskPrompt)
	}
}

func TestRunnerRouteIntegration_AssigneesQuotasFreezeAndRoleFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
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
	failSeniorClaudeAttempts := 3
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.Persona == "claude" && opts.RoleInstructions == "Senior route guidance." && failSeniorClaudeAttempts > 0 {
				failSeniorClaudeAttempts--
				return &agent.TryResult{Completed: false, Summary: "rate limit"}, nil
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
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}
	executors := map[string]agent.Executor{
		"claude": exec,
		"codex":  exec,
	}

	oldHeadPull := headPullLap
	headPullCalls := 0
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		headPullCalls++
		switch headPullCalls {
		case 1, 2:
			return laps.Lap{Title: "senior task", Description: "work senior item", Assignee: "SENIOR"}, nil
		case 3, 4:
			return laps.Lap{Title: "junior task", Description: "work junior item", Assignee: "JUNIOR"}, nil
		default:
			return laps.Lap{Title: "default task", Description: "work default item"}, nil
		}
	}
	defer func() { headPullLap = oldHeadPull }()

	r := NewRunner(s, Config{
		WorkspaceDir: workspaceDir,
		DataDir:      t.TempDir(),
		RouteSpecs: map[string][]string{
			"default": []string{"cx:1"},
			"SENIOR":  []string{"cc:1", "cx:1"},
			"JUNIOR":  []string{"cx:2"},
		},
		TargetIterations: 4,
		LapsEnabled:      true,
		Instructions:     "Base instructions.",
		Resolver:         testResolver,
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

	st, _ := r.resilience.getState("claude")
	if st != StatePaused {
		t.Fatalf("claude state = %s, want %s after simulated freeze", st, StatePaused)
	}
}

func TestRunnerNoBackendUsesDefaultRouteAndFallbackPrompt(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Fallback prompt content."), 0o644)

	s := newTestStore(t, rallyDir)
	var receivedPersona string
	var receivedTaskPrompt string
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			receivedPersona = opts.Persona
			receivedTaskPrompt = opts.TaskPrompt
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change\n")
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"codex": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:             workspaceDir,
		DataDir:                  t.TempDir(),
		RouteSpecs:               map[string][]string{"default": []string{"cx:1"}},
		TargetIterations:         1,
		LapsEnabled:              false,
		FallbackInstructionsFile: fallbackFile,
		Resolver:                 testResolver,
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

// --- Phase 15: Verification — execution paths ---

func writeRelayScript(t *testing.T, dir, name, scriptContent string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestE2E_FullConfig_NamedModelsAndFallback(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	fallbackFile := filepath.Join(workspaceDir, "fallback.md")
	os.WriteFile(fallbackFile, []byte("Custom fallback instructions."), 0o644)

	configContent := fmt.Sprintf(`schema_version = 2

[defaults]
iterations = 2
mix = "cc:opus"

[harness.cc.models]
opus = "claude-opus-4-7"

[fallback]
instructions_file = %q
`, fallbackFile)
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if cfg.Defaults.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", cfg.Defaults.Iterations)
	}
	if cfg.Defaults.Mix != "cc:opus" {
		t.Fatalf("mix = %q, want 'cc:opus'", cfg.Defaults.Mix)
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return agent.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	var capturedTaskPrompt string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedModel = opts.Model
			capturedTaskPrompt = opts.TaskPrompt
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	mixSpecs := strings.Fields(cfg.Defaults.Mix)
	r := NewRunner(s, Config{
		WorkspaceDir:             workspaceDir,
		DataDir:                  t.TempDir(),
		AgentMixSpecs:            mixSpecs,
		TargetIterations:         cfg.Defaults.Iterations,
		Resolver:                 resolver,
		FallbackInstructionsFile: cfg.Fallback.InstructionsFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "claude-opus-4-7" {
		t.Errorf("expected model 'claude-opus-4-7' from named resolution, got %q", capturedModel)
	}
	if capturedTaskPrompt != "Custom fallback instructions." {
		t.Errorf("expected fallback instructions as task prompt, got %q", capturedTaskPrompt)
	}

	relays := s.AllRelays()
	if len(relays) != 1 || relays[0].CompletedIterations != 2 {
		t.Fatalf("expected 2 completed iterations, got %+v", relays)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagSet(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := "--model"
	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return agent.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		if spec == "droid" {
			return agent.ResolvedAgent{Harness: "droid"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "--model") || !strings.Contains(content, "droid-v1") {
		t.Errorf("expected '--model droid-v1' in args, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_BareAliasNoModel(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := "--model"
	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid" {
			return agent.ResolvedAgent{Harness: "droid"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "--model") || strings.Contains(content, "droid-v1") {
		t.Errorf("expected no model in args for bare alias, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagEmpty(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	modelFlag := ""
	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: &modelFlag,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return agent.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "droid-v1") {
		t.Errorf("expected positional model 'droid-v1' in args, got %q", content)
	}
	if strings.Contains(content, "--model") {
		t.Errorf("expected no '--model' flag, got %q", content)
	}
}

func TestE2E_UserDefinedHarness_ModelFlagUnset_InfoNote(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	argsFile := filepath.Join(workspaceDir, "recorded_args.txt")
	changeFile := filepath.Join(workspaceDir, "changes.txt")
	script := writeRelayScript(t, workspaceDir, "droid.sh", fmt.Sprintf(
		`echo "ARGS:$@" > %q; touch %q; echo "ok"`, argsFile, changeFile))

	droidExec := &agent.GenericExecutor{
		Command:   []string{script},
		ModelFlag: nil,
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		if spec == "droid:v1" {
			return agent.ResolvedAgent{Harness: "droid", Model: "droid-v1"}, nil
		}
		return testResolver(spec)
	}

	s := newTestStore(t, rallyDir)
	executors := map[string]agent.Executor{"droid": droidExec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"droid:v1"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "droid-v1") {
		t.Errorf("expected no model in args when model_flag unset, got %q", content)
	}
}

func TestE2E_BackwardsCompat_RootModelFields(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	configContent := `claude_model = "root-sonnet"
codex_model = "root-codex"
data_dir = "/tmp/data"
run_hooks_on_autocommit = true
`
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if len(cfg.DeprecationNotes) != 2 {
		t.Fatalf("expected 2 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}

	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("ResolveAgent cc: %v", err)
	}
	if resolved.Model != "root-sonnet" {
		t.Errorf("expected model 'root-sonnet' from root-level field, got %q", resolved.Model)
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return agent.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedModel = opts.Model
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc"},
		TargetIterations: 1,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "root-sonnet" {
		t.Errorf("expected model 'root-sonnet' reaching executor, got %q", capturedModel)
	}
}

func TestE2E_DefaultsSection_NoDeprecation(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := filepath.Join(workspaceDir, ".rally")
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	configContent := `schema_version = 2

[defaults]
claude_model = "defaults-opus"
iterations = 1
mix = "cc"
`
	os.WriteFile(filepath.Join(rallyDir, "config.toml"), []byte(configContent), 0o644)

	cfg, err := config.LoadV2(workspaceDir)
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if len(cfg.DeprecationNotes) != 0 {
		t.Fatalf("expected 0 deprecation notes, got %d: %v", len(cfg.DeprecationNotes), cfg.DeprecationNotes)
	}

	resolved, err := cfg.ResolveAgent("cc")
	if err != nil {
		t.Fatalf("ResolveAgent cc: %v", err)
	}
	if resolved.Model != "defaults-opus" {
		t.Errorf("expected model 'defaults-opus' from [defaults], got %q", resolved.Model)
	}

	resolver := func(spec string) (agent.ResolvedAgent, error) {
		ra, err := cfg.ResolveAgent(spec)
		if err != nil {
			return agent.ResolvedAgent{}, err
		}
		return agent.ResolvedAgent{Harness: ra.Harness, Model: ra.Model}, nil
	}

	s := newTestStore(t, rallyDir)
	var capturedModel string
	changeCounter := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedModel = opts.Model
			changeCounter++
			f, _ := os.OpenFile(filepath.Join(workspaceDir, "changes.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			fmt.Fprintf(f, "change %d\n", changeCounter)
			f.Close()
			return &agent.TryResult{Completed: true}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    strings.Fields(cfg.Defaults.Mix),
		TargetIterations: cfg.Defaults.Iterations,
		Resolver:         resolver,
		TaskPrompt:       "test prompt",
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if capturedModel != "defaults-opus" {
		t.Errorf("expected model 'defaults-opus' reaching executor, got %q", capturedModel)
	}
}
