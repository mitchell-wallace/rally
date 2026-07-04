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
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/store"
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
	outcome, err := waitWithCountdown(ctx, runtimeevent.NoopSink{}, nil, 10*time.Second, "test %s")
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
	actionCh := make(chan runtimeevent.Press, 1)
	actionCh <- runtimeevent.Press{Action: runtimeevent.OperatorActionSkip, Confirmed: true}
	start := time.Now()
	outcome := waitLoop(context.Background(), runtimeevent.NoopSink{}, 10*time.Second, "test %s", actionCh, 50*time.Millisecond)
	elapsed := time.Since(start)
	if outcome != waitSkipped {
		t.Errorf("outcome = %v, want waitSkipped", outcome)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("skip should be near-instant, took %v", elapsed)
	}
}

func TestWaitLoopStopOnQuit(t *testing.T) {
	actionCh := make(chan runtimeevent.Press, 1)
	actionCh <- runtimeevent.Press{Action: runtimeevent.OperatorActionQuit, Confirmed: true}
	outcome := waitLoop(context.Background(), runtimeevent.NoopSink{}, 10*time.Second, "test %s", actionCh, 50*time.Millisecond)
	if outcome != waitStopped {
		t.Errorf("outcome = %v, want waitStopped", outcome)
	}
}

func TestWaitLoopElapses(t *testing.T) {
	actionCh := make(chan runtimeevent.Press)
	start := time.Now()
	outcome := waitLoop(context.Background(), runtimeevent.NoopSink{}, 200*time.Millisecond, "test %s", actionCh, 30*time.Millisecond)
	elapsed := time.Since(start)
	if outcome != waitElapsed {
		t.Errorf("outcome = %v, want waitElapsed", outcome)
	}
	if elapsed < 150*time.Millisecond {
		t.Errorf("elapsed too early at %v", elapsed)
	}
}

func TestWaitLoopRendersHintAndCountdown(t *testing.T) {
	actionCh := make(chan runtimeevent.Press, 1)
	actionCh <- runtimeevent.Press{Action: runtimeevent.OperatorActionSkip, Confirmed: true}
	rec := runtimeevent.NewRecordingSink()
	_ = waitLoop(context.Background(), rec, 5*time.Second, "agents frozen, waiting %s...", actionCh, 50*time.Millisecond)
	events := rec.Events()
	if len(events) == 0 {
		t.Fatal("wait loop emitted no events")
	}
	start, ok := events[0].(runtimeevent.WaitStarted)
	if !ok {
		t.Fatalf("first event = %T, want WaitStarted", events[0])
	}
	if start.Message != "agents frozen, waiting 5s..." {
		t.Errorf("countdown message = %q", start.Message)
	}
}

// TestWaitLoopArmedPressShowsHint pins that a first (unconfirmed) press during a
// wait surfaces the "press X again" hint instead of acting.
func TestWaitLoopArmedPressShowsHint(t *testing.T) {
	actionCh := make(chan runtimeevent.Press, 2)
	// Arm a quit, then confirm a skip so the loop ends deterministically.
	actionCh <- runtimeevent.Press{Action: runtimeevent.OperatorActionQuit, Confirmed: false}
	actionCh <- runtimeevent.Press{Action: runtimeevent.OperatorActionSkip, Confirmed: true}
	rec := runtimeevent.NewRecordingSink()
	outcome := waitLoop(context.Background(), rec, 5*time.Second, "agents paused, waiting %s...", actionCh, 50*time.Millisecond)
	if outcome != waitSkipped {
		t.Errorf("outcome = %v, want waitSkipped", outcome)
	}
	found := false
	for _, event := range rec.Events() {
		if tick, ok := event.(runtimeevent.WaitTick); ok && tick.Hint == "press Ctrl+C again to quit now" {
			found = true
		}
	}
	if !found {
		t.Errorf("armed press did not emit the press-again hint: %v", rec.Events())
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

	outcome, err := waitWithCountdown(context.Background(), runtimeevent.NoopSink{}, nil, 1500*time.Millisecond, "test %s")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if outcome != waitElapsed {
		t.Errorf("outcome = %v, want waitElapsed", outcome)
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
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "nope"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      5,
		EventSink:        rec,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		if !strings.Contains(err.Error(), "all agents unavailable") {
			t.Fatalf("run failed with unexpected error: %v", err)
		}
	}

	retries, terminal := footerEvents(rec.Events())
	if len(retries) != 4 {
		t.Fatalf("expected 4 interim retry events (attempts 1-4), got %d: %#v", len(retries), retries)
	}
	for i, retry := range retries {
		if !retry.Interim || retry.Attempt != i+1 || retry.MaxAttempts != 5 {
			t.Errorf("retry event %d = %+v, want interim attempt %d/5", i, retry, i+1)
		}
	}
	if terminal == nil {
		t.Fatal("expected one terminal attempt footer event")
	}
	if terminal.Passed || terminal.Interim || terminal.Attempt != 5 || terminal.MaxAttempts != 5 {
		t.Errorf("terminal footer event = %+v, want failed terminal attempt 5/5", *terminal)
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
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt < 3 {
				return &harnessapi.TryResult{Completed: false, Summary: "fail"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("ok-%d.txt", attempt)))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      5,
		EventSink:        rec,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	retries, terminal := footerEvents(rec.Events())
	if len(retries) != 2 {
		t.Fatalf("expected 2 interim retry events, got %d: %#v", len(retries), retries)
	}
	for i, retry := range retries {
		if !retry.Interim || retry.Attempt != i+1 || retry.MaxAttempts != 5 {
			t.Errorf("retry event %d = %+v, want interim attempt %d/5", i, retry, i+1)
		}
	}
	if terminal == nil {
		t.Fatal("expected one terminal attempt footer event")
	}
	if !terminal.Passed || terminal.Interim || terminal.Attempt != 3 || terminal.MaxAttempts != 5 {
		t.Errorf("terminal footer event = %+v, want passing terminal attempt 3/5", *terminal)
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
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			attempt++
			if attempt == 1 {
				return &harnessapi.TryResult{Completed: false, Summary: "first runner failed"}, nil
			}
			f, _ := os.Create(filepath.Join(workspaceDir, fmt.Sprintf("ok-%d.txt", attempt)))
			f.WriteString("changed")
			f.Close()
			return &harnessapi.TryResult{Completed: true, Summary: "success"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{
		"antigravity": exec,
		"claude":      exec,
		"codex":       exec,
	}

	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc", "cx", "ag"},
		UseOverrideRoute: true,
		TargetIterations: 2,
		RetryBudget:      1,
		Resolver:         cheapTestResolver,
		EventSink:        rec,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	var got []runtimeevent.OutingHeaderReady
	for _, event := range rec.Events() {
		if header, ok := event.(runtimeevent.OutingHeaderReady); ok {
			got = append(got, header)
		}
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 outing header events, got %d: %#v", len(got), got)
	}
	want := []int{0, 0, 1}
	for i, header := range got {
		if header.TotalOutings != 2 {
			t.Errorf("header %d TotalOutings = %d, want 2", i, header.TotalOutings)
		}
		if header.OutingIndex != want[i] {
			t.Errorf("header %d OutingIndex = %d, want %d", i, header.OutingIndex, want[i])
		}
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
		fn: func(ctx context.Context, opts harnessapi.RunOptions) (*harnessapi.TryResult, error) {
			return &harnessapi.TryResult{Completed: false, Summary: "nope"}, nil
		},
	}
	executors := map[string]harnessapi.Executor{"claude": exec}

	rec := runtimeevent.NewRecordingSink()
	r := NewRunner(s, Config{
		WorkspaceDir:     workspaceDir,
		DataDir:          t.TempDir(),
		AgentMixSpecs:    []string{"cc:1"},
		TargetIterations: 1,
		RetryBudget:      1,
		EventSink:        rec,
	}, executors)

	if err := r.Run(context.Background()); err != nil {
		if !strings.Contains(err.Error(), "all agents unavailable") {
			t.Fatalf("run failed with unexpected error: %v", err)
		}
	}

	retries, terminal := footerEvents(rec.Events())
	if len(retries) != 0 {
		t.Fatalf("single-attempt run should emit no interim retry footer, got %#v", retries)
	}
	if terminal == nil {
		t.Fatal("expected one terminal attempt footer event")
	}
	if terminal.Passed || terminal.Interim || terminal.Attempt != 1 || terminal.MaxAttempts != 1 {
		t.Errorf("terminal footer event = %+v, want failed terminal attempt 1/1", *terminal)
	}
}

func footerEvents(events []runtimeevent.Event) ([]runtimeevent.FooterData, *runtimeevent.FooterData) {
	var retries []runtimeevent.FooterData
	var terminal *runtimeevent.FooterData
	for _, event := range events {
		switch e := event.(type) {
		case runtimeevent.RetryFooterUpdated:
			retries = append(retries, e.FooterData)
		case runtimeevent.AttemptFinished:
			footer := e.FooterData
			terminal = &footer
		}
	}
	return retries, terminal
}
