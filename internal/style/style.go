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

// RenderHeader renders a try header with separator lines, agent name, run index,
// attempt number, and start time.
func RenderHeader(runIndex, totalRuns int, agentName string, attempt int, startTime time.Time) string {
	timeStr := startTime.Local().Format("15:04")
	label := fmt.Sprintf("[%d/%d] %s — started %s", runIndex+1, totalRuns, agentName, timeStr)
	if attempt > 1 {
		label = fmt.Sprintf("[%d/%d] %s (attempt %d) — started %s", runIndex+1, totalRuns, agentName, attempt, timeStr)
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

// RenderFooter renders a try footer with outcome, runtime, files changed count,
// and commit hash.
func RenderFooter(passed bool, duration time.Duration, filesChanged int, commitHash string) string {
	var outcomeIcon, outcomeText string
	var outcomeStyle lipgloss.Style

	if passed {
		outcomeIcon = "✓"
		outcomeText = "passed"
		outcomeStyle = SuccessStyle
	} else {
		outcomeIcon = "✗"
		outcomeText = "failed"
		outcomeStyle = FailureStyle
	}

	outcome := outcomeStyle.Render(fmt.Sprintf("%s %s", outcomeIcon, outcomeText))
	durStr := DimStyle.Render(formatDuration(duration))
	filesStr := DimStyle.Render(fmt.Sprintf("%d file%s", filesChanged, plural(filesChanged)))

	commit := commitHash
	if commit == "" {
		commit = "—"
	}
	commitStr := DimStyle.Render(commit)

	return fmt.Sprintf("%s  │  %s  │  %s  │  %s", outcome, durStr, filesStr, commitStr)
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
