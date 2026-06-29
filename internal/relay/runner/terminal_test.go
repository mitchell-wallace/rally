package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/store"
	"github.com/mitchell-wallace/rally/internal/style"
)

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
