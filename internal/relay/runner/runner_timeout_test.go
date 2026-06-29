package runner

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/keyboard"
)

// fireNow returns a time channel that has already fired, so the corresponding
// select arm is immediately ready. Used to deterministically arm a wall-clock
// bound without any real sleep.
func fireNow() <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

// timeoutTry wires a tryCh whose only producer completes once the attempt
// context is cancelled, mirroring how the real execute goroutine surfaces a
// context-cancelled result after cancelAttempt. The returned result carries the
// captured session id so callers can confirm it survives the drain.
func timeoutTry(attemptCtx context.Context, sessionID string) <-chan tryResult {
	tryCh := make(chan tryResult, 1)
	go func() {
		<-attemptCtx.Done()
		tryCh <- tryResult{
			result: &agent.TryResult{Completed: false, SessionID: sessionID},
			err:    attemptCtx.Err(),
		}
	}()
	return tryCh
}

// TestActionLoopRunBudgetCancelsAndStopsRetries pins task 3.2/3.3: when the
// shared per-run budget fires, the loop cancels the active attempt, drains its
// result, and reports a run-budget timeout — the signal runOne consumes to stop
// retrying (the willRetry gate and the explicit attemptLoop break both test
// !runBudgetExhausted). The try only completes once cancelled, so the loop can
// only return by cancelling the attempt itself.
func TestActionLoopRunBudgetCancelsAndStopsRetries(t *testing.T) {
	r := &Runner{}
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()
	tryCh := timeoutTry(attemptCtx, "sess-run-budget")

	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         make(chan int, 1),
		actionCh:      make(chan keyboard.Press, 1),
		stallTick:     neverTick(),
		runBudgetCh:   fireNow(),
		tryDeadline:   neverTick(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           &fakeMonitor{},
		log:           io.Discard,
	})

	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action loop did not return promptly on run-budget exhaustion")
	}

	if !out.timedOut {
		t.Error("expected timedOut=true on run-budget exhaustion")
	}
	if !out.runBudgetExhausted {
		t.Error("expected runBudgetExhausted=true so the run stops retrying")
	}
	if out.actionTaken {
		t.Error("a wall-clock timeout is not an operator action; expected actionTaken=false")
	}
	if attemptCtx.Err() == nil {
		t.Error("expected the active attempt to be cancelled by the run budget")
	}
	if out.result == nil || out.result.SessionID != "sess-run-budget" {
		t.Errorf("expected the cancelled attempt's drained result, got %+v", out.result)
	}
}

// TestActionLoopTryCapCancelsButLeavesBudget pins task 3.2/3.3: a per-try cap
// firing cancels the attempt and reports a timeout WITHOUT marking the run
// budget exhausted, so runOne may start a fresh retry within remaining budget.
func TestActionLoopTryCapCancelsButLeavesBudget(t *testing.T) {
	r := &Runner{}
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()
	tryCh := timeoutTry(attemptCtx, "sess-try-cap")

	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         make(chan int, 1),
		actionCh:      make(chan keyboard.Press, 1),
		stallTick:     neverTick(),
		runBudgetCh:   neverTick(),
		tryDeadline:   fireNow(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           &fakeMonitor{},
		log:           io.Discard,
	})

	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action loop did not return promptly on per-try cap")
	}

	if !out.timedOut {
		t.Error("expected timedOut=true on per-try cap")
	}
	if out.runBudgetExhausted {
		t.Error("a per-try cap must not exhaust the run budget; retry should remain possible")
	}
	if attemptCtx.Err() == nil {
		t.Error("expected the attempt to be cancelled by the per-try cap")
	}
}

// TestActionLoopUnderBudgetCompletesNormally pins task 3.5's "a run finishing
// under budget is unaffected": with both bounds armed but never firing, a
// normally-completing try returns with no timeout flags set.
func TestActionLoopUnderBudgetCompletesNormally(t *testing.T) {
	r := &Runner{}
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()
	tryCh := make(chan tryResult, 1)
	tryCh <- tryResult{result: &agent.TryResult{Completed: true, Summary: "ok"}}

	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         make(chan int, 1),
		actionCh:      make(chan keyboard.Press, 1),
		stallTick:     neverTick(),
		runBudgetCh:   neverTick(),
		tryDeadline:   neverTick(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           &fakeMonitor{},
		log:           io.Discard,
	})

	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action loop did not return on a normal completion")
	}

	if out.timedOut || out.runBudgetExhausted {
		t.Errorf("under-budget completion must not set timeout flags: %+v", out)
	}
	if attemptCtx.Err() != nil {
		t.Error("an under-budget completion must not cancel the attempt")
	}
	if out.result == nil || !out.result.Completed {
		t.Errorf("expected the try's own completed result, got %+v", out.result)
	}
}

// TestActionLoopStallPrecedesTimeout pins task 3.5's stall-precedence: when a
// stall is detected before either wall-clock bound is ready, the loop records
// the stall and is NOT a timeout. The timeout arms stay un-armed (neverTick) so
// the stall is unambiguously what fired first; the try then completes naturally.
func TestActionLoopStallPrecedesTimeout(t *testing.T) {
	r := &Runner{}
	tryCh := make(chan tryResult, 1)
	stallTick := make(chan time.Time, 1)
	stalledCh := make(chan struct{}, 1)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	mon := &fakeMonitor{onStalled: func(v bool) {
		if v {
			stalledCh <- struct{}{}
		}
	}}
	sc := &fakeStallController{check: func(context.Context) (bool, error) { return true, nil }}

	done := runLoopAsync(r, actionLoopDeps{
		tryCh:           tryCh,
		pidCh:           make(chan int, 1),
		actionCh:        make(chan keyboard.Press, 1),
		stallTick:       stallTick,
		runBudgetCh:     neverTick(),
		tryDeadline:     neverTick(),
		attemptCtx:      attemptCtx,
		cancelAttempt:   cancelAttempt,
		stallController: sc,
		mon:             mon,
		log:             io.Discard,
	})

	// Drive the stall before any timeout could fire.
	stallTick <- time.Now()
	select {
	case <-stalledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("stall was never detected")
	}

	// The stalled attempt then completes on its own.
	tryCh <- tryResult{result: &agent.TryResult{Completed: false, Summary: "stalled then done"}}
	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after the stalled try finished")
	}

	if !out.stallTriggered {
		t.Error("expected stallTriggered=true when a stall precedes any timeout")
	}
	if out.timedOut {
		t.Error("a stall is not a wall-clock timeout; expected timedOut=false")
	}
}
