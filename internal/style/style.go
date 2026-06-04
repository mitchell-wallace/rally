package style

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// PrintLine writes s to w followed by a newline, but only when s contains
// non-whitespace. This avoids leaking blank lines into the console when a
// caller has no real content (e.g. an empty status string from a renderer).
func PrintLine(w io.Writer, s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	fmt.Fprintln(w, s)
}

// shortcutHintStyle is a medium grey applied to the keyboard hint line that
// sits under the try header. It's a touch lighter than the dim separators so
// the hints stay readable while clearly being chrome, not content.
var shortcutHintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

var shortcutHintTiers = []string{
	"[Ctrl+S skip] [Ctrl+P pause] [Ctrl+X stop] [Ctrl+C quit]",
	"[^S skip] [^P pause] [^X stop] [^C quit]",
	"^S skip · ^P pause · ^X stop · ^C quit",
	"^S·^P·^X·^C",
}

func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

func shortcutHintForWidth(width int) string {
	for _, tier := range shortcutHintTiers {
		if lipgloss.Width(tier) <= width {
			return shortcutHintStyle.Render(tier)
		}
	}
	return shortcutHintStyle.Render(shortcutHintTiers[len(shortcutHintTiers)-1])
}

func ShortcutHint() string {
	return shortcutHintForWidth(terminalWidth())
}

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

func subSeparator() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", separatorWidth))
}

// HeaderOptions carries parameters for rendering a run header.
type HeaderOptions struct {
	RunIndex     int
	TotalRuns    int
	AgentName    string
	Attempt      int
	StartTime    time.Time
	IsLapsBacked bool
	LapTitle     string
	LapsStarted  int
	LapsTotal    int
	Model        string
}

// RenderHeader renders a try header with separator lines, agent name, run index,
// attempt number, and start time. When LapsTotal > 0 a `laps: X/Y` line is
// appended; when Model is set a `model: <model>` line is appended.
func RenderHeader(opts HeaderOptions) string {
	timeStr := opts.StartTime.Local().Format("15:04")
	var label string

	if opts.IsLapsBacked {
		attemptStr := ""
		if opts.Attempt > 1 {
			attemptStr = fmt.Sprintf(" (attempt %d)", opts.Attempt)
		}
		title := opts.LapTitle
		if title == "" {
			title = "Untitled task"
		}
		label = fmt.Sprintf("%s%s — %s — started %s", opts.AgentName, attemptStr, title, timeStr)
	} else {
		if opts.Attempt > 1 {
			label = fmt.Sprintf("[%d/%d] %s (attempt %d) — started %s", opts.RunIndex+1, opts.TotalRuns, opts.AgentName, opts.Attempt, timeStr)
		} else {
			label = fmt.Sprintf("[%d/%d] %s — started %s", opts.RunIndex+1, opts.TotalRuns, opts.AgentName, timeStr)
		}
	}

	var sb strings.Builder
	// Leading newline separates this header from prior output for readability.
	sb.WriteString("\n")
	sb.WriteString(separator())
	sb.WriteString("\n")
	sb.WriteString("  ")
	sb.WriteString(label)
	sb.WriteString("\n")
	sb.WriteString(subSeparator())

	if opts.LapsTotal > 0 {
		sb.WriteString("\n  ")
		sb.WriteString(DimStyle.Render(fmt.Sprintf("laps: %d/%d", opts.LapsStarted, opts.LapsTotal)))
	}
	if strings.TrimSpace(opts.Model) != "" {
		sb.WriteString("\n  ")
		sb.WriteString(DimStyle.Render(fmt.Sprintf("model: %s", opts.Model)))
	}
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
