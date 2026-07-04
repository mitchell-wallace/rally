package tuicore

import (
	"fmt"
	"strings"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/style"
)

// Transcript reduces runtime events into completed transcript lines plus live
// tail lines that a TUI renders beneath the scrollback.
type Transcript struct {
	lines []string

	interimLine string
	waitMessage string
	waitHint    string
	pauseLine   string

	shortcutHintRequested bool
	relayID               int
	targetIterations      int
	agentMix              string
	relayCompleted        bool
	armedActionHint       string
	appliedActionHint     string
}

// Apply folds one runtime event into the transcript state.
func (t *Transcript) Apply(e runtimeevent.Event) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case runtimeevent.RelayStarted:
		t.relayID = e.RelayID
		t.targetIterations = e.TargetIterations
		t.agentMix = e.AgentMix
	case runtimeevent.RelayCompleted:
		t.relayID = e.RelayID
		t.relayCompleted = true
	case runtimeevent.RouteWarning:
		t.appendLine(e.Message)
	case runtimeevent.TaskFileWarning:
		t.appendLine(e.Message)
	case runtimeevent.OutingHeaderReady:
		t.appendBlock(style.RenderHeader(headerOptions(e)))
	case runtimeevent.TryStatusSnapshot:
		t.appendLine(e.Status)
	case runtimeevent.ShortcutHintReady:
		t.shortcutHintRequested = true
	case runtimeevent.RetryFooterUpdated:
		t.interimLine = style.RenderFooter(footerOptions(e.FooterData))
	case runtimeevent.AttemptFinished:
		t.appendFinalFooter(e.FooterData)
	case runtimeevent.AttemptCancelled:
		t.appendFinalFooter(e.FooterData)
	case runtimeevent.HandoffAttemptFinished:
		t.appendFinalFooter(e.FooterData)
	case runtimeevent.RateLimitWaitStarted:
		t.appendLine(style.DimStyle.Render(fmt.Sprintf("waiting %v for rate limit...", e.Wait)))
	case runtimeevent.WaitStarted:
		t.waitMessage = e.Message
		t.waitHint = e.Hint
	case runtimeevent.WaitTick:
		t.waitMessage = e.Message
		t.waitHint = e.Hint
	case runtimeevent.WaitFinished:
		t.waitMessage = ""
		t.waitHint = ""
	case runtimeevent.OperatorActionArmed:
		t.armedActionHint = e.Message
	case runtimeevent.OperatorActionApplied:
		t.appliedActionHint = e.Message
	case runtimeevent.PausePromptShown:
		t.pauseLine = e.Message
	case runtimeevent.RelaySummaryReady:
		t.appendBlock(style.RenderSummary(e.TotalOutings, e.Passed, e.Failed, e.TotalDuration, e.Cancelled))
	}
}

// Lines returns a copy of completed transcript lines.
func (t *Transcript) Lines() []string {
	return append([]string(nil), t.lines...)
}

// LiveTail returns the current live tail lines in display order.
func (t *Transcript) LiveTail() []string {
	var out []string
	if t.interimLine != "" {
		out = append(out, splitBlock(t.interimLine)...)
	}
	if t.waitMessage != "" {
		out = append(out, style.DimStyle.Render(t.waitMessage))
		if t.waitHint != "" {
			out = append(out, style.WarningStyle.Render("⌨ "+t.waitHint))
		} else {
			out = append(out, style.ShortcutHint())
		}
	}
	if t.pauseLine != "" {
		out = append(out, t.pauseLine)
	}
	return out
}

// ShortcutHintRequested reports whether the event stream requested shortcut
// chrome that the TUI should render in its own hint bar.
func (t *Transcript) ShortcutHintRequested() bool {
	return t.shortcutHintRequested
}

// RelayMeta returns the latest relay lifecycle metadata.
func (t *Transcript) RelayMeta() (relayID, targetIterations int, agentMix string) {
	return t.relayID, t.targetIterations, t.agentMix
}

// RelayCompleted reports whether the relay lifecycle has completed.
func (t *Transcript) RelayCompleted() bool {
	return t.relayCompleted
}

// ArmedActionHint returns the last operator action arm hint.
func (t *Transcript) ArmedActionHint() string {
	return t.armedActionHint
}

// AppliedActionHint returns the last operator action applied hint.
func (t *Transcript) AppliedActionHint() string {
	return t.appliedActionHint
}

func (t *Transcript) appendFinalFooter(data runtimeevent.FooterData) {
	t.interimLine = ""
	t.appendBlock(style.RenderFooter(footerOptions(data)))
}

func (t *Transcript) appendLine(line string) {
	t.lines = append(t.lines, line)
}

func (t *Transcript) appendBlock(block string) {
	t.lines = append(t.lines, splitBlock(block)...)
}

func splitBlock(block string) []string {
	return strings.Split(block, "\n")
}

func headerOptions(e runtimeevent.OutingHeaderReady) style.HeaderOptions {
	return style.HeaderOptions{
		OutingIndex:  e.OutingIndex,
		TotalOutings: e.TotalOutings,
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
