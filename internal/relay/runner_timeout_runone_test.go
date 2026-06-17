package relay

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/progress"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

// These tests exercise the timing state machine one level up from the action
// loop: they drive the full runOne retry loop so the SHARED per-run budget and
// the per-attempt try cap are wired exactly as production wires them. The
// injectable r.timerFunc fires bounds deterministically (no real sleeps); the
// fake executor blocks until its attempt context is cancelled, so the only way a
// timed-out attempt can end is the bound the test armed. Assertions are on
// recorded outcomes and retry behaviour — never on elapsed wall time — and stay
// explicit about whether the run budget or the per-try cap won.

// blockingTimeoutExecutor returns an executor whose attempt-N behaviour is
// chosen by perAttempt: a nil entry means "block until the attempt context is
// cancelled" (i.e. let a wall-clock bound end it); a non-nil entry runs that
// function to produce a normal result. attempts counts invocations.
func blockingTimeoutExecutor(perAttempt []func(int) (*agent.TryResult, error), attempts *int32) *funcExecutor {
	return &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			n := int(atomic.AddInt32(attempts, 1))
			var behavior func(int) (*agent.TryResult, error)
			if n-1 < len(perAttempt) {
				behavior = perAttempt[n-1]
			}
			if behavior == nil {
				// Slow attempt: only returns once the timeout cancels the context,
				// mirroring how the real execute goroutine surfaces a cancelled try.
				<-ctx.Done()
				return &agent.TryResult{Completed: false}, ctx.Err()
			}
			return behavior(n)
		},
	}
}

func rateLimitErrorExecutor(attempts *int32) *funcExecutor {
	return &funcExecutor{
		fn: func(_ context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			atomic.AddInt32(attempts, 1)
			if opts.LogPath != "" {
				_ = os.WriteFile(opts.LogPath, []byte("rate limit\n"), 0o644)
			}
			return &agent.TryResult{Completed: false}, errors.New("rate limit")
		},
	}
}

// fireOnCall returns a timerFunc that fires immediately (fireNow) on the calls
// whose 1-based index is in fireCalls, and never fires otherwise. runOne builds
// the run-budget timer once (first call when RunTimeout>0) and one try-cap timer
// per attempt, so tests pick exactly which bound is armed.
func fireOnCall(fireCalls ...int) func(time.Duration) (<-chan time.Time, func() bool) {
	want := make(map[int]bool, len(fireCalls))
	for _, c := range fireCalls {
		want[c] = true
	}
	var calls int32
	return func(time.Duration) (<-chan time.Time, func() bool) {
		n := int(atomic.AddInt32(&calls, 1))
		if want[n] {
			return fireNow(), func() bool { return true }
		}
		return neverTick(), func() bool { return true }
	}
}

type boundTimerController struct {
	mu    sync.Mutex
	chans []chan time.Time
}

func (c *boundTimerController) timer(time.Duration) (<-chan time.Time, func() bool) {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.chans = append(c.chans, ch)
	c.mu.Unlock()
	return ch, func() bool { return true }
}

func (c *boundTimerController) waitForCall(t *testing.T, call int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		ready := len(c.chans) >= call
		c.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timer call %d was never created", call)
		case <-time.After(time.Millisecond):
		}
	}
}

func (c *boundTimerController) fire(t *testing.T, call int) {
	t.Helper()
	c.waitForCall(t, call)
	c.mu.Lock()
	ch := c.chans[call-1]
	c.mu.Unlock()
	select {
	case ch <- time.Now():
	default:
	}
}

func (c *boundTimerController) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.chans)
}

func waitForAttempts(t *testing.T, attempts *int32, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(attempts) < want {
		select {
		case <-deadline:
			t.Fatalf("executor reached %d attempts, want at least %d", atomic.LoadInt32(attempts), want)
		case <-time.After(time.Millisecond):
		}
	}
}

// newTimeoutTestRunner builds a runner over a real git workspace with a single
// executor wired in, suitable for driving runOne directly. The console writer is
// discarded so footer/monitor output does not pollute test logs.
func newTimeoutTestRunner(t *testing.T, exec agent.Executor, cfg Config) (*Runner, *store.Store, string) {
	t.Helper()
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initRepo(t, workspaceDir)
	runGit(t, workspaceDir, "commit", "--allow-empty", "-m", "initial", "--no-verify")

	s := newTestStore(t, rallyDir)
	cfg.WorkspaceDir = workspaceDir
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.Resolver == nil {
		cfg.Resolver = cheapTestResolver
	}
	r := NewRunner(s, cfg, map[string]agent.Executor{"opencode": exec})
	r.out = io.Discard
	return r, s, workspaceDir
}

func runTimeoutTask() runTask {
	// A non-laps task: finalize is implicit, so an attempt that completes with a
	// file change resolves to success without needing laps bookkeeping.
	return runTask{Name: "task", Prompt: "do work", Assignee: "senior", ResolvedRoute: "senior"}
}

func runTimeoutLapsTask() runTask {
	return runTask{
		Name:          "lap task",
		Prompt:        "do work",
		Assignee:      "senior",
		ResolvedRoute: "senior",
		LapID:         "lap-timeout",
		IsLapsBacked:  true,
	}
}

func driveRunOne(t *testing.T, r *Runner) runOutcome {
	t.Helper()
	return driveRunOneTask(t, r, runTimeoutTask())
}

func driveRunOneTask(t *testing.T, r *Runner, task runTask) runOutcome {
	t.Helper()
	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		task,
		nil, nil, false, false, nil, nil,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	return res
}

func driveRunOneAsync(t *testing.T, r *Runner) <-chan runOutcome {
	t.Helper()
	done := make(chan runOutcome, 1)
	go func() { done <- driveRunOne(t, r) }()
	return done
}

func driveRunOneTaskAsync(t *testing.T, r *Runner, task runTask) <-chan runOutcome {
	t.Helper()
	done := make(chan runOutcome, 1)
	go func() { done <- driveRunOneTask(t, r, task) }()
	return done
}

func awaitRunOne(t *testing.T, done <-chan runOutcome) runOutcome {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(2 * time.Second):
		t.Fatal("runOne did not return")
		return runOutcome{}
	}
}

func TestRunOneTryTimeoutAtOrAboveRunBudgetIsSubsumed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tryTimeout time.Duration
	}{
		{name: "try equals run", tryTimeout: time.Hour},
		{name: "try greater than run", tryTimeout: 2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts int32
			exec := blockingTimeoutExecutor(nil, &attempts)
			r, s, _ := newTimeoutTestRunner(t, exec, Config{
				RetryBudget: 3,
				RunTimeout:  time.Hour,
				TryTimeout:  tc.tryTimeout,
			})
			timers := &boundTimerController{}
			r.timerFunc = timers.timer

			done := driveRunOneAsync(t, r)
			waitForAttempts(t, &attempts, 1)
			timers.fire(t, 1)

			res := awaitRunOne(t, done)

			if res.Success {
				t.Fatal("run-budget exhaustion must not resolve as success")
			}
			if res.Outcome != reliability.OutcomeHandoffTimeout {
				t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffTimeout)
			}
			if got := timers.callCount(); got != 1 {
				t.Fatalf("timer calls = %d, want 1 shared run-budget timer only; try cap must be subsumed", got)
			}
			if got := atomic.LoadInt32(&attempts); got != 1 {
				t.Fatalf("executor ran %d attempts, want 1", got)
			}
			tries := s.AllTries()
			if len(tries) != 1 {
				t.Fatalf("recorded %d tries, want 1", len(tries))
			}
			if tries[0].FailReason != "run timeout; harness cannot resume for handoff" {
				t.Fatalf("try fail reason = %q, want no-resume run timeout", tries[0].FailReason)
			}
		})
	}
}

func TestRunOneRunBudgetExpiredBeforeCooldownStopsRetries(t *testing.T) {
	var attempts int32
	exec := rateLimitErrorExecutor(&attempts)
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 3,
		RunTimeout:  time.Nanosecond,
	})
	r.timerFunc = func(time.Duration) (<-chan time.Time, func() bool) {
		return neverTick(), func() bool { return true }
	}
	r.sleepFunc = func(time.Duration) {
		t.Fatal("run budget expiry must not sleep for cooldown")
	}

	res := driveRunOne(t, r)

	if res.Success {
		t.Fatal("expired run budget must not resolve as success")
	}
	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffTimeout)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor ran %d attempts, want 1: expired run budget must stop before retry", got)
	}
	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("recorded %d tries, want 1", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("try outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeHandoffTimeout)
	}
}

// TestRunOneRunBudgetStopsRetries pins task 3.5's "cumulative retry time crossing
// run_timeout_secs": the shared per-run budget fires during the first attempt and
// must stop the run from retrying. The executor never completes on its own, so
// only the run budget can end it. With no resumable session, task 4.3 resolves
// that same implementation try as handoff_timeout rather than fabricating a
// handoff-only continuation.
func TestRunOneRunBudgetStopsRetries(t *testing.T) {
	var attempts int32
	exec := blockingTimeoutExecutor(nil, &attempts) // every attempt blocks
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 3,
		RunTimeout:  time.Hour, // positive so the budget timer is built; fake fires it
	})
	// Only the run-budget timer is built (TryTimeout=0), so call #1 is it.
	r.timerFunc = fireOnCall(1)

	res := driveRunOne(t, r)

	if res.Success {
		t.Fatal("run-budget exhaustion must not resolve as success")
	}
	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want %q (no resumable handoff)", res.Outcome, reliability.OutcomeHandoffTimeout)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0 for timeout lifecycle outcome", res.InfraFailures)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor ran %d attempts, want 1: run-budget exhaustion must stop retries", got)
	}
	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("recorded %d tries, want 1 (no retry after run budget)", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("try outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeHandoffTimeout)
	}
	if tries[0].FailReason != "run timeout; harness cannot resume for handoff" {
		t.Fatalf("try fail reason = %q, want no-resume reason", tries[0].FailReason)
	}
	if tries[0].Completed {
		t.Error("a run-timeout try must not be marked completed")
	}
}

// TestRunOneRunBudgetIsCumulativeAcrossRetries pins task 3.5's "cumulative
// retry time crossing run_timeout_secs": attempt 1 is cancelled by the per-try
// cap and retried, then the original shared run-budget channel fires during
// attempt 2. If runOne accidentally rebuilt the run-budget timer per attempt,
// this test would not be able to end attempt 2 by firing call #1.
func TestRunOneRunBudgetIsCumulativeAcrossRetries(t *testing.T) {
	var attempts int32
	exec := blockingTimeoutExecutor(nil, &attempts) // every attempt blocks
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 3,
		RunTimeout:  2 * time.Hour,
		TryTimeout:  time.Hour,
	})
	timers := &boundTimerController{}
	r.timerFunc = timers.timer

	done := driveRunOneAsync(t, r)

	// Timer call order is: #1 shared run budget, #2 attempt 1 try cap.
	timers.waitForCall(t, 2)
	timers.fire(t, 2)
	waitForAttempts(t, &attempts, 2)
	// Attempt 2 builds only a fresh try cap (#3). Firing #1 proves the run
	// budget did not reset across the retry.
	timers.fire(t, 1)

	res := awaitRunOne(t, done)

	if res.Success {
		t.Fatal("cumulative run-budget exhaustion must not resolve as success")
	}
	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want %q (shared run budget won after retry, no resume)", res.Outcome, reliability.OutcomeHandoffTimeout)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0 for timeout lifecycle outcome", res.InfraFailures)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("executor ran %d attempts, want 2: first try cap retries, then run budget stops", got)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("recorded %d tries, want 2", len(tries))
	}
	if tries[0].FailReason != "try timeout" {
		t.Fatalf("try 1 fail reason = %q, want %q", tries[0].FailReason, "try timeout")
	}
	if tries[1].FailReason != "run timeout; harness cannot resume for handoff" {
		t.Fatalf("try 2 fail reason = %q, want no-resume reason", tries[1].FailReason)
	}
	if tries[1].Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("try 2 outcome = %q, want %q", tries[1].Outcome, reliability.OutcomeHandoffTimeout)
	}
}

// TestRunOneTryCapRetriesWithinBudget pins task 3.5's "a single attempt crossing
// try_timeout_secs while run budget remains": the per-try cap ends attempt 1, but
// because the run budget is not exhausted the run retries and the second attempt
// succeeds. We assert two tries — the first a "try timeout", the second a normal
// completion — and overall run success.
func TestRunOneTryCapRetriesWithinBudget(t *testing.T) {
	var attempts int32
	var workspaceDir string
	complete := func(n int) (*agent.TryResult, error) {
		// Make a file change so the completed attempt has visible work and does
		// not trip the "no changes made" guard.
		if err := os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("done\n"), 0o644); err != nil {
			return nil, err
		}
		return &agent.TryResult{Completed: true, Summary: "second attempt ok"}, nil
	}
	// Attempt 1 blocks (nil → capped by the try timer); attempt 2 completes.
	exec := blockingTimeoutExecutor([]func(int) (*agent.TryResult, error){nil, complete}, &attempts)

	r, s, ws := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 3,
		TryTimeout:  time.Hour, // per-attempt cap built fresh each attempt
		// RunTimeout=0: the run budget is disabled, so a try cap leaves budget.
	})
	workspaceDir = ws
	// No run-budget timer (RunTimeout=0). Call #1 is attempt 1's try cap (fire),
	// call #2 is attempt 2's try cap (never), so attempt 2 runs free to completion.
	r.timerFunc = fireOnCall(1)

	res := driveRunOne(t, r)

	if !res.Success {
		t.Fatalf("expected run success after retry within budget, got %+v", res)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("executor ran %d attempts, want 2: a try cap must leave budget for retry", got)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("recorded %d tries, want 2", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeRunTimeout {
		t.Fatalf("try 1 outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeRunTimeout)
	}
	if tries[0].FailReason != "try timeout" {
		t.Fatalf("try 1 fail reason = %q, want %q (per-try cap, not run budget)", tries[0].FailReason, "try timeout")
	}
	if !tries[1].Completed {
		t.Fatalf("try 2 must be completed; got fail reason %q", tries[1].FailReason)
	}
	if tries[1].Outcome != reliability.OutcomeCompleted {
		t.Fatalf("try 2 outcome = %q, want %q", tries[1].Outcome, reliability.OutcomeCompleted)
	}
}

// TestRunOneUnderBudgetCompletesNormally pins task 3.5's "under-budget
// completion": with both bounds configured but neither firing, a normally
// completing attempt resolves as success with a single completed try and no
// timeout outcome — proving the timer wiring is inert on the happy path.
func TestRunOneUnderBudgetCompletesNormally(t *testing.T) {
	var attempts int32
	var workspaceDir string
	complete := func(n int) (*agent.TryResult, error) {
		if err := os.WriteFile(filepath.Join(workspaceDir, "work.txt"), []byte("done\n"), 0o644); err != nil {
			return nil, err
		}
		return &agent.TryResult{Completed: true, Summary: "ok"}, nil
	}
	exec := blockingTimeoutExecutor([]func(int) (*agent.TryResult, error){complete}, &attempts)

	r, s, ws := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 3,
		RunTimeout:  time.Hour,
		TryTimeout:  time.Hour,
	})
	workspaceDir = ws
	// Both bounds are built but never fire.
	r.timerFunc = fireOnCall() // nothing fires

	res := driveRunOne(t, r)

	if !res.Success {
		t.Fatalf("under-budget completion must resolve as success, got %+v", res)
	}
	if res.Outcome != reliability.OutcomeCompleted {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeCompleted)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor ran %d attempts, want 1", got)
	}
	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("recorded %d tries, want 1", len(tries))
	}
	if !tries[0].Completed {
		t.Fatalf("the single try must be completed; got fail reason %q", tries[0].FailReason)
	}
	if tries[0].Outcome != reliability.OutcomeCompleted {
		t.Fatalf("try outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeCompleted)
	}
}

// TestRunOneStallPrecedesHardTimeout pins task 3.5's stall-precedence at the
// runOne level: both hard bounds are configured but never fire, the stall
// detector reports first, and the resolving try is classified as an ordinary
// failed attempt rather than a run timeout or try timeout.
func TestRunOneStallPrecedesHardTimeout(t *testing.T) {
	oldInterval := stallCheckInterval
	stallCheckInterval = time.Millisecond
	defer func() { stallCheckInterval = oldInterval }()

	var attempts int32
	stalled := make(chan struct{})
	exec := &funcExecutor{
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			atomic.AddInt32(&attempts, 1)
			select {
			case <-stalled:
				return &agent.TryResult{Completed: false, Summary: "stalled"}, nil
			case <-ctx.Done():
				return &agent.TryResult{Completed: false}, ctx.Err()
			}
		},
	}
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 1,
		RunTimeout:  time.Hour,
		TryTimeout:  time.Hour,
	})
	r.timerFunc = fireOnCall() // hard timeouts are armed but never fire
	var closed atomic.Bool
	r.stallControllerFactory = func(string) reliability.StallController {
		return &fakeStallController{check: func(context.Context) (bool, error) {
			if closed.CompareAndSwap(false, true) {
				close(stalled)
			}
			return true, nil
		}}
	}

	res := driveRunOne(t, r)

	if res.Success {
		t.Fatal("stalled failed try must not resolve as success")
	}
	if res.Outcome != reliability.OutcomeFailed {
		t.Fatalf("run outcome = %q, want %q (stall won, not a hard timeout)", res.Outcome, reliability.OutcomeFailed)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor ran %d attempts, want 1", got)
	}
	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("recorded %d tries, want 1", len(tries))
	}
	if tries[0].FailReason == "run timeout" || tries[0].FailReason == "try timeout" {
		t.Fatalf("stall should not be recorded as hard timeout; fail reason = %q", tries[0].FailReason)
	}
	if tries[0].Outcome != reliability.OutcomeFailed {
		t.Fatalf("try outcome = %q, want %q (stall/agent failure path)", tries[0].Outcome, reliability.OutcomeFailed)
	}
}

func TestRunOneRunBudgetResumableContinuationRecordsSeparateHandoffTry(t *testing.T) {
	var attempts int32
	var workspaceDir string
	var prompts []string
	var resumeIDs []string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			n := int(atomic.AddInt32(&attempts, 1))
			prompts = append(prompts, opts.Prompt)
			resumeIDs = append(resumeIDs, opts.ResumeSessionID)
			if n == 1 {
				<-ctx.Done()
				return &agent.TryResult{Completed: false, SessionID: "sess-run-budget"}, ctx.Err()
			}
			if opts.ResumeSessionID != "sess-run-budget" {
				t.Errorf("handoff continuation ResumeSessionID = %q, want sess-run-budget", opts.ResumeSessionID)
			}
			if !strings.Contains(opts.Prompt, "Do not continue implementation") || !strings.Contains(opts.Prompt, "laps handoff") {
				t.Errorf("handoff prompt did not contain handoff-only instructions:\n%s", opts.Prompt)
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
			return &agent.TryResult{Completed: true, Summary: "handoff recorded"}, nil
		},
	}
	r, s, ws := newTimeoutTestRunner(t, exec, Config{
		RetryBudget:    1,
		RunTimeout:     time.Hour,
		HandoffTimeout: time.Hour,
		LapsEnabled:    true,
	})
	workspaceDir = ws
	r.timerFunc = fireOnCall(1)
	var stallControllers int32
	r.stallControllerFactory = func(string) reliability.StallController {
		atomic.AddInt32(&stallControllers, 1)
		return &fakeStallController{}
	}

	res := driveRunOneTask(t, r, runTimeoutLapsTask())

	if !res.Success {
		t.Fatalf("handoff continuation should resolve as success, got %+v", res)
	}
	if res.Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffRequested)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0", res.InfraFailures)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("executor ran %d attempts, want implementation + handoff continuation", got)
	}
	if got := atomic.LoadInt32(&stallControllers); got != 1 {
		t.Fatalf("stall controllers created = %d, want 1 (none for handoff-only phase)", got)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("recorded %d tries, want 2", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeRunTimeout || tries[0].HandoffOnly {
		t.Fatalf("try 1 = outcome %q handoff_only=%v, want run_timeout implementation try", tries[0].Outcome, tries[0].HandoffOnly)
	}
	if tries[1].Outcome != reliability.OutcomeHandoffRequested || !tries[1].HandoffOnly {
		t.Fatalf("try 2 = outcome %q handoff_only=%v, want handoff_requested handoff-only try", tries[1].Outcome, tries[1].HandoffOnly)
	}
	if tries[1].RunID != tries[0].RunID {
		t.Fatalf("handoff try RunID = %d, want same as implementation %d", tries[1].RunID, tries[0].RunID)
	}
	if tries[1].AttemptNumber != 2 {
		t.Fatalf("handoff try attempt = %d, want maxAttempts+1 attempt 2", tries[1].AttemptNumber)
	}
	if len(resumeIDs) != 2 || resumeIDs[0] != "" || resumeIDs[1] != "sess-run-budget" {
		t.Fatalf("ResumeSessionIDs = %v, want [\"\" \"sess-run-budget\"]", resumeIDs)
	}
	if len(prompts) != 2 || prompts[0] != "" || prompts[1] == "" {
		t.Fatalf("prompt overrides = %v, want only handoff continuation prompt override", prompts)
	}
}

func TestRunOneRunBudgetResumeSupportedWithoutSessionRecordsSingleHandoffTimeout(t *testing.T) {
	var attempts int32
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			atomic.AddInt32(&attempts, 1)
			<-ctx.Done()
			return &agent.TryResult{Completed: false}, ctx.Err()
		},
	}
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget: 1,
		RunTimeout:  time.Hour,
	})
	r.timerFunc = fireOnCall(1)

	res := driveRunOne(t, r)

	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffTimeout)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor ran %d attempts, want no synthetic handoff continuation", got)
	}
	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("recorded %d tries, want single implementation resolver", len(tries))
	}
	if tries[0].HandoffOnly {
		t.Fatal("no-session path must not fabricate a handoff-only try")
	}
	if tries[0].Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("try outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeHandoffTimeout)
	}
	if tries[0].FailReason != "run timeout; no session captured for handoff" {
		t.Fatalf("fail reason = %q, want no-session reason", tries[0].FailReason)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0", res.InfraFailures)
	}
}

func TestRunOneBoundedHandoffOnlyPartialHandoffRecordsHandoffTimeout(t *testing.T) {
	var attempts int32
	var workspaceDir string
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			n := int(atomic.AddInt32(&attempts, 1))
			if n == 1 {
				<-ctx.Done()
				return &agent.TryResult{Completed: false, SessionID: "sess-partial"}, ctx.Err()
			}
			rs, err := progress.LoadRunState(workspaceDir)
			if err != nil {
				return nil, err
			}
			rs.HandoffState = 1
			if err := progress.SaveRunState(workspaceDir, rs); err != nil {
				return nil, err
			}
			return &agent.TryResult{Completed: true, Summary: "handoff command ran but wrapup did not"}, nil
		},
	}
	r, s, ws := newTimeoutTestRunner(t, exec, Config{
		RetryBudget:    1,
		RunTimeout:     time.Hour,
		HandoffTimeout: time.Hour,
		LapsEnabled:    true,
	})
	workspaceDir = ws
	r.timerFunc = fireOnCall(1)

	res := driveRunOneTask(t, r, runTimeoutLapsTask())

	if res.Success {
		t.Fatalf("partial handoff must not resolve as success: %+v", res)
	}
	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffTimeout)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("recorded %d tries, want implementation + handoff continuation", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeRunTimeout {
		t.Fatalf("try 1 outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeRunTimeout)
	}
	if tries[1].Outcome != reliability.OutcomeHandoffTimeout || !tries[1].HandoffOnly {
		t.Fatalf("try 2 = outcome %q handoff_only=%v, want handoff_timeout handoff-only", tries[1].Outcome, tries[1].HandoffOnly)
	}
	if tries[1].FailReason != "handoff without wrapup" {
		t.Fatalf("handoff fail reason = %q, want partial/no-wrapup reason", tries[1].FailReason)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0", res.InfraFailures)
	}
}

func TestRunOneBoundedHandoffOnlyFailedContinuationRecordsHandoffTimeout(t *testing.T) {
	var attempts int32
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			n := int(atomic.AddInt32(&attempts, 1))
			if n == 1 {
				<-ctx.Done()
				return &agent.TryResult{Completed: false, SessionID: "sess-failed"}, ctx.Err()
			}
			return &agent.TryResult{Completed: false, Summary: "handoff failed"}, nil
		},
	}
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget:    1,
		RunTimeout:     time.Hour,
		HandoffTimeout: time.Hour,
		LapsEnabled:    true,
	})
	r.timerFunc = fireOnCall(1)

	res := driveRunOneTask(t, r, runTimeoutLapsTask())

	if res.Success {
		t.Fatalf("failed handoff continuation must not resolve as success: %+v", res)
	}
	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffTimeout)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("recorded %d tries, want implementation + handoff continuation", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeRunTimeout {
		t.Fatalf("try 1 outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeRunTimeout)
	}
	if tries[1].Outcome != reliability.OutcomeHandoffTimeout || !tries[1].HandoffOnly {
		t.Fatalf("try 2 = outcome %q handoff_only=%v, want handoff_timeout handoff-only", tries[1].Outcome, tries[1].HandoffOnly)
	}
	if tries[1].FailReason != "handoff not completed" {
		t.Fatalf("handoff fail reason = %q, want handoff not completed", tries[1].FailReason)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0", res.InfraFailures)
	}
}

func TestRunOneBoundedHandoffOnlyTimeoutRecordsHandoffTimeout(t *testing.T) {
	var attempts int32
	exec := &funcExecutor{
		resumeSupported: true,
		fn: func(ctx context.Context, opts agent.RunOptions) (*agent.TryResult, error) {
			n := int(atomic.AddInt32(&attempts, 1))
			<-ctx.Done()
			if n == 1 {
				return &agent.TryResult{Completed: false, SessionID: "sess-timeout"}, ctx.Err()
			}
			return &agent.TryResult{Completed: false, Summary: "handoff timed out"}, ctx.Err()
		},
	}
	r, s, _ := newTimeoutTestRunner(t, exec, Config{
		RetryBudget:    1,
		RunTimeout:     time.Hour,
		HandoffTimeout: time.Hour,
		LapsEnabled:    true,
	})
	timers := &boundTimerController{}
	r.timerFunc = timers.timer

	done := driveRunOneTaskAsync(t, r, runTimeoutLapsTask())
	timers.fire(t, 1) // run budget cancels implementation attempt
	waitForAttempts(t, &attempts, 2)
	timers.fire(t, 2) // handoff-only bound cancels continuation

	res := awaitRunOne(t, done)

	if res.Success {
		t.Fatalf("timed-out handoff continuation must fail: %+v", res)
	}
	if res.Outcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("run outcome = %q, want %q", res.Outcome, reliability.OutcomeHandoffTimeout)
	}
	tries := s.AllTries()
	if len(tries) != 2 {
		t.Fatalf("recorded %d tries, want implementation + handoff continuation", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeRunTimeout {
		t.Fatalf("try 1 outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeRunTimeout)
	}
	if tries[1].Outcome != reliability.OutcomeHandoffTimeout || !tries[1].HandoffOnly {
		t.Fatalf("try 2 = outcome %q handoff_only=%v, want handoff_timeout handoff-only", tries[1].Outcome, tries[1].HandoffOnly)
	}
	if tries[1].FailReason != "handoff timeout" {
		t.Fatalf("handoff fail reason = %q, want handoff timeout", tries[1].FailReason)
	}
	if res.InfraFailures != 0 {
		t.Fatalf("infra failures = %d, want 0", res.InfraFailures)
	}
}
