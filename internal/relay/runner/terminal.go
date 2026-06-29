package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mitchell-wallace/rally/internal/keyboard"
	"github.com/mitchell-wallace/rally/internal/style"
)

// renderRunFooter writes a per-attempt outcome to out, collapsing a burst of
// retries into one updating line. While the run still has retry budget
// (opts.Interim), it draws the neutral retry line, clearing the current line
// and parking the cursor at its start with no committed newline — so the next
// attempt's status line, or the next retry line, overwrites it in place. At a
// terminal outcome it clears any pending interim line and commits the coloured
// ✓/✗ footer block. This is the only place the per-attempt footer is emitted.
func renderRunFooter(out io.Writer, opts style.FooterOptions) {
	rendered := style.RenderFooter(opts)
	if opts.Interim {
		fmt.Fprintf(out, "\r\x1b[2K%s\r", rendered)
		return
	}
	fmt.Fprintf(out, "\r\x1b[2K%s\n", rendered)
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
func waitWithCountdown(ctx context.Context, total time.Duration, msgFmt string) (waitOutcome, error) {
	if total <= 0 {
		return waitElapsed, nil
	}

	kb := keyboard.NewKeyboard(os.Stdin, os.Stdout)
	_ = kb.SetRawMode()
	defer func() { _ = kb.Stop() }()
	kbCtx, kbCancel := context.WithCancel(ctx)
	defer kbCancel()
	actionCh := kb.Start(kbCtx)

	outcome := waitLoop(ctx, total, msgFmt, actionCh, os.Stdout, time.Second)
	if outcome == waitCancelled {
		return outcome, ctx.Err()
	}
	return outcome, nil
}

// waitLoop is the I/O-free core of [waitWithCountdown]: it ticks at
// `tickInterval`, renders the countdown + shortcut hint to `out`, and returns
// when the timer elapses, ctx is cancelled, or an action arrives on actionCh.
// Split out from waitWithCountdown for testability.
func waitLoop(ctx context.Context, total time.Duration, msgFmt string, actionCh <-chan keyboard.Press, out io.Writer, tickInterval time.Duration) waitOutcome {
	deadline := time.Now().Add(total)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var armedAction keyboard.Action
	var armedUntil time.Time
	lastFrame := ""

	render := func(remaining time.Duration) {
		if remaining < 0 {
			remaining = 0
		}
		remaining = remaining.Round(time.Second)
		line := style.DimStyle.Render(fmt.Sprintf(msgFmt, formatRemaining(remaining)))
		// The second line is normally the dim shortcut legend; while a shortcut
		// is armed it becomes a highlighted "press X again…" hint so the
		// operator sees the first press registered.
		hint := style.ShortcutHint()
		if armedAction != keyboard.ActionNone {
			if time.Now().Before(armedUntil) {
				hint = style.WarningStyle.Render("⌨ " + keyboard.ArmMessage(armedAction))
			} else {
				armedAction = keyboard.ActionNone
			}
		}
		// Only repaint when the visible frame actually changes. With a
		// minute-granularity countdown (see formatRemaining) this collapses a
		// long wait from one repaint per second to one per minute, killing the
		// scroll/noise the operator was seeing.
		frame := line + "\x00" + hint
		if frame == lastFrame {
			return
		}
		lastFrame = frame
		// \r\x1b[J clears from cursor to end of screen so a shorter countdown
		// can't leave stale characters. The \r\n (carriage return + line feed)
		// between the two lines matters in raw mode: a bare \n there only feeds
		// the line without returning the column, which is what made the hint
		// stair-step onto fresh lines. The trailing \x1b[1A\r parks the cursor
		// back at the start of the countdown line ready for the next repaint.
		fmt.Fprintf(out, "\r\x1b[J%s\r\n%s\x1b[1A\r", line, hint)
	}
	clear := func() {
		// Erase both lines and leave the cursor at the top one so subsequent
		// stdout writes land on a fresh line.
		fmt.Fprint(out, "\r\x1b[J")
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
				if press.Action != keyboard.ActionPause {
					armedAction = press.Action
					armedUntil = time.Now().Add(keyboard.ConfirmWindow)
					render(time.Until(deadline))
				}
				continue
			}
			switch press.Action {
			case keyboard.ActionSkip:
				clear()
				return waitSkipped
			case keyboard.ActionStop, keyboard.ActionQuit:
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
