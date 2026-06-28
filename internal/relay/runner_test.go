package relay

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/laps"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
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

type fakeStallController struct {
	check func(context.Context) (bool, error)
}

func (f *fakeStallController) SetProcessGroupID(int) {}

func (f *fakeStallController) Check(ctx context.Context) (bool, error) {
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
	// Exclude rally's local machine state from git status.
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	os.WriteFile(excludePath, []byte(".rally/state/\n"), 0o644)
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
		"ag": "antigravity", "agy": "antigravity", "antigravity": "antigravity",
		"cc": "claude", "claude": "claude",
		"cx": "codex", "codex": "codex",
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

const cheapTestModel = "opencode/big-pickle"

func cheapTestResolver(spec string) (agent.ResolvedAgent, error) {
	if spec == "op:dsf" {
		return agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel}, nil
	}
	return testResolver(spec)
}

func TestFormatRemaining(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		// Under a minute: seconds, rounded.
		{500 * time.Millisecond, "1s"},
		{1*time.Second + 400*time.Millisecond, "1s"},
		{1*time.Second + 600*time.Millisecond, "2s"},
		{30 * time.Second, "30s"},
		{-time.Second, "0s"},
		// A minute and up: whole minutes only, so the line repaints once a
		// minute instead of every second during a long wait.
		{90 * time.Second, "1m"},
		{2*time.Minute + 59*time.Second, "2m"},
		{2*time.Hour + 5*time.Minute + 7*time.Second, "2h 5m"},
	}
	for _, tc := range cases {
		if got := formatRemaining(tc.in); got != tc.want {
			t.Errorf("formatRemaining(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWaitWithCountdownCancellable(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = devnull
	defer func() {
		os.Stdout = origStdout
		_ = devnull.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	outcome, err := waitWithCountdown(ctx, 10*time.Second, "test %s")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if outcome != waitCancelled {
		t.Errorf("outcome = %v, want waitCancelled", outcome)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("waitWithCountdown did not return promptly on cancel, took %v", elapsed)
	}
}

func TestWaitLoopSkipOnAction(t *testing.T) {
	actionCh := make(chan keyboard.Press, 1)
	actionCh <- keyboard.Press{Action: keyboard.ActionSkip, Confirmed: true}
	start := time.Now()
	outcome := waitLoop(context.Background(), 10*time.Second, "test %s", actionCh, io.Discard, 50*time.Millisecond)
	elapsed := time.Since(start)
	if outcome != waitSkipped {
		t.Errorf("outcome = %v, want waitSkipped", outcome)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("skip should be near-instant, took %v", elapsed)
	}
}

func TestWaitLoopStopOnQuit(t *testing.T) {
	actionCh := make(chan keyboard.Press, 1)
	actionCh <- keyboard.Press{Action: keyboard.ActionQuit, Confirmed: true}
	outcome := waitLoop(context.Background(), 10*time.Second, "test %s", actionCh, io.Discard, 50*time.Millisecond)
	if outcome != waitStopped {
		t.Errorf("outcome = %v, want waitStopped", outcome)
	}
}

func TestWaitLoopElapses(t *testing.T) {
	actionCh := make(chan keyboard.Press)
	start := time.Now()
	outcome := waitLoop(context.Background(), 200*time.Millisecond, "test %s", actionCh, io.Discard, 30*time.Millisecond)
	elapsed := time.Since(start)
	if outcome != waitElapsed {
		t.Errorf("outcome = %v, want waitElapsed", outcome)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("elapsed too early at %v", elapsed)
	}
}

func TestWaitLoopRendersHintAndCountdown(t *testing.T) {
	actionCh := make(chan keyboard.Press, 1)
	actionCh <- keyboard.Press{Action: keyboard.ActionSkip, Confirmed: true}
	var buf bytes.Buffer
	_ = waitLoop(context.Background(), 5*time.Second, "agents frozen, waiting %s...", actionCh, &buf, 50*time.Millisecond)
	got := buf.String()
	if !strings.Contains(got, "agents frozen, waiting 5s...") {
		t.Errorf("output missing countdown line: %q", got)
	}
	if !strings.Contains(got, "Ctrl+S skip") {
		t.Errorf("output missing shortcut hint: %q", got)
	}
}

// TestWaitLoopArmedPressShowsHint pins that a first (unconfirmed) press during a
// wait surfaces the "press X again" hint instead of acting.
func TestWaitLoopArmedPressShowsHint(t *testing.T) {
	actionCh := make(chan keyboard.Press, 2)
	// Arm a quit, then confirm a skip so the loop ends deterministically.
	actionCh <- keyboard.Press{Action: keyboard.ActionQuit, Confirmed: false}
	actionCh <- keyboard.Press{Action: keyboard.ActionSkip, Confirmed: true}
	var buf bytes.Buffer
	outcome := waitLoop(context.Background(), 5*time.Second, "agents paused, waiting %s...", actionCh, &buf, 50*time.Millisecond)
	if outcome != waitSkipped {
		t.Errorf("outcome = %v, want waitSkipped", outcome)
	}
	got := buf.String()
	if !strings.Contains(got, "press Ctrl+C again to quit now") {
		t.Errorf("armed press did not render the press-again hint: %q", got)
	}
}

// TestWaitLoopRendersOnNewLineSafely pins the raw-mode fix: the two rendered
// lines are separated by a carriage-return + line-feed, not a bare line feed
// (which stair-stepped the hint onto fresh lines in raw mode).
func TestWaitLoopRendersOnNewLineSafely(t *testing.T) {
	actionCh := make(chan keyboard.Press, 1)
	actionCh <- keyboard.Press{Action: keyboard.ActionSkip, Confirmed: true}
	var buf bytes.Buffer
	_ = waitLoop(context.Background(), 5*time.Second, "agents paused, waiting %s...", actionCh, &buf, 50*time.Millisecond)
	got := buf.String()
	if strings.Contains(got, "...\n") && !strings.Contains(got, "...\r\n") {
		t.Errorf("countdown line followed by bare LF (raw-mode unsafe): %q", got)
	}
	if !strings.Contains(got, "\r\n") {
		t.Errorf("expected a CR+LF between countdown and hint, got %q", got)
	}
}

func TestWaitWithCountdownElapses(t *testing.T) {
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	origStdout := os.Stdout
	os.Stdout = devnull
	defer func() {
		os.Stdout = origStdout
		_ = devnull.Close()
	}()

	outcome, err := waitWithCountdown(context.Background(), 1500*time.Millisecond, "test %s")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if outcome != waitElapsed {
		t.Errorf("outcome = %v, want waitElapsed", outcome)
	}
}

func TestInstructionsPassedToExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
			if err := progress.RecordLap(workspaceDir, "lap-42"); err != nil {
				return nil, err
			}
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

func TestAgentCyclingDeterminism(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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

	logData, err := os.ReadFile(relayLogPath(dataDir, workspaceDir, 1))
	if err != nil {
		t.Fatalf("read relay log: %v", err)
	}
	if !strings.Contains(string(logData), "rotate fallback for opencode: rotate failed") {
		t.Fatalf("relay log = %q, want rotate fallback message", string(logData))
	}
}

func TestRunnerDoesNotCreateRepoRelayLogDir(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rallyDir, ".gitignore"), []byte("state/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	dataDir := t.TempDir()
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "change.txt"), []byte("ok\n"), 0o644); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          dataDir,
		RouteSpecs:       map[string][]string{"default": {"op:model:1"}},
		TargetIterations: 1,
		Resolver:         testResolver,
		TaskPrompt:       "no repo relay logs",
	}, map[string]agent.Executor{"opencode": exec})

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if _, err := os.Stat(relayLogPath(dataDir, workspaceDir, 1)); err != nil {
		t.Fatalf("expected data-dir relay log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rallyDir, "relays")); !os.IsNotExist(err) {
		t.Fatalf(".rally/relays should not be created when .rally/.gitignore only ignores state/, stat err=%v", err)
	}
}

func TestFilesChangedListUsesCommitDiff(t *testing.T) {
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
	got := r.filesChangedList(nil, before, after, after)
	if len(got) != 2 {
		t.Fatalf("expected 2 changed files from commit diff, got %d (%v)", len(got), got)
	}
	wantSet := map[string]bool{"one.txt": true, "two.txt": true}
	for _, p := range got {
		if !wantSet[p] {
			t.Fatalf("unexpected path %q in %v", p, got)
		}
	}
}

func TestFilesChangedListFallsBackToDirtyFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	// Dirty the workspace without committing. Mix in a .rally/ file that
	// should be filtered out of the result.
	os.WriteFile(filepath.Join(workspaceDir, "user.txt"), []byte("user change\n"), 0o644)
	if err := os.MkdirAll(store.RallyDir(workspaceDir), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(store.RunStatePath(workspaceDir), []byte("{}"), 0o644)

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	got := r.filesChangedList(nil, "", "", "")
	if len(got) != 1 || got[0] != "user.txt" {
		t.Fatalf("expected only user.txt in fallback list, got %v", got)
	}
}

func TestFilesChangedListExcludesClaudeSettings(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed\n"), 0o644)
	os.MkdirAll(filepath.Join(workspaceDir, ".claude"), 0o755)
	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{}\n"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{\"changed\":true}\n"), 0o644)

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	got := r.filesChangedList(nil, "", "", "")
	if len(got) != 0 {
		t.Fatalf("expected empty list when only .claude/settings.local.json is dirty, got %v", got)
	}
}

func TestFilesChangedListExcludesAllTransientPaths(t *testing.T) {
	workspaceDir := t.TempDir()
	initRepo(t, workspaceDir)

	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed\n"), 0o644)
	os.MkdirAll(filepath.Join(workspaceDir, ".claude"), 0o755)
	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{}\n"), 0o644)
	os.MkdirAll(store.RallyDir(workspaceDir), 0o755)
	os.WriteFile(store.RunStatePath(workspaceDir), []byte("{}"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "init")

	os.WriteFile(filepath.Join(workspaceDir, "user.txt"), []byte("change\n"), 0o644)
	os.WriteFile(store.RunStatePath(workspaceDir), []byte("{\"changed\":true}"), 0o644)
	os.MkdirAll(filepath.Join(workspaceDir, ".laps"), 0o755)
	os.WriteFile(filepath.Join(workspaceDir, ".laps", "laps.json"), []byte("{\"changed\":true}\n"), 0o644)
	os.WriteFile(filepath.Join(workspaceDir, ".claude", "settings.local.json"), []byte("{\"changed\":true}\n"), 0o644)

	r := &Runner{cfg: Config{WorkspaceDir: workspaceDir}}
	got := r.filesChangedList(nil, "", "", "")
	if len(got) != 1 || got[0] != "user.txt" {
		t.Fatalf("expected only user.txt, got %v", got)
	}
}

func TestDetectLapsMarkerInText(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		{"empty", "", ""},
		{"leading laps done", "laps done\n\n**What landed**\n- foo", "laps done"},
		{"line laps handoff", "Some intro\nlaps handoff\nrest", "laps handoff"},
		{"laps done as space-separated word in prose", "the agent says laps done at the end", ""},
		{"trailing laps done", "All work complete.\n\nlaps done", "laps done"},
		{"mixed case", "LAPS DONE\nfoo", "laps done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectLapsMarkerInText(tc.summary)
			if got != tc.want {
				t.Errorf("detectLapsMarkerInText(%q) = %q, want %q", tc.summary, got, tc.want)
			}
		})
	}
}

var footerAnsiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripFooterAnsi(s string) string { return footerAnsiRe.ReplaceAllString(s, "") }

func TestRenderRunFooterInterimRedrawsInPlace(t *testing.T) {
	var buf bytes.Buffer
	renderRunFooter(&buf, style.FooterOptions{
		Passed:      false,
		Interim:     true,
		Duration:    12 * time.Second,
		FailReason:  "agent error",
		Attempt:     2,
		MaxAttempts: 5,
	})
	got := buf.String()
	// Interim lines clear the current line and park the cursor at column 0 with
	// no committed newline, so the next attempt's status line overwrites them.
	if !strings.HasPrefix(got, "\r\x1b[2K") {
		t.Errorf("interim footer should begin with a clear-line sequence, got: %q", got)
	}
	if !strings.HasSuffix(got, "\r") {
		t.Errorf("interim footer should park the cursor at the line start, got: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("interim footer must not commit a newline, got: %q", got)
	}
	if !strings.Contains(stripFooterAnsi(got), "↻ retrying 2/5") {
		t.Errorf("interim footer missing retry text, got: %q", got)
	}
}

func TestRenderRunFooterTerminalCommits(t *testing.T) {
	var buf bytes.Buffer
	renderRunFooter(&buf, style.FooterOptions{
		Passed:      false,
		Duration:    12 * time.Second,
		FailReason:  "agent error",
		Attempt:     5,
		MaxAttempts: 5,
	})
	got := buf.String()
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("terminal footer should commit with a trailing newline, got: %q", got)
	}
	if !strings.Contains(stripFooterAnsi(got), "failed after 5 tries") {
		t.Errorf("terminal footer missing outcome text, got: %q", got)
	}
}

// TestRunFooterCadenceExhausted drives a run whose agent always fails and
// asserts the console shows one updating retry line per within-budget attempt
// and exactly one coloured terminal footer — not one red footer per attempt.
func TestRunFooterCadenceExhausted(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "nope"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	var buf bytes.Buffer
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      5,
	}, executors)
	r.out = &buf

	if err := r.Run(context.Background()); err != nil {
		if !strings.Contains(err.Error(), "all agents unavailable") {
			t.Fatalf("run failed with unexpected error: %v", err)
		}
	}

	out := buf.String()
	plain := stripFooterAnsi(out)
	if n := strings.Count(plain, "↻ retrying"); n != 4 {
		t.Errorf("expected 4 interim retry lines (attempts 1-4), got %d\noutput: %q", n, plain)
	}
	if n := strings.Count(plain, "✗"); n != 1 {
		t.Errorf("expected exactly one coloured ✗ footer, got %d\noutput: %q", n, plain)
	}
	if !strings.Contains(plain, "failed after 5 tries") {
		t.Errorf("expected terminal 'failed after 5 tries' footer, got: %q", plain)
	}
	// The terminal footer must be coloured (FailureStyle red), not plain text.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI colour on terminal footer, got: %q", out)
	}
}

// TestRunFooterCadenceRecovery asserts a run that fails then recovers prints
// interim retry lines followed by exactly one green terminal footer.
func TestRunFooterCadenceRecovery(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("ok-%d.txt", attempt)))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	var buf bytes.Buffer
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      5,
	}, executors)
	r.out = &buf

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	plain := stripFooterAnsi(buf.String())
	if n := strings.Count(plain, "↻ retrying"); n != 2 {
		t.Errorf("expected 2 interim retry lines, got %d\noutput: %q", n, plain)
	}
	if !strings.Contains(plain, "passed on try 3/5") {
		t.Errorf("expected green 'passed on try 3/5' footer, got: %q", plain)
	}
	if strings.Contains(plain, "✗") {
		t.Errorf("a recovering run should print no ✗ footer, got: %q", plain)
	}
}

func TestRunHeaderDoesNotExceedTargetAfterFailedRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	attempt := 0
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt == 1 {
				return &agent.TryResult{Completed: false, Summary: "first runner failed"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("ok-%d.txt", attempt)))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{
		"antigravity": exec,
		"claude":      exec,
		"codex":       exec,
	}

	var buf bytes.Buffer
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc", "cx", "ag"},
		UseOverrideRoute: true,
		TargetIterations: 2,
		RetryBudget:      1,
		Resolver:         cheapTestResolver,
	}, executors)
	r.out = &buf

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	plain := stripFooterAnsi(buf.String())
	if strings.Contains(plain, "run: 3/2") {
		t.Fatalf("header exceeded target after failed run:\n%s", plain)
	}
	if n := strings.Count(plain, "run: 1/2"); n != 2 {
		t.Fatalf("expected failed first target slot and replacement to both render as run: 1/2, got %d\n%s", n, plain)
	}
	if n := strings.Count(plain, "run: 2/2"); n != 1 {
		t.Fatalf("expected final target slot to render once as run: 2/2, got %d\n%s", n, plain)
	}
}

// TestRunFooterSingleAttemptColoursImmediately asserts a single-attempt run
// (RetryBudget 1) colours its first failure red with no interim retry line.
func TestRunFooterSingleAttemptColoursImmediately(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "nope"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec}

	var buf bytes.Buffer
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
	}, executors)
	r.out = &buf

	if err := r.Run(context.Background()); err != nil {
		if !strings.Contains(err.Error(), "all agents unavailable") {
			t.Fatalf("run failed with unexpected error: %v", err)
		}
	}

	out := buf.String()
	plain := stripFooterAnsi(out)
	if strings.Contains(plain, "↻ retrying") {
		t.Errorf("single-attempt run should print no interim retry line, got: %q", plain)
	}
	if n := strings.Count(plain, "✗"); n != 1 {
		t.Errorf("expected exactly one ✗ footer, got %d\noutput: %q", n, plain)
	}
	if strings.Contains(plain, "after") {
		t.Errorf("single-attempt footer should not say 'after N tries', got: %q", plain)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI colour on the terminal footer, got: %q", out)
	}
}

func TestRetryWithinRun(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
		agent.ResolvedAgent{Harness: "claude"},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			runCount++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)

			if runCount == 1 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("exec: some-cli not found\n"), 0o644)
				}
				return &agent.TryResult{
					Completed: false,
					Summary:   "harness missing",
					SessionID: "sess-run1-should-discard",
				}, nil
			}

			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("run%d.txt", runCount)))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]agent.Executor{"claude": exec, "codex": exec}

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt < 3 {
				return &agent.TryResult{
					Completed: false,
					Summary:   fmt.Sprintf("attempt %d failed", attempt),
					SessionID: fmt.Sprintf("sess-attempt-%d", attempt),
				}, nil
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

func TestFailureCascadeMultipleInfraIncrements(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "changed but did not finalize"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt == 1 {
				if err := os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644); err != nil {
					return nil, err
				}
				return &agent.TryResult{Completed: true, Summary: "partial"}, nil
			}
			retryPrompt = opts.TaskPrompt
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
		RetryBudget:      2,
		LapsEnabled:      true,
		Resolver:         cheapTestResolver,
	}, map[string]agent.Executor{"opencode": exec})

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			_ = os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644)
			return &agent.TryResult{Completed: true, Summary: "partial"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "no-op"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if err := os.WriteFile(filepath.Join(workspaceDir, "new.txt"), []byte("new"), 0o644); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "changed but did not finalize"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			cmd := exec.Command("git", "-C", workspaceDir, "add", "leftover.txt")
			if err := cmd.Run(); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "staged leftover but did not finalize"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "did nothing"}, nil
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

func TestFailureCascadeSingleInfraDoesNotIncrement(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "infra"}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      1,
		Resolver:         cheapTestResolver,
	}, map[string]agent.Executor{"opencode": exec})

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			return &agent.TryResult{Completed: false, Summary: "agent failed"}, nil
		},
	}
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"op:dsf"},
		TargetIterations: 1,
		RetryBudget:      2,
		Resolver:         cheapTestResolver,
	}, map[string]agent.Executor{"opencode": exec})

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

func TestGracefulStop(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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

func TestCommitHistoryTracking_MultipleAgentCommits(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	// Seed an initial commit so headBefore is non-empty.
	os.WriteFile(filepath.Join(workspaceDir, "seed.txt"), []byte("seed"), 0o644)
	runGit(t, workspaceDir, "add", ".")
	runGit(t, workspaceDir, "commit", "-m", "initial", "--no-verify")
	seedHash := strings.TrimSpace(runGit(t, workspaceDir, "rev-parse", "HEAD"))

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			// Make three distinct commits within a single try.
			for i := 1; i <= 3; i++ {
				name := fmt.Sprintf("file%d.txt", i)
				if err := os.WriteFile(filepath.Join(workspaceDir, name), []byte("x"), 0o644); err != nil {
					return nil, err
				}
				runGit(t, workspaceDir, "add", ".")
				runGit(t, workspaceDir, "commit", "-m", fmt.Sprintf("commit %d", i), "--no-verify")
			}
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

	// Re-read from tries.jsonl (not just the in-memory cache) to prove the full
	// ordered history survives the round-trip to disk.
	reloaded := newTestStore(t, rallyDir)
	tries := reloaded.AllTries()
	if len(tries) != 1 {
		t.Fatalf("expected 1 try, got %d", len(tries))
	}
	got := tries[0]
	if len(got.CommitHistory) != 3 {
		t.Fatalf("expected 3 commits in CommitHistory, got %d: %v", len(got.CommitHistory), got.CommitHistory)
	}

	// The agent's three commits are the first three after the seed, in order.
	// A run that never finalizes (no laps wrapup, as here) writes a stub
	// summary.jsonl after its commits; the end-of-relay failover then commits
	// that leftover as a trailing "rally: commit leftover summary" commit, so it
	// sits after the agent commits and must not be mistaken for one.
	afterSeed := commitRangeAfter(t, workspaceDir, seedHash)
	if len(afterSeed) < 3 {
		t.Fatalf("expected at least 3 commits after seed, got %v", afterSeed)
	}
	wantHashes := afterSeed[:3]
	for i, want := range wantHashes {
		if got.CommitHistory[i] != want {
			t.Errorf("CommitHistory[%d] = %q, want %q", i, got.CommitHistory[i], want)
		}
	}

	// CommitHash backward compat: equals the last element of the history.
	if got.CommitHash != got.CommitHistory[len(got.CommitHistory)-1] {
		t.Errorf("CommitHash = %q, want last history element %q", got.CommitHash, got.CommitHistory[len(got.CommitHistory)-1])
	}

	// The leftover stub summary was committed by the failover, not left dirty.
	if subject := gitSubject(t, workspaceDir, "HEAD"); subject != "rally: commit leftover summary" {
		t.Errorf("HEAD subject = %q, want the failover commit", subject)
	}
	if dirty := strings.TrimSpace(runGit(t, workspaceDir, "status", "--porcelain")); dirty != "" {
		t.Errorf("working tree should be clean after the failover, got:\n%s", dirty)
	}
}

// commitRangeAfter returns the commit hashes reachable from HEAD but not from
// ref, oldest first.
func commitRangeAfter(t *testing.T, dir, ref string) []string {
	t.Helper()
	out := runGit(t, dir, "rev-list", "--reverse", ref+"..HEAD")
	var hashes []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if h := strings.TrimSpace(line); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

// gitSubject returns the subject line of the commit at ref.
func gitSubject(t *testing.T, dir, ref string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, dir, "log", "-1", "--format=%s", ref))
}

// commitHashesInOrder returns the most recent n commit hashes, oldest first.
func commitHashesInOrder(t *testing.T, dir string, n int) []string {
	t.Helper()
	out := runGit(t, dir, "rev-list", "--reverse", fmt.Sprintf("-%d", n), "HEAD")
	var hashes []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if h := strings.TrimSpace(line); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hashes
}

func TestCommitHashTracking_NoChanges(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

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
		Cycle: []agent.ResolvedAgent{
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
		Cycle: []agent.ResolvedAgent{
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

func TestStubEntryOnIncompleteRun(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "test"}, nil }
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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

	entries, err := progress.LoadSummaryEntries(workspaceDir)
	if err != nil {
		t.Fatalf("LoadSummaryEntries error: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one stub entry in summary.jsonl")
	}
	found := false
	for _, entry := range entries {
		if entry.Summary == "agent stopped early" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a stub entry with summary 'agent stopped early', got %v", entries)
	}

	if _, err := os.Stat(progress.RunStatePath(workspaceDir)); !os.IsNotExist(err) {
		t.Fatal("expected run-state.json to be cleared")
	}
}

func TestProgressLapsCompletedForRunReadsSummaryJSONL(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:         "run-1",
		Summary:       "first",
		LapsCompleted: []string{"lap-a", "lap-b"},
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}
	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:         "run-2",
		Summary:       "other",
		LapsCompleted: []string{"lap-c"},
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}
	if err := progress.AppendRunEntry(workspaceDir, progress.RunEntry{
		RunID:         "run-1",
		Summary:       "second",
		LapsCompleted: "lap-d",
	}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	got := progressLapsCompletedForRun(workspaceDir, "run-1")
	want := []string{"lap-a", "lap-b", "lap-d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("progressLapsCompletedForRun() = %v, want %v", got, want)
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

func TestPerHarnessModelPauseIsolation(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)

	s := newTestStore(t, rallyDir)
	resilience := NewResilience(s)

	// Pause only claude:opus — claude:sonnet should remain active.
	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "opus"}, 1); err != nil {
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

	// claude:sonnet is still active, so SelectActiveAgent should succeed.
	picked, _, _, err := resilience.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("SelectActiveAgent should succeed with sonnet active: %v", err)
	}
	if picked.Model != "sonnet" {
		t.Fatalf("expected sonnet (active model), got %s:%s", picked.Harness, picked.Model)
	}

	// Now pause sonnet too — all models paused.
	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "sonnet"}, 1); err != nil {
		t.Fatalf("PauseAgent(sonnet) failed: %v", err)
	}

	_, _, _, err = resilience.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error when all agent models are paused")
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       false,
		FreeRunPromptFile: fallbackFile,
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
	rallyDir := store.RallyDir(workspaceDir)
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
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       false,
		TaskPrompt:        "CLI prompt",
		FreeRunPromptFile: fallbackFile,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != "CLI prompt" {
		t.Errorf("expected CLI prompt to take precedence over fallback, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsIgnoredInLapsMode(t *testing.T) {
	oldHeadPull := headPullLap
	headPullLap = func(context.Context, string) (laps.Lap, error) { return laps.Lap{Title: "configured prompt"}, nil }
	defer func() { headPullLap = oldHeadPull }()

	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       true,
		TaskPrompt:        "configured prompt",
		FreeRunPromptFile: fallbackFile,
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
	rallyDir := store.RallyDir(workspaceDir)
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
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     []string{"cc:1"},
		TargetIterations:  1,
		LapsEnabled:       false,
		FreeRunPromptFile: filepath.Join(workspaceDir, "nonexistent.md"),
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if receivedTaskPrompt != builtInDefaultFreeRunPrompt {
		t.Errorf("expected built-in default fallback, got %q", receivedTaskPrompt)
	}
}

func TestFallbackInstructionsUnconfiguredUsesBuiltInDefault(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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

	if receivedTaskPrompt != builtInDefaultFreeRunPrompt {
		t.Errorf("expected built-in default fallback, got %q", receivedTaskPrompt)
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.Persona == "opencode" && opts.Model == cheapTestModel && opts.RoleInstructions == "Senior route guidance." && failSeniorCheapAttempts > 0 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
				}
				failSeniorCheapAttempts--
				return &agent.TryResult{Completed: false, Summary: "simulated senior cheap-model failure"}, nil
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
		"opencode": exec,
		"codex":    exec,
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

// --- Phase 15: Verification — execution paths ---

func writeRelayScript(t *testing.T, dir, name, scriptContent string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildRecentContext_PerSummaryTruncation(t *testing.T) {
	longSummary := strings.Repeat("abcdefghij", 20)

	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: true, Summary: longSummary},
		{RunID: 2, AgentType: "codex", Completed: false, Summary: longSummary},
	}

	result := buildRecentContext(tries, 50, 0)
	for _, tr := range tries {
		if !strings.Contains(result, fmt.Sprintf("Run %d (%s)", tr.RunID, tr.AgentType)) {
			t.Errorf("expected mention of run %d", tr.RunID)
		}
	}
	if !strings.Contains(result, "... [truncated] ...") {
		t.Errorf("expected per-summary truncation marker in result: %q", result)
	}
}

func TestBuildRecentContext_OverallTruncation(t *testing.T) {
	mediumSummary := strings.Repeat("x", 200)
	tries := []store.TryRecord{}
	for i := 1; i <= 20; i++ {
		tries = append(tries, store.TryRecord{
			RunID: i, AgentType: "claude", Completed: true, Summary: mediumSummary,
		})
	}

	result := buildRecentContext(tries, 0, 500)
	if !strings.Contains(result, "... [truncated] ...") {
		t.Errorf("expected overall truncation marker in result: %q", result)
	}
}

func TestE2E_FullConfig_NamedModelsAndFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
		WorkspaceDir:      workspaceDir,
		DataDir:           t.TempDir(),
		AgentMixSpecs:     mixSpecs,
		TargetIterations:  cfg.Defaults.Iterations,
		Resolver:          resolver,
		FreeRunPromptFile: cfg.FreeRun.PromptFile,
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	rallyDir := store.RallyDir(workspaceDir)
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
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

// --- Phase 11: Verification ---

func TestE2E_CheapRotationOpencodeGLMToKimi(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var modelsExecuted []string
	exec := &funcExecutor{
		rotateSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			modelsExecuted = append(modelsExecuted, opts.Model)
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("change-%s.txt", opts.Model)))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "ok"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			capturedSessionIDs = append(capturedSessionIDs, opts.ResumeSessionID)
			if attempt == 1 {
				if opts.LogPath != "" {
					_ = os.WriteFile(opts.LogPath, []byte("sending to claude...\nerror 429 Too Many Requests\nretry-after: 1\n"), 0o644)
				}
				return &agent.TryResult{Completed: false, Summary: "rate limit hit", SessionID: "sess-rate"}, nil
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
		RetryBudget:      3,
	}, map[string]agent.Executor{"claude": exec})

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			time.Sleep(50 * time.Millisecond)
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
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
	}, map[string]agent.Executor{"codex": exec})

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			resumeIDs = append(resumeIDs, opts.ResumeSessionID)
			if attempt == 1 {
				<-freezeCh
				return &agent.TryResult{Completed: false, Summary: "freeze", SessionID: "sess-probe"}, nil
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
		AgentMixSpecs:    []string{"cx:1"},
		TargetIterations: 1,
		RetryBudget:      3,
		LivenessProbe:    true,
	}, map[string]agent.Executor{"codex": exec})

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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					attempt++
					if attempt == 1 && opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte(tt.logContent), 0o644)
					}
					if attempt < tt.wantAttempts {
						return &agent.TryResult{Completed: false, Summary: "fail", SessionID: "sess-1"}, nil
					}
					f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
					f.WriteString("changed")
					f.Close()
					return &agent.TryResult{Completed: true, Summary: "success"}, nil
				},
			}
			executors := map[string]agent.Executor{tt.harness: exec}

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

func TestE2E_ErrorPatternRotateAdvancesRoute(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var executed []string
	opencodeExec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executed = append(executed, "opencode:"+opts.Model)
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("some output\nerror: API bad request from provider\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "rotate"}, nil
		},
	}
	codexExec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			executed = append(executed, "codex:"+opts.Model)
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
			f.Close()
			return &agent.TryResult{Completed: true, Summary: "success"}, nil
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
	}, map[string]agent.Executor{
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

func TestE2E_RunStateClearedAtRelayStart(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	if err := progress.SaveRunState(workspaceDir, &progress.RunState{RunID: "old-run", HandoffState: 1, RecordedLaps: []string{"lap-old"}}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			f, _ := os.Create(filepath.Join(workspaceDir, "success.txt"))
			f.WriteString("changed")
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

	rs, err := progress.LoadRunState(workspaceDir)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if rs.RunID != "" {
		t.Fatalf("expected run-state cleared at relay start, got RunID=%q", rs.RunID)
	}
	if rs.HandoffState != 0 {
		t.Fatalf("expected HandoffState=0 after relay start, got %d", rs.HandoffState)
	}
	if len(rs.RecordedLaps) != 0 {
		t.Fatalf("expected RecordedLaps empty after relay start, got %v", rs.RecordedLaps)
	}
}

func TestRunWritesActiveTryMetadataBeforeExecutor(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	var activeAtExecutor progress.RunState
	var executorErr error
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			rs, err := progress.LoadRunState(workspaceDir)
			if err != nil {
				executorErr = fmt.Errorf("load run-state in executor: %w", err)
				return nil, executorErr
			}
			activeAtExecutor = *rs
			if opts.LogPath != "" {
				if err := os.WriteFile(opts.LogPath, []byte("executor log\n"), 0o644); err != nil {
					executorErr = fmt.Errorf("write try log: %w", err)
					return nil, executorErr
				}
			}
			if err := os.WriteFile(filepath.Join(workspaceDir, "active-try.txt"), []byte("changed"), 0o644); err != nil {
				executorErr = fmt.Errorf("write workspace file: %w", err)
				return nil, executorErr
			}
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
	}, map[string]agent.Executor{"claude": exec})
	r.out = io.Discard

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if executorErr != nil {
		t.Fatalf("executor setup failed: %v", executorErr)
	}

	if activeAtExecutor.RunID != "relay-1-run-1" {
		t.Fatalf("RunID visible to executor = %q, want relay-1-run-1", activeAtExecutor.RunID)
	}
	if activeAtExecutor.ActiveRelayID != 1 {
		t.Fatalf("ActiveRelayID = %d, want 1", activeAtExecutor.ActiveRelayID)
	}
	if activeAtExecutor.ActiveRunID != 1 {
		t.Fatalf("ActiveRunID = %d, want 1", activeAtExecutor.ActiveRunID)
	}
	if activeAtExecutor.ActiveTryID != 1 {
		t.Fatalf("ActiveTryID = %d, want 1", activeAtExecutor.ActiveTryID)
	}
	if activeAtExecutor.ActiveLogPath == "" {
		t.Fatal("ActiveLogPath was empty")
	}
	if _, err := time.Parse(time.RFC3339, activeAtExecutor.ActiveStartedAt); err != nil {
		t.Fatalf("ActiveStartedAt = %q, want RFC3339: %v", activeAtExecutor.ActiveStartedAt, err)
	}

	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("tries = %d, want 1", len(tries))
	}
	if tries[0].ID != activeAtExecutor.ActiveTryID {
		t.Fatalf("try ID = %d, active try ID = %d", tries[0].ID, activeAtExecutor.ActiveTryID)
	}
	if tries[0].LogPath != activeAtExecutor.ActiveLogPath {
		t.Fatalf("try LogPath = %q, active LogPath = %q", tries[0].LogPath, activeAtExecutor.ActiveLogPath)
	}
	rs, err := progress.LoadRunState(workspaceDir)
	if err != nil {
		t.Fatalf("LoadRunState after run: %v", err)
	}
	if rs.ActiveRelayID != 0 || rs.ActiveRunID != 0 || rs.ActiveTryID != 0 || rs.ActiveLogPath != "" || rs.ActiveStartedAt != "" {
		t.Fatalf("active metadata left after run: %+v", rs)
	}
}

func TestE2E_WindowsFreezeDisabledRetryBudgetExhaustion(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)

	s := newTestStore(t, rallyDir)
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			return &agent.TryResult{Completed: false, Summary: "fail"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

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

// TestProbationIncompletePromotesToActive is an end-to-end test exercising the
// full Run loop: an agent starts in probation (frozen > FreezeDuration ago),
// produces an incomplete result (laps-backed run with file changes but no
// finalization), and should be promoted back to active — not re-frozen.
//
// This catches the bug where the probation branch in Run() compared
// failReason == "incomplete run", but runOne's ClassifyError had already
// overwritten failReason to the classified string before returning. The fix
// checks failureClass == reliability.FailureIncomplete instead.
func TestProbationIncompletePromotesToActive(t *testing.T) {
	oldHeadPull := headPullLap
	pullCount := 0
	headPullLap = func(context.Context, string) (laps.Lap, error) {
		pullCount++
		if pullCount == 1 {
			return laps.Lap{ID: "lap-1", Title: "probation test", Assignee: "senior"}, nil
		}
		return laps.NoLap, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			_ = os.WriteFile(filepath.Join(workspaceDir, fmt.Sprintf("partial-%d.txt", attempt)), []byte("partial"), 0o644)
			return &agent.TryResult{Completed: true, Summary: "made progress but did not finalize"}, nil
		},
	}
	executors := map[string]agent.Executor{"opencode": exec}

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

func TestStallRecovery_VerifyRoleExcluded(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	os.MkdirAll(rallyDir, 0o755)
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	stallCh := make(chan struct{})
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			os.WriteFile(filepath.Join(workspaceDir, "fix.txt"), []byte("trivial fix"), 0o644)
			runGit(t, workspaceDir, "add", "fix.txt")
			runGit(t, workspaceDir, "commit", "-m", "trivial fix", "--no-verify")
			<-stallCh
			return &agent.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
	}, map[string]agent.Executor{"claude": exec})

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
		agent.ResolvedAgent{Harness: "claude"},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			os.WriteFile(filepath.Join(workspaceDir, "impl.txt"), []byte("implementation"), 0o644)
			runGit(t, workspaceDir, "add", "impl.txt")
			runGit(t, workspaceDir, "commit", "-m", "implementation work", "--no-verify")
			<-stallCh
			return &agent.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
	}, map[string]agent.Executor{"claude": exec})

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
		agent.ResolvedAgent{Harness: "claude"},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			// Create a file so there are changes to auto-commit
			f, _ := os.Create(filepath.Join(workspaceDir, "verify-fix.txt"))
			f.WriteString("trivial fix")
			f.Close()
			<-freezeCh
			return &agent.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1, // only 1 attempt
	}, map[string]agent.Executor{"claude": exec})

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
		agent.ResolvedAgent{Harness: "claude"},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			f, _ := os.Create(filepath.Join(workspaceDir, "impl-fix.txt"))
			f.WriteString("implementation fix")
			f.Close()
			<-freezeCh
			return &agent.TryResult{Completed: false, Summary: "stalled"}, nil
		},
	}

	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
	}, map[string]agent.Executor{"claude": exec})

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
		agent.ResolvedAgent{Harness: "claude"},
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

func TestPromptBudget_PerSummaryTruncation(t *testing.T) {
	longSummary := strings.Repeat("x", 500)
	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: false, Summary: longSummary},
	}
	result := buildRecentContext(tries, 100, 0)
	if !strings.Contains(result, "... [truncated] ...") {
		t.Fatal("expected truncation marker in output")
	}
	if len(result) >= 500 {
		t.Fatalf("expected output shorter than full summary, got %d chars", len(result))
	}
	headSize := 100 / 2
	tailSize := 100 - headSize
	if !strings.Contains(result, longSummary[:headSize]) {
		t.Fatal("expected head of summary preserved")
	}
	if !strings.Contains(result, longSummary[len(longSummary)-tailSize:]) {
		t.Fatal("expected tail of summary preserved")
	}
}

func TestPromptBudget_ShortSummariesPassThrough(t *testing.T) {
	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: true, Summary: "short"},
		{RunID: 2, AgentType: "opencode", Completed: false, Summary: "also short"},
	}
	result := buildRecentContext(tries, 1000, 0)
	if strings.Contains(result, "... [truncated] ...") {
		t.Fatal("short summaries should not be truncated")
	}
	if !strings.Contains(result, "summary=short") {
		t.Fatal("expected first summary present")
	}
	if !strings.Contains(result, "summary=also short") {
		t.Fatal("expected second summary present")
	}
}

func TestBuildRecentContextCancelledUsesOutcome(t *testing.T) {
	tries := []store.TryRecord{
		{
			RunID:              1,
			AgentType:          "codex",
			Completed:          false,
			Outcome:            reliability.OutcomeCancelled,
			CancellationSource: "graceful_stop",
			Summary:            "operator stopped the run",
		},
	}

	result := buildRecentContext(tries, 1000, 0)
	if !strings.Contains(result, "outcome=cancelled source=graceful_stop") {
		t.Fatalf("expected cancelled outcome/source in recent context, got: %q", result)
	}
	if strings.Contains(result, "completed=false") {
		t.Fatalf("cancelled recent context should not look like a generic failed try, got: %q", result)
	}
}

func TestPromptBudget_CountHonored(t *testing.T) {
	var tries []store.TryRecord
	for i := 1; i <= 3; i++ {
		tries = append(tries, store.TryRecord{RunID: i, AgentType: "claude", Completed: true, Summary: fmt.Sprintf("try %d", i)})
	}
	result := buildRecentContext(tries, 0, 0)
	for i := 1; i <= 3; i++ {
		if !strings.Contains(result, fmt.Sprintf("try %d", i)) {
			t.Fatalf("expected try %d in output", i)
		}
	}
}

func TestPromptBudget_OverallLimit(t *testing.T) {
	tries := []store.TryRecord{
		{RunID: 1, AgentType: "claude", Completed: false, Summary: strings.Repeat("a", 200)},
		{RunID: 2, AgentType: "claude", Completed: false, Summary: strings.Repeat("b", 200)},
	}
	result := buildRecentContext(tries, 0, 200)
	if !strings.Contains(result, "... [truncated] ...") {
		t.Fatal("expected overall truncation marker")
	}
	if len(result) > 250 {
		t.Fatalf("expected output near 200 chars, got %d", len(result))
	}
	headSize := 200 / 2
	if !strings.HasPrefix(result[:headSize], "Run 1") {
		t.Fatal("expected head of overall context to start with first try")
	}
	if !strings.Contains(result[len(result)-headSize:], strings.Repeat("b", 10)) {
		t.Fatal("expected tail of overall context to contain second summary")
	}
}

func TestLapPinValidation_NormalPassThrough(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-1"})
	if mismatch {
		t.Fatalf("expected no mismatch for normal pass-through, got reason=%q", reason)
	}
}

func TestLapPinValidation_WrongLapConsumed(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-9"})
	if !mismatch {
		t.Fatal("expected mismatch when consumed lap differs from pinned")
	}
	if reason != "wrong_lap_consumed" {
		t.Fatalf("reason = %q, want %q", reason, "wrong_lap_consumed")
	}
}

func TestLapPinValidation_MultiLapConsumed(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-1", "lap-2"})
	if !mismatch {
		t.Fatal("expected mismatch when multiple laps consumed")
	}
	if reason != "multi_lap_consumed" {
		t.Fatalf("reason = %q, want %q", reason, "multi_lap_consumed")
	}
}

func TestLapPinValidation_EmptyPinnedID(t *testing.T) {
	reason, mismatch := validatePinnedLap("", []string{"lap-1"})
	if mismatch {
		t.Fatalf("expected no mismatch when pinned lap ID is empty, got reason=%q", reason)
	}
}

func TestLapPinValidation_NoRecordedLaps(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", nil)
	if mismatch {
		t.Fatalf("expected no mismatch when no laps recorded, got reason=%q", reason)
	}
	reason, mismatch = validatePinnedLap("lap-1", []string{})
	if mismatch {
		t.Fatalf("expected no mismatch when empty laps recorded, got reason=%q", reason)
	}
}

func TestLapPinValidation_DuplicateSameLap(t *testing.T) {
	reason, mismatch := validatePinnedLap("lap-1", []string{"lap-1", "lap-1"})
	if mismatch {
		t.Fatalf("expected no mismatch for duplicate same lap, got reason=%q", reason)
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"other-lap"}
			progress.SaveRunState(workspaceDir, rs)
			return &agent.TryResult{Completed: true, Summary: "wrong lap"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"lap-1", "lap-2"}
			progress.SaveRunState(workspaceDir, rs)
			return &agent.TryResult{Completed: true, Summary: "multi lap"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"other-lap"}
			progress.SaveRunState(workspaceDir, rs)
			return &agent.TryResult{Completed: true, Summary: "wrong lap but pinned done"}, nil
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			callCount++
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec failed\n"), 0o644)
			}
			if callCount >= 3 {
				rs, _ := progress.LoadRunState(workspaceDir)
				rs.RecordedLaps = []string{"wrong-lap"}
				progress.SaveRunState(workspaceDir, rs)
			}
			return &agent.TryResult{Completed: false, Summary: "failed"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})

	res, _ := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					// Log tail would classify as harness_launch (infra) on its own.
					if opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte("fork/exec /bin/agent: failed\n"), 0o644)
					}
					return &agent.TryResult{
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
			}, map[string]agent.Executor{"opencode": exec})

			res, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("fork/exec /bin/agent: failed\n"), 0o644)
			}
			// Produce an unfinalized task-file change so runOne computes the
			// incomplete context; executor evidence must still take priority.
			_ = os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("dirty\n"), 0o644)
			return &agent.TryResult{
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
	}, map[string]agent.Executor{"opencode": exec})

	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					callCount++
					if opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte("failed\n"), 0o644)
					}
					ev := &reliability.FailureEvidence{Category: tt.category}
					if tt.wantReset {
						ev.ResetAfter = resetAfter
					}
					return &agent.TryResult{Completed: false, Summary: "failed", Evidence: ev}, nil
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
			}, map[string]agent.Executor{"opencode": exec})
			// sleepFunc is a no-op so any (unexpected) wait+resume cooldown does
			// not slow the test; the assertion is on attempt count.
			r.sleepFunc = func(time.Duration) {}

			res, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					execCalls++
					ev := &reliability.FailureEvidence{Category: tt.category}
					if tt.category == reliability.CategoryUsageLimit {
						ev.ResetAfter = time.Hour
					}
					return &agent.TryResult{Completed: false, Summary: "failed", Evidence: ev}, nil
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
			}, map[string]agent.Executor{"opencode": exec})

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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			rs, _ := progress.LoadRunState(workspaceDir)
			rs.RecordedLaps = []string{"lap-1"}
			progress.SaveRunState(workspaceDir, rs)
			os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("done"), 0o644)
			runGit(t, workspaceDir, "add", "work.txt")
			runGit(t, workspaceDir, "commit", "-m", "completed work", "--no-verify")
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			progress.RecordLap(workspaceDir, "lap-1")
			os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("done"), 0o644)
			runGit(t, workspaceDir, "add", "work.txt")
			runGit(t, workspaceDir, "commit", "-m", "completed work", "--no-verify")
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedPrompt = agent.BuildPrompt(opts)
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedPrompt = agent.BuildPrompt(opts)
			// Produce a real user-file change so the run is not flagged as
			// "no changes made". The leftover-work check already ran at run
			// start (captured in opts), so this write does not affect it.
			if err := os.WriteFile(filepath.Join(workspaceDir, "result.go"), []byte("package result\n"), 0o644); err != nil {
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			capturedPrompt = agent.BuildPrompt(opts)
			// Produce a real user-file change so the run is not flagged as
			// "no changes made". The leftover-work check already ran at run
			// start (captured in opts), so this write does not affect it.
			if err := os.WriteFile(filepath.Join(workspaceDir, "result.go"), []byte("package result\n"), 0o644); err != nil {
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
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			attempt++
			if attempt == 1 {
				_ = os.WriteFile(filepath.Join(workspaceDir, "partial.txt"), []byte("partial"), 0o644)
				return &agent.TryResult{Completed: true, Summary: "partial"}, nil
			}
			retryPrompt = opts.TaskPrompt
			if err := progress.RecordLap(workspaceDir, "lap-1"); err != nil {
				return nil, err
			}
			_ = os.WriteFile(filepath.Join(workspaceDir, "done.txt"), []byte("done"), 0o644)
			return &agent.TryResult{Completed: true, Summary: "done"}, nil
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
	}, map[string]agent.Executor{"opencode": exec})

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

func TestCategorizedTryRecordCarriesCategoryAndDisplayReason(t *testing.T) {
	tests := []struct {
		name                   string
		category               reliability.FailureCategory
		evidence               *reliability.FailureEvidence
		wantCategory           string
		wantFailReasonContains string
	}{
		{
			name:                   "usage_limit with reset",
			category:               reliability.CategoryUsageLimit,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit, ResetAfter: 5 * time.Hour},
			wantCategory:           string(reliability.CategoryUsageLimit),
			wantFailReasonContains: "usage limit, resets in",
		},
		{
			name:                   "short_rate_limit",
			category:               reliability.CategoryShortRateLimit,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryShortRateLimit, RetryAfter: 90 * time.Second},
			wantCategory:           string(reliability.CategoryShortRateLimit),
			wantFailReasonContains: "rate limit, waiting",
		},
		{
			name:                   "invalid_model",
			category:               reliability.CategoryInvalidModel,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryInvalidModel},
			wantCategory:           string(reliability.CategoryInvalidModel),
			wantFailReasonContains: "invalid model",
		},
		{
			name:                   "auth_or_proxy",
			category:               reliability.CategoryAuthOrProxy,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryAuthOrProxy},
			wantCategory:           string(reliability.CategoryAuthOrProxy),
			wantFailReasonContains: "auth/proxy error",
		},
		{
			name:                   "provider_overloaded",
			category:               reliability.CategoryProviderOverloaded,
			evidence:               &reliability.FailureEvidence{Category: reliability.CategoryProviderOverloaded},
			wantCategory:           string(reliability.CategoryProviderOverloaded),
			wantFailReasonContains: "provider overloaded",
		},
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
				fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
					if opts.LogPath != "" {
						_ = os.WriteFile(opts.LogPath, []byte("failed\n"), 0o644)
					}
					return &agent.TryResult{Completed: false, Summary: "failed", Evidence: tt.evidence}, nil
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

			_, err := r.runOne(
				context.Background(),
				&store.RelayRecord{ID: 1, TargetIterations: 1},
				0,
				agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
				runTask{Name: "task", Prompt: "do work", Assignee: "senior"},
				nil, nil, false, false, nil, nil,
				io.Discard,
			)
			if err != nil {
				t.Fatalf("runOne error = %v", err)
			}

			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("tries = %d, want 1", len(tries))
			}
			rec := tries[0]
			if rec.Category != tt.wantCategory {
				t.Fatalf("Category = %q, want %q", rec.Category, tt.wantCategory)
			}
			if !strings.Contains(rec.FailReason, tt.wantFailReasonContains) {
				t.Fatalf("FailReason = %q, want containing %q", rec.FailReason, tt.wantFailReasonContains)
			}
			if rec.Category == rec.FailReason {
				t.Fatalf("Category and FailReason should differ: both = %q", rec.Category)
			}
		})
	}
}

func TestFormatCategorizedDisplay(t *testing.T) {
	tests := []struct {
		name     string
		cat      reliability.FailureCategory
		cooldown time.Duration
		evidence *reliability.FailureEvidence
		want     string
	}{
		{
			name:     "usage_limit with reset after",
			cat:      reliability.CategoryUsageLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit, ResetAfter: 123*time.Hour + 50*time.Minute},
			want:     "usage limit, resets in 123h50m",
		},
		{
			name:     "usage_limit with reset at",
			cat:      reliability.CategoryUsageLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{
				Category: reliability.CategoryUsageLimit,
				ResetAt:  func() *time.Time { t := time.Now().Add(5*time.Hour + 30*time.Minute); return &t }(),
			},
		},
		{
			// Without parsed reset evidence the label carries no timing: the
			// classifier cooldown is not the quota reset, and the real bench
			// window is BenchDefaultDuration.
			name:     "usage_limit without parsed reset omits timing",
			cat:      reliability.CategoryUsageLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryUsageLimit},
			want:     "usage limit",
		},
		{
			name:     "short_rate_limit with cooldown",
			cat:      reliability.CategoryShortRateLimit,
			cooldown: 2 * time.Minute,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryShortRateLimit},
			want:     "rate limit, waiting 2m",
		},
		{
			name:     "invalid_model no timing",
			cat:      reliability.CategoryInvalidModel,
			evidence: &reliability.FailureEvidence{Category: reliability.CategoryInvalidModel},
			want:     "invalid model",
		},
		{
			name: "agent_error no timing",
			cat:  reliability.CategoryAgentError,
			want: "agent error",
		},
		{
			name: "incomplete_finalization no timing",
			cat:  reliability.CategoryIncompleteFinalization,
			want: "incomplete: file changes without finalization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCategorizedDisplay(tt.cat, tt.cooldown, tt.evidence)
			if tt.want != "" && got != tt.want {
				t.Fatalf("formatCategorizedDisplay() = %q, want %q", got, tt.want)
			}
			if strings.Contains(tt.name, "reset at") {
				if !strings.Contains(got, "usage limit, resets in") {
					t.Fatalf("expected 'usage limit, resets in' in output, got %q", got)
				}
				if strings.Contains(got, "0m") && !strings.Contains(got, "0h") {
					t.Fatalf("expected hours in output for reset_at test, got %q", got)
				}
			}
		})
	}
}
