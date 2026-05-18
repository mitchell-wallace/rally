package style

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Color scheme styles.
var (
	SuccessStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	FailureStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	WarningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	DimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	BoldStyle    = lipgloss.NewStyle().Bold(true)
)

const separatorWidth = 40

func separator() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("═", separatorWidth))
}

// HeaderOptions carries parameters for rendering a run header.
type HeaderOptions struct {
	RunIndex      int
	TotalRuns     int
	AgentName     string
	Attempt       int
	StartTime     time.Time
	IsLapsBacked  bool
	LapTitle      string
	LapsRemaining int
}

// RenderHeader renders a try header with separator lines, agent name, run index,
// attempt number, and start time.
func RenderHeader(opts HeaderOptions) string {
	timeStr := opts.StartTime.Local().Format("15:04")
	var label string
	
	if opts.IsLapsBacked {
		remStr := ""
		if opts.LapsRemaining > 0 {
			remStr = fmt.Sprintf(" (%d remaining)", opts.LapsRemaining)
		}
		attemptStr := ""
		if opts.Attempt > 1 {
			attemptStr = fmt.Sprintf(" (attempt %d)", opts.Attempt)
		}
		title := opts.LapTitle
		if title == "" {
			title = "Untitled task"
		}
		label = fmt.Sprintf("%s%s — %s%s — started %s", opts.AgentName, attemptStr, title, remStr, timeStr)
	} else {
		if opts.Attempt > 1 {
			label = fmt.Sprintf("[%d/%d] %s (attempt %d) — started %s", opts.RunIndex+1, opts.TotalRuns, opts.AgentName, opts.Attempt, timeStr)
		} else {
			label = fmt.Sprintf("[%d/%d] %s — started %s", opts.RunIndex+1, opts.TotalRuns, opts.AgentName, timeStr)
		}
	}

	var sb strings.Builder
	sb.WriteString(separator())
	sb.WriteString("\n")
	sb.WriteString("  ")
	sb.WriteString(label)
	sb.WriteString("\n")
	sb.WriteString(separator())
	return sb.String()
}

// FooterOptions carries parameters for rendering a run footer.
type FooterOptions struct {
	Passed       bool
	Duration     time.Duration
	FilesChanged int
	CommitHash   string
	CommitTitle  string
	FailReason   string
}

// RenderFooter renders a try footer with outcome, runtime, files changed count,
// and commit hash.
func RenderFooter(opts FooterOptions) string {
	var outcomeIcon, outcomeText string
	var outcomeStyle lipgloss.Style

	if opts.Passed {
		outcomeIcon = "✓"
		outcomeText = "passed"
		outcomeStyle = SuccessStyle
	} else {
		outcomeIcon = "✗"
		outcomeText = "failed"
		outcomeStyle = FailureStyle
	}

	outcome := outcomeStyle.Render(fmt.Sprintf("%s %s", outcomeIcon, outcomeText))
	durStr := DimStyle.Render(formatDuration(opts.Duration))
	filesStr := DimStyle.Render(fmt.Sprintf("%d file%s", opts.FilesChanged, plural(opts.FilesChanged)))

	var extraStr string
	if opts.Passed {
		commit := opts.CommitHash
		if commit == "" {
			commit = "—"
		} else if opts.CommitTitle != "" {
			commit = fmt.Sprintf("%s (%s)", commit, opts.CommitTitle)
		}
		extraStr = DimStyle.Render(commit)
	} else {
		reason := opts.FailReason
		if reason == "" {
			reason = "—"
		}
		extraStr = DimStyle.Render(reason)
	}

	return fmt.Sprintf("%s  │  %s  │  %s  │  %s", outcome, durStr, filesStr, extraStr)
}

// RenderSummary renders a relay summary with total runs, pass/fail counts, and
// total runtime.
func RenderSummary(totalRuns, passedCount, failedCount int, totalDuration time.Duration) string {
	runsStr := fmt.Sprintf("%d run%s", totalRuns, plural(totalRuns))
	passedStr := SuccessStyle.Render(fmt.Sprintf("%d passed", passedCount))
	failedStr := FailureStyle.Render(fmt.Sprintf("%d failed", failedCount))
	durStr := DimStyle.Render(fmt.Sprintf("total %s", formatDuration(totalDuration)))

	return fmt.Sprintf("Relay complete: %s  │  %s  │  %s  │  %s", runsStr, passedStr, failedStr, durStr)
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
