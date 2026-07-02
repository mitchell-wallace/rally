package terminal

import (
	"context"
	"fmt"
	"io"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/style"
)

// Sink renders runtime events to terminal writers using the CLI's existing
// byte format.
type Sink struct {
	out io.Writer
	err io.Writer
}

var _ runtimeevent.Sink = (*Sink)(nil)

// NewSink returns a terminal event sink that writes operator-facing output to
// out and warnings to err, matching the runner's current writer targets.
func NewSink(out, err io.Writer) *Sink {
	if out == nil {
		out = io.Discard
	}
	if err == nil {
		err = io.Discard
	}
	return &Sink{out: out, err: err}
}

// Emit implements runtimeevent.Sink.
func (s *Sink) Emit(_ context.Context, event runtimeevent.Event) {
	if event == nil {
		return
	}
	switch e := event.(type) {
	case runtimeevent.RelayStarted, runtimeevent.RelayCompleted:
		// Data-only lifecycle markers. app.StartRelay remains the owner of the
		// literal "Relay complete." line.
		return
	case runtimeevent.RouteWarning:
		fmt.Fprintln(s.err, e.Message)
	case runtimeevent.TaskFileWarning:
		fmt.Fprintln(s.err, e.Message)
	case runtimeevent.RunHeaderReady:
		fmt.Fprintln(s.out, style.RenderHeader(headerOptions(e)))
	case runtimeevent.TryStatusSnapshot:
		fmt.Fprintf(s.out, "\r\x1b[2K%s\n", e.Status)
	case runtimeevent.ShortcutHintReady:
		fmt.Fprintf(s.out, "\r\x1b[2K%s\n", style.ShortcutHint())
	case runtimeevent.RetryFooterUpdated:
		renderRunFooter(s.out, footerOptions(e.FooterData))
	case runtimeevent.AttemptFinished:
		renderRunFooter(s.out, footerOptions(e.FooterData))
	case runtimeevent.AttemptCancelled:
		renderRunFooter(s.out, footerOptions(e.FooterData))
	case runtimeevent.HandoffAttemptFinished:
		renderRunFooter(s.out, footerOptions(e.FooterData))
	case runtimeevent.RateLimitWaitStarted:
		fmt.Fprintln(s.out, style.DimStyle.Render(fmt.Sprintf("waiting %v for rate limit...", e.Wait)))
	case runtimeevent.WaitStarted:
		renderWaitFrame(s.out, e.Message, e.Hint)
	case runtimeevent.WaitTick:
		renderWaitFrame(s.out, e.Message, e.Hint)
	case runtimeevent.WaitFinished:
		fmt.Fprint(s.out, "\r\x1b[J")
	case runtimeevent.OperatorActionArmed, runtimeevent.OperatorActionApplied:
		// Wait-loop feedback is rendered by the following WaitTick frame; active
		// try feedback remains monitor-indicator-driven during this change.
		return
	case runtimeevent.PausePromptShown:
		fmt.Fprintln(s.out, e.Message)
	case runtimeevent.RelaySummaryReady:
		fmt.Fprintln(s.out, style.RenderSummary(e.TotalRuns, e.Passed, e.Failed, e.TotalDuration, e.Cancelled))
	}
}

func renderRunFooter(out io.Writer, opts style.FooterOptions) {
	rendered := style.RenderFooter(opts)
	if opts.Interim {
		fmt.Fprintf(out, "\r\x1b[2K%s\r", rendered)
		return
	}
	fmt.Fprintf(out, "\r\x1b[2K%s\n", rendered)
}

func renderWaitFrame(out io.Writer, message, hintText string) {
	line := style.DimStyle.Render(message)
	hint := style.ShortcutHint()
	if hintText != "" {
		hint = style.WarningStyle.Render("⌨ " + hintText)
	}
	fmt.Fprintf(out, "\r\x1b[J%s\r\n%s\x1b[1A\r", line, hint)
}

func headerOptions(e runtimeevent.RunHeaderReady) style.HeaderOptions {
	return style.HeaderOptions{
		RunIndex:     e.RunIndex,
		TotalRuns:    e.TotalRuns,
		AgentName:    e.AgentName,
		Attempt:      e.Attempt,
		StartTime:    e.StartTime,
		IsLapsBacked: e.IsLapsBacked,
		LapTitle:     e.LapTitle,
		LapsStarted:  e.LapsStarted,
		LapsTotal:    e.LapsTotal,
		Model:        e.Model,
		RoleLabel:    e.RoleLabel,
	}
}

func footerOptions(e runtimeevent.FooterData) style.FooterOptions {
	return style.FooterOptions{
		Passed:             e.Passed,
		Cancelled:          e.Cancelled,
		Duration:           e.Duration,
		FilesChanged:       e.FilesChanged,
		CommitHash:         e.CommitHash,
		CommitTitle:        e.CommitTitle,
		FailReason:         e.FailReason,
		CancellationSource: e.CancellationSource,
		Interim:            e.Interim,
		Attempt:            e.Attempt,
		MaxAttempts:        e.MaxAttempts,
	}
}
