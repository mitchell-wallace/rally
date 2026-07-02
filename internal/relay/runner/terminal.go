package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

// emitAttemptFooter emits the per-attempt footer event: an interim within-budget
// retry (RetryFooterUpdated) or a terminal pass/fail outcome (AttemptFinished).
// The cancelled and handoff-only footers carry their own distinct events at
// their call sites.
func (r *Runner) emitAttemptFooter(ctx context.Context, footer runtimeevent.FooterData) {
	if footer.Interim {
		r.eventSink().Emit(ctx, runtimeevent.RetryFooterUpdated{FooterData: footer})
		return
	}
	r.eventSink().Emit(ctx, runtimeevent.AttemptFinished{FooterData: footer})
}

// waitOutcome enumerates how a waitWithCountdown call ended.
type waitOutcome int

const (
	waitElapsed   waitOutcome = iota // timer ran out normally
	waitSkipped                      // user pressed Ctrl+S (skip) to bail out early
	waitStopped                      // user pressed Ctrl+X / Ctrl+C to abort the relay
	waitCancelled                    // ctx was cancelled (returns ctx.Err alongside)
)

// waitWithCountdown blocks for `total`, redrawing a one-line countdown +
// shortcut hint on stdout once per second. See [waitLoop] for the core logic;
// this wrapper handles the keyboard, terminal raw mode, and stdout rendering.
func waitWithCountdown(ctx context.Context, sink runtimeevent.Sink, controls runtimeevent.ControlSource, total time.Duration, msgFmt string) (waitOutcome, error) {
	if total <= 0 {
		return waitElapsed, nil
	}

	var actionCh <-chan runtimeevent.Press
	if controls != nil {
		var err error
		actionCh, err = controls.Start(ctx)
		if err != nil {
			return waitCancelled, err
		}
		defer controls.Stop()
	}

	outcome := waitLoop(ctx, sink, total, msgFmt, actionCh, time.Second)
	if outcome == waitCancelled {
		return outcome, ctx.Err()
	}
	return outcome, nil
}

// waitLoop is the I/O-free core of [waitWithCountdown]: it ticks at
// `tickInterval`, emits the countdown + shortcut hint to the sink, and returns
// when the timer elapses, ctx is cancelled, or an action arrives on actionCh.
// Split out from waitWithCountdown for testability.
func waitLoop(ctx context.Context, sink runtimeevent.Sink, total time.Duration, msgFmt string, actionCh <-chan runtimeevent.Press, tickInterval time.Duration) waitOutcome {
	deadline := time.Now().Add(total)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var armedAction runtimeevent.OperatorAction
	var armedUntil time.Time
	lastFrame := ""
	started := false

	render := func(remaining time.Duration) {
		if remaining < 0 {
			remaining = 0
		}
		remaining = remaining.Round(time.Second)
		msg := fmt.Sprintf(msgFmt, formatRemaining(remaining))
		hintText := ""
		if armedAction != runtimeevent.OperatorActionNone {
			if time.Now().Before(armedUntil) {
				hintText = runtimeevent.ArmMessage(armedAction)
			} else {
				armedAction = runtimeevent.OperatorActionNone
			}
		}
		// Only repaint when the visible frame actually changes. With a
		// minute-granularity countdown (see formatRemaining) this collapses a
		// long wait from one repaint per second to one per minute, killing the
		// scroll/noise the operator was seeing.
		frame := msg + "\x00" + hintText
		if frame == lastFrame {
			return
		}
		lastFrame = frame
		if !started {
			started = true
			sink.Emit(ctx, runtimeevent.WaitStarted{Message: msg, Hint: hintText, Total: total, Remaining: remaining})
		} else {
			sink.Emit(ctx, runtimeevent.WaitTick{Message: msg, Hint: hintText, Remaining: remaining})
		}
	}
	clear := func() {
		sink.Emit(ctx, runtimeevent.WaitFinished{})
	}

	render(total)
	for {
		select {
		case <-ctx.Done():
			clear()
			return waitCancelled
		case press := <-actionCh:
			if !press.Confirmed {
				// Ignore pause arming entirely — there is no try to pause —
				// otherwise show the "press again" hint for this shortcut.
				if press.Action != runtimeevent.OperatorActionPause {
					armedAction = press.Action
					armedUntil = time.Now().Add(runtimeevent.ConfirmWindow)
					sink.Emit(ctx, runtimeevent.OperatorActionArmed{
						Action:  press.Action,
						Message: runtimeevent.ArmMessage(press.Action),
					})
					render(time.Until(deadline))
				}
				continue
			}
			switch press.Action {
			case runtimeevent.OperatorActionSkip:
				clear()
				return waitSkipped
			case runtimeevent.OperatorActionStop, runtimeevent.OperatorActionQuit:
				clear()
				return waitStopped
			}
			// Ignore a confirmed pause during a wait — there is no active try.
		case now := <-ticker.C:
			remaining := time.Until(deadline)
			if remaining <= 0 || !now.Before(deadline) {
				clear()
				return waitElapsed
			}
			render(remaining)
		}
	}
}

// formatRemaining renders d for the wait countdown. Below a minute it shows
// seconds (`Ss`); from a minute up it shows only whole minutes (`Mm` / `Hh Mm`)
// and drops the seconds component. Coarsening above a minute means the rendered
// string only changes once per minute, so the countdown repaints once a minute
// during a long wait instead of every second — the dominant noise reduction.
func formatRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second).Seconds())
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	h := total / 3600
	m := (total % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
