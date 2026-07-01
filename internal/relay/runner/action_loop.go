package runner

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/reliability"
)

// forceKillGroup escalates the cancel drain to an immediate group-wide SIGKILL,
// routing through the injectable hook so tests can observe the escalation.
func (r *Runner) forceKillGroup(pgid int) error {
	if r.forceKillFunc != nil {
		return r.forceKillFunc(pgid)
	}
	return reliability.ForceKillProcessGroup(pgid)
}

// drainTimedOut handles a wall-clock timeout arm in the action loop: it cancels
// the running attempt, records the timeout (and whether the run budget was the
// trigger), then blocks for the cancelled attempt's result so the goroutine that
// owns tryCh does not leak. It mirrors the pause/skip drain of a single tryCh
// receive after cancelAttempt.
func (r *Runner) drainTimedOut(d actionLoopDeps, out *actionLoopResult, runBudget bool) {
	d.cancelAttempt()
	out.timedOut = true
	out.runBudgetExhausted = runBudget
	res := <-d.tryCh
	out.result = res.result
	out.execErr = res.err
}

// tryResult carries one attempt's executor outcome from the execute goroutine
// back to the action loop.
type tryResult struct {
	result *harnessapi.TryResult
	err    error
}

// actionMonitor is the slice of *monitor.Monitor the action loop drives. It is
// an interface so the loop can be tested with a fake that records calls.
type actionMonitor interface {
	SetProcessGroupID(pgid int)
	SetStalled(v bool)
	SetStopping(v bool)
	SetArmed(msg string, ttl time.Duration)
	SetActing(msg string)
}

// actionLoopDeps bundles the channels and collaborators the in-try action loop
// selects over. Splitting them out lets [Runner.runActionLoop] be driven by a
// fake executor/try channel and simulated keyboard.Action values in tests.
type actionLoopDeps struct {
	tryCh     <-chan tryResult
	pidCh     <-chan int
	actionCh  <-chan keyboard.Press
	stallTick <-chan time.Time
	// runBudgetCh fires when the per-run wall-clock budget is exhausted. It is
	// constructed ONCE before the attempt loop and the same channel is passed
	// into every runActionLoop invocation, so it measures cumulative time across
	// all retries rather than resetting per attempt. A nil channel disables the
	// run budget (blocks forever in select).
	runBudgetCh <-chan time.Time
	// tryDeadline fires when the per-attempt cap is exhausted. Unlike runBudgetCh
	// it MAY be created fresh each attempt (mirroring stallTick), so it bounds a
	// single attempt without consuming the shared run budget. A nil channel
	// disables the per-try cap.
	tryDeadline     <-chan time.Time
	attemptCtx      context.Context
	cancelAttempt   context.CancelFunc
	stallController reliability.StallController
	mon             actionMonitor
	onStall         func()
	log             io.Writer

	// Identifiers used only for log lines.
	relayID  int
	runIndex int
	attempt  int
	harness  string
}

// actionLoopResult is the outcome of one pass through the action loop.
type actionLoopResult struct {
	result         *harnessapi.TryResult
	execErr        error
	actionTaken    bool
	stallTriggered bool
	attemptPGID    int
	// timedOut is set when a wall-clock bound (run budget or per-try cap)
	// cancelled the attempt, distinguishing it from a stall, an operator action,
	// or an ordinary agent error in post-loop handling.
	timedOut bool
	// runBudgetExhausted distinguishes a run-budget timeout (stop retrying,
	// proceed to the bounded handoff) from a per-try cap firing with run budget
	// still remaining (the attempt may retry). Only meaningful when timedOut.
	runBudgetExhausted bool
	// cancellationSource records the operator-initiated cancellation that ended
	// this attempt. When non-empty, the attempt is classified as
	// OutcomeCancelled rather than failed/success, and all failure taxonomy,
	// retry scheduling, and resilience counter updates are skipped.
	cancellationSource CancellationSource
}

// CancellationSource identifies the explicit operator action that cancelled an
// attempt. It is intentionally separate from actionTaken/skipFlag/stopFlag so
// outcome derivation can honor operator intent before executor exit handling.
type CancellationSource string

const (
	CancellationSourceNone         CancellationSource = ""
	CancellationSourceSkip         CancellationSource = "skip"
	CancellationSourceGracefulStop CancellationSource = "graceful_stop"
	CancellationSourceQuitNow      CancellationSource = "quit_now"
)

func (s CancellationSource) String() string {
	return string(s)
}

// runActionLoop is the in-try select-based state machine. It blocks until the
// attempt resolves on tryCh, or an operator action redirects it:
//
//   - Ctrl+S (skip): cancel the attempt, record source=skip, then drain tryCh.
//   - Ctrl+P (pause): cancel the attempt, then drain tryCh.
//   - Ctrl+X (stop): cancel the attempt, record source=graceful_stop, drain
//     tryCh, and halt the relay afterwards.
//   - Ctrl+C (quit now): set stopFlag, cancel the attempt immediately, and drain
//     tryCh while staying responsive — a second Ctrl+C within the grace window
//     escalates to a group-wide SIGKILL via forceKillGroup rather than waiting
//     the window out.
//
// While stop/quit drains, the monitor shows a "stopping…" indicator so the UI
// does not look frozen. Stall detection (stallTick) and late pid reports
// (pidCh) are handled alongside the action cases.
//
// Two wall-clock bounds join the select as their own arms, mirroring stallTick:
// runBudgetCh (the shared per-run budget across retries) and tryDeadline (the
// per-attempt cap). On fire either cancels the attempt, marks the result
// timed-out, drains tryCh, and breaks — so whichever of run budget, per-try cap,
// or stall fires first wins.
func (r *Runner) runActionLoop(d actionLoopDeps) actionLoopResult {
	var out actionLoopResult
actionLoop:
	for {
		select {
		case res := <-d.tryCh:
			out.result = res.result
			out.execErr = res.err
			break actionLoop
		case <-d.runBudgetCh:
			// Per-run budget exhausted: cancel the active attempt, mark it a
			// run-budget timeout so the loop stops retrying and proceeds to the
			// bounded handoff, then drain the cancelled attempt's result.
			r.drainTimedOut(d, &out, true)
			break actionLoop
		case <-d.tryDeadline:
			// Per-try cap exhausted with run budget possibly remaining: cancel
			// the attempt and mark it a (non-run-budget) timeout; the loop may
			// start a fresh retry if budget and retries remain.
			r.drainTimedOut(d, &out, false)
			break actionLoop
		case pid := <-d.pidCh:
			out.attemptPGID = pid
			d.mon.SetProcessGroupID(pid)
			if d.stallController != nil {
				d.stallController.SetProcessGroupID(pid)
			}
		case <-d.stallTick:
			if d.stallController == nil || out.stallTriggered {
				continue
			}
			stalled, err := d.stallController.Check(d.attemptCtx)
			if err != nil {
				fmt.Fprintf(d.log, "relay %d run %d attempt %d stall check warning: %v\n", d.relayID, d.runIndex+1, d.attempt, err)
				continue
			}
			if !stalled {
				continue
			}
			out.stallTriggered = true
			d.mon.SetStalled(true)
			if d.onStall != nil {
				d.onStall()
			}
			fmt.Fprintf(d.log, "relay %d run %d attempt %d stall detected for %s\n", d.relayID, d.runIndex+1, d.attempt, d.harness)
		case press := <-d.actionCh:
			if !press.Confirmed {
				// First press only arms the action: surface a "press X again"
				// hint on the live status line so the operator sees it
				// registered and what a second press will do.
				d.mon.SetArmed(keyboard.ArmMessage(press.Action), keyboard.ConfirmWindow)
				continue
			}
			switch press.Action {
			case keyboard.ActionSkip:
				d.mon.SetActing(keyboard.ActMessage(press.Action))
				d.cancelAttempt()
				r.skipFlag.Store(true)
				out.actionTaken = true
				out.cancellationSource = CancellationSourceSkip
				r.drainOperatorCancellation(d, &out)
				break actionLoop
			case keyboard.ActionPause:
				d.mon.SetActing(keyboard.ActMessage(press.Action))
				d.cancelAttempt()
				out.actionTaken = true
				res := <-d.tryCh
				out.result = res.result
				out.execErr = res.err
				break actionLoop
			case keyboard.ActionStop:
				// Graceful stop (Ctrl+X): cancel the running try, drain the
				// result, set stopFlag so the relay halts after recording the
				// cancelled attempt. Unlike the old passive-wait behaviour,
				// the attempt is cancelled immediately so the operator sees a
				// clean OutcomeCancelled record rather than whatever the agent
				// happens to be doing when the relay eventually stops.
				r.stopFlag.Store(true)
				d.cancelAttempt()
				d.mon.SetStopping(true)
				out.actionTaken = true
				out.cancellationSource = CancellationSourceGracefulStop
				r.drainOperatorCancellation(d, &out)
				break actionLoop
			case keyboard.ActionQuit:
				// Quit now (Ctrl+C): cancel the running try immediately and
				// abort the relay. cancelAttempt fires Cmd.Cancel, which sends
				// SIGINT to the process group then escalates to a group-wide
				// SIGKILL after the grace window. Mirror the pause/skip drain
				// of tryCh, but keep selecting on actionCh so a second Ctrl+C
				// within the grace window forces an immediate SIGKILL rather
				// than waiting it out. The "stopping…" monitor indicator keeps
				// the UI from looking frozen during the drain.
				r.stopFlag.Store(true)
				d.cancelAttempt()
				d.mon.SetStopping(true)
				out.actionTaken = true
				out.cancellationSource = CancellationSourceQuitNow
				r.drainOperatorCancellation(d, &out)
				break actionLoop
			}
		}
	}
	return out
}

// drainOperatorCancellation waits for the cancelled attempt's result while
// retaining the quit-now escalation path. If the operator escalates with
// Ctrl+C while a skip or graceful-stop cancellation is draining, the source is
// promoted to quit_now and the relay stop flag is set.
func (r *Runner) drainOperatorCancellation(d actionLoopDeps, out *actionLoopResult) {
drainLoop:
	for {
		select {
		case res := <-d.tryCh:
			out.result = res.result
			out.execErr = res.err
			break drainLoop
		case pid := <-d.pidCh:
			// A late OnStart can land here if the try hadn't reported its pid
			// yet; capture it so a quit escalation can target the right group.
			out.attemptPGID = pid
		case press := <-d.actionCh:
			// Only a confirmed (double-press) quit escalates the drain to an
			// immediate force-kill; a lone arming press is ignored here since the
			// drain is already cancelling.
			if press.Action != keyboard.ActionQuit || !press.Confirmed {
				continue
			}
			r.stopFlag.Store(true)
			if d.mon != nil {
				d.mon.SetStopping(true)
			}
			out.cancellationSource = CancellationSourceQuitNow
			// SIGKILL is idempotent (a re-signalled or already exited group
			// resolves to nil), so this cannot race the WaitDelay/Cmd.Cancel
			// escalation into an error.
			if out.attemptPGID > 0 {
				_ = r.forceKillGroup(out.attemptPGID)
			}
		}
	}
}
