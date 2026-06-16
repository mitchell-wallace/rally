package relay

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
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
	return runTask{Name: "task", Prompt: "do work", Assignee: "senior"}
}

func driveRunOne(t *testing.T, r *Runner) runOutcome {
	t.Helper()
	res, err := r.runOne(
		context.Background(),
		&store.RelayRecord{ID: 1, TargetIterations: 1},
		0,
		agent.ResolvedAgent{Harness: "opencode", Model: cheapTestModel},
		runTimeoutTask(),
		nil, nil, false, false, nil, nil,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runOne error = %v", err)
	}
	return res
}

// TestRunOneRunBudgetStopsRetries pins task 3.5's "cumulative retry time crossing
// run_timeout_secs": the shared per-run budget fires during the first attempt and
// must stop the run from retrying. The executor never completes on its own, so
// only the run budget can end it; we assert a single recorded try labelled "run
// timeout" rather than the full retry budget being burned.
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
	if res.Outcome != reliability.OutcomeRunTimeout {
		t.Fatalf("run outcome = %q, want %q (run budget won)", res.Outcome, reliability.OutcomeRunTimeout)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("executor ran %d attempts, want 1: run-budget exhaustion must stop retries", got)
	}
	tries := s.AllTries()
	if len(tries) != 1 {
		t.Fatalf("recorded %d tries, want 1 (no retry after run budget)", len(tries))
	}
	if tries[0].Outcome != reliability.OutcomeRunTimeout {
		t.Fatalf("try outcome = %q, want %q", tries[0].Outcome, reliability.OutcomeRunTimeout)
	}
	if tries[0].FailReason != "run timeout" {
		t.Fatalf("try fail reason = %q, want %q (run budget, not try cap)", tries[0].FailReason, "run timeout")
	}
	if tries[0].Completed {
		t.Error("a run-timeout try must not be marked completed")
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
