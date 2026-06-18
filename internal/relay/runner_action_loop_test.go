package relay

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/keyboard"
)

// fakeMonitor records the action loop's monitor calls via optional callbacks so
// tests can synchronise on UI-state transitions (stalled, stopping, pid known).
type fakeMonitor struct {
	onPGID     func(int)
	onStalled  func(bool)
	onStopping func(bool)
}

func (m *fakeMonitor) SetProcessGroupID(pgid int) {
	if m.onPGID != nil {
		m.onPGID(pgid)
	}
}
func (m *fakeMonitor) SetStalled(v bool) {
	if m.onStalled != nil {
		m.onStalled(v)
	}
}
func (m *fakeMonitor) SetStopping(v bool) {
	if m.onStopping != nil {
		m.onStopping(v)
	}
}

// runLoopAsync starts runActionLoop in a goroutine and returns a channel that
// delivers its result. Tests assert promptness by selecting on this channel
// against a deadline — no real sleeps anywhere near WaitDelay.
func runLoopAsync(r *Runner, d actionLoopDeps) <-chan actionLoopResult {
	done := make(chan actionLoopResult, 1)
	go func() { done <- r.runActionLoop(d) }()
	return done
}

// neverTick is a stall ticker channel that never fires.
func neverTick() <-chan time.Time { return make(chan time.Time) }

// TestActionLoopQuitCancelsAndAbortsWithoutWaiting pins acceptance (1): Ctrl+C
// cancels the running attempt and aborts the relay without waiting for the try
// to finish on its own. The fake try blocks until its context is cancelled, so
// the loop can only return if it cancelled the attempt first.
func TestActionLoopQuitCancelsAndAbortsWithoutWaiting(t *testing.T) {
	r := &Runner{}
	tryCh := make(chan tryResult, 1)
	actionCh := make(chan keyboard.Action, 1)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	// The try only completes once the attempt context is cancelled; if the loop
	// waited for natural completion instead of cancelling, this never fires.
	go func() {
		<-attemptCtx.Done()
		tryCh <- tryResult{result: &agent.TryResult{Completed: false}, err: attemptCtx.Err()}
	}()

	actionCh <- keyboard.ActionQuit
	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         make(chan int, 1),
		actionCh:      actionCh,
		stallTick:     neverTick(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           &fakeMonitor{},
		log:           io.Discard,
	})

	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action loop did not return promptly on Ctrl+C")
	}

	if !out.actionTaken {
		t.Error("expected actionTaken=true for quit-now")
	}
	if out.cancellationSource != CancellationSourceQuitNow {
		t.Fatalf("cancellationSource = %q, want %q", out.cancellationSource, CancellationSourceQuitNow)
	}
	if !r.stopFlag.Load() {
		t.Error("expected stopFlag set so the relay aborts")
	}
	if attemptCtx.Err() == nil {
		t.Error("expected the attempt context to be cancelled")
	}
}

// TestActionLoopStopCancelsAndDrains pins the 0.10 semantics: Ctrl+X graceful
// stop cancels the current attempt, drains it, records graceful_stop, and then
// halts the relay.
func TestActionLoopStopCancelsAndDrains(t *testing.T) {
	r := &Runner{}
	tryCh := make(chan tryResult, 1)
	actionCh := make(chan keyboard.Action, 1)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	go func() {
		<-attemptCtx.Done()
		tryCh <- tryResult{result: &agent.TryResult{Completed: false, Summary: "cancelled"}, err: attemptCtx.Err()}
	}()

	actionCh <- keyboard.ActionStop
	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         make(chan int, 1),
		actionCh:      actionCh,
		stallTick:     neverTick(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           &fakeMonitor{},
		log:           io.Discard,
	})

	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action loop did not return promptly on Ctrl+X")
	}
	if !r.stopFlag.Load() {
		t.Error("expected stopFlag set after Ctrl+X")
	}
	if attemptCtx.Err() == nil {
		t.Error("expected Ctrl+X to cancel the running attempt")
	}
	if !out.actionTaken {
		t.Error("expected actionTaken=true for Ctrl+X cancellation")
	}
	if out.cancellationSource != CancellationSourceGracefulStop {
		t.Fatalf("cancellationSource = %q, want %q", out.cancellationSource, CancellationSourceGracefulStop)
	}
	if out.result == nil || out.result.Completed {
		t.Errorf("expected the cancelled try result, got %+v", out.result)
	}
}

// TestActionLoopStalledAttemptQuitsPromptly pins acceptance (3): a stalled /
// long-running attempt ends promptly on Ctrl+C rather than waiting for the
// stall threshold to escalate. The try blocks until cancelled; the stall
// controller reports stalled, but quit-now is what ends the attempt.
func TestActionLoopStalledAttemptQuitsPromptly(t *testing.T) {
	r := &Runner{}
	tryCh := make(chan tryResult, 1)
	actionCh := make(chan keyboard.Action, 1)
	stallTick := make(chan time.Time, 1)
	stalledCh := make(chan struct{}, 1)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	go func() {
		<-attemptCtx.Done()
		tryCh <- tryResult{result: &agent.TryResult{Completed: false}, err: attemptCtx.Err()}
	}()

	mon := &fakeMonitor{onStalled: func(v bool) {
		if v {
			stalledCh <- struct{}{}
		}
	}}
	sc := &fakeStallController{check: func(context.Context) (bool, error) { return true, nil }}

	done := runLoopAsync(r, actionLoopDeps{
		tryCh:           tryCh,
		pidCh:           make(chan int, 1),
		actionCh:        actionCh,
		stallTick:       stallTick,
		attemptCtx:      attemptCtx,
		cancelAttempt:   cancelAttempt,
		stallController: sc,
		mon:             mon,
		log:             io.Discard,
	})

	// Drive a stall detection first so the attempt is "stalled" when quit lands.
	stallTick <- time.Now()
	select {
	case <-stalledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("stall was never detected")
	}

	// Ctrl+C should end the stalled attempt without waiting on any threshold.
	start := time.Now()
	actionCh <- keyboard.ActionQuit
	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled attempt did not end promptly on Ctrl+C")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("quit on a stalled attempt took %v, expected near-instant", elapsed)
	}
	if !out.stallTriggered {
		t.Error("expected stallTriggered=true so the run records the stall")
	}
	if !out.actionTaken || !r.stopFlag.Load() {
		t.Error("expected quit-now to set actionTaken and stopFlag")
	}
	if out.cancellationSource != CancellationSourceQuitNow {
		t.Fatalf("cancellationSource = %q, want %q", out.cancellationSource, CancellationSourceQuitNow)
	}
	if attemptCtx.Err() == nil {
		t.Error("expected the stalled attempt to be cancelled by quit-now")
	}
}

// TestActionLoopSecondQuitForceKills pins acceptance (4): a second Ctrl+C during
// the drain escalates straight to the force-kill path with the captured process
// group id, instead of waiting out the grace window.
func TestActionLoopSecondQuitForceKills(t *testing.T) {
	const pgid = 4242
	var killed int32
	killedCh := make(chan int, 1)
	r := &Runner{forceKillFunc: func(p int) error {
		atomic.StoreInt32(&killed, int32(p))
		killedCh <- p
		return nil
	}}

	tryCh := make(chan tryResult) // unbuffered: try stays alive through the drain
	actionCh := make(chan keyboard.Action)
	pidCh := make(chan int, 1)
	pgidKnown := make(chan struct{}, 1)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	mon := &fakeMonitor{onPGID: func(int) { pgidKnown <- struct{}{} }}

	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         pidCh,
		actionCh:      actionCh,
		stallTick:     neverTick(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           mon,
		log:           io.Discard,
	})

	// Report the pid so the loop captures the process group before the drain.
	pidCh <- pgid
	select {
	case <-pgidKnown:
	case <-time.After(2 * time.Second):
		t.Fatal("pid was never observed by the loop")
	}

	// First quit-now enters the drain; the second escalates to force-kill.
	actionCh <- keyboard.ActionQuit
	actionCh <- keyboard.ActionQuit
	select {
	case p := <-killedCh:
		if p != pgid {
			t.Errorf("force-kill targeted pgid %d, want %d", p, pgid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Ctrl+C did not escalate to the force-kill path")
	}

	// Releasing the try lets the drain (and the loop) finish.
	tryCh <- tryResult{result: &agent.TryResult{Completed: false}}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after the try drained")
	}
	if atomic.LoadInt32(&killed) != pgid {
		t.Errorf("expected force-kill of pgid %d, got %d", pgid, atomic.LoadInt32(&killed))
	}
}

// TestActionLoopPauseCapturesSessionID verifies that ActionPause cancels the
// running attempt and returns the result with its SessionID intact, so the
// outer runOneRun code can propagate it to the next attempt.
func TestActionLoopPauseCapturesSessionID(t *testing.T) {
	r := &Runner{}
	tryCh := make(chan tryResult, 1)
	actionCh := make(chan keyboard.Action, 1)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	go func() {
		<-attemptCtx.Done()
		tryCh <- tryResult{
			result: &agent.TryResult{
				Completed: false,
				Summary:   "paused mid-work",
				SessionID: "sess-pause-capture",
			},
			err: attemptCtx.Err(),
		}
	}()

	actionCh <- keyboard.ActionPause
	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         make(chan int, 1),
		actionCh:      actionCh,
		stallTick:     neverTick(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           &fakeMonitor{},
		log:           io.Discard,
	})

	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action loop did not return promptly on ActionPause")
	}

	if !out.actionTaken {
		t.Error("expected actionTaken=true for pause")
	}
	if out.result == nil {
		t.Fatal("expected result returned from pause")
	}
	if out.result.SessionID != "sess-pause-capture" {
		t.Errorf("SessionID = %q, want %q", out.result.SessionID, "sess-pause-capture")
	}
	if attemptCtx.Err() == nil {
		t.Error("expected the attempt context to be cancelled")
	}
}

// TestActionLoopSkipReturnsResultAndSetsFlag verifies that ActionSkip sets the
// skip flag and returns the attempt result. The outer runOneRun code checks
// skipFlag after actionTaken and returns without propagating session ID, so
// the next run starts fresh.
func TestActionLoopSkipReturnsResultAndSetsFlag(t *testing.T) {
	r := &Runner{}
	tryCh := make(chan tryResult, 1)
	actionCh := make(chan keyboard.Action, 1)
	attemptCtx, cancelAttempt := context.WithCancel(context.Background())
	defer cancelAttempt()

	go func() {
		<-attemptCtx.Done()
		tryCh <- tryResult{
			result: &agent.TryResult{
				Completed: false,
				Summary:   "skipped mid-work",
				SessionID: "sess-skip-discard",
			},
			err: attemptCtx.Err(),
		}
	}()

	actionCh <- keyboard.ActionSkip
	done := runLoopAsync(r, actionLoopDeps{
		tryCh:         tryCh,
		pidCh:         make(chan int, 1),
		actionCh:      actionCh,
		stallTick:     neverTick(),
		attemptCtx:    attemptCtx,
		cancelAttempt: cancelAttempt,
		mon:           &fakeMonitor{},
		log:           io.Discard,
	})

	var out actionLoopResult
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("action loop did not return promptly on ActionSkip")
	}

	if !out.actionTaken {
		t.Error("expected actionTaken=true for skip")
	}
	if out.cancellationSource != CancellationSourceSkip {
		t.Fatalf("cancellationSource = %q, want %q", out.cancellationSource, CancellationSourceSkip)
	}
	if !r.skipFlag.Load() {
		t.Error("expected skipFlag set after ActionSkip")
	}
	if out.result == nil {
		t.Fatal("expected result returned from skip")
	}
	if out.result.SessionID != "sess-skip-discard" {
		t.Errorf("SessionID = %q, want %q (preserved in result for caller inspection)", out.result.SessionID, "sess-skip-discard")
	}
}
