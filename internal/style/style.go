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
	"[Ctrl+S skip] [Ctrl+P pause] [Ctrl+X graceful stop] [Ctrl+C quit now]",
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

const maxSepWidth = 80

const labelIndent = 2

func sepWidth() int {
	w := terminalWidth()
	if w > maxSepWidth {
		return maxSepWidth
	}
	return w
}

func separatorForWidth(w int) string {
	if w < 1 {
		w = 1
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("═", w))
}

func subSeparatorForWidth(w int) string {
	if w < 1 {
		w = 1
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(strings.Repeat("─", w))
}

func truncateToVisible(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return string(runes[:1])
	}
	return string(runes[:maxRunes-1]) + "…"
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
	// RoleLabel is the routing role for the run (e.g. "verify", "JUNIOR").
	// When set, the non-laps header renders a role-first label —
	// "ROLE: harness - model - started HH:MM" — instead of a bare harness
	// name. It is ignored for laps-backed headers, which keep their
	// title + laps format.
	RoleLabel string
}

// RenderHeader renders a try header with separator lines, agent name, run index,
// attempt number, and start time. When LapsTotal > 0 a `laps: X/Y` line is
// appended; when Model is set a `model: <model>` line is appended.
func RenderHeader(opts HeaderOptions) string {
	w := sepWidth()

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
		counter := fmt.Sprintf("run: %d/%d", opts.RunIndex+1, opts.TotalRuns)
		attemptStr := ""
		if opts.Attempt > 1 {
			attemptStr = fmt.Sprintf(" (attempt %d)", opts.Attempt)
		}
		// Build the identity segment, joining harness and model with " - ".
		var parts []string
		if strings.TrimSpace(opts.AgentName) != "" {
			parts = append(parts, opts.AgentName)
		}
		if strings.TrimSpace(opts.Model) != "" {
			parts = append(parts, opts.Model)
		}
		ident := strings.Join(parts, " - ")
		// Prepend a role label when available: "ROLE: harness - model".
		if role := strings.TrimSpace(opts.RoleLabel); role != "" {
			ident = strings.ToUpper(role) + ": " + ident
		}
		label = fmt.Sprintf("%s %s%s - started %s", counter, ident, attemptStr, timeStr)
	}

	maxLabelW := w - labelIndent
	if maxLabelW < 1 {
		maxLabelW = 1
	}
	if len([]rune(label)) > maxLabelW {
		label = truncateToVisible(label, maxLabelW)
	}

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(separatorForWidth(w))
	sb.WriteString("\n")
	sb.WriteString("  ")
	sb.WriteString(label)
	sb.WriteString("\n")
	sb.WriteString(subSeparatorForWidth(w))

	if opts.LapsTotal > 0 {
		sb.WriteString("\n  ")
		sb.WriteString(DimStyle.Render(fmt.Sprintf("laps: %d/%d", opts.LapsStarted, opts.LapsTotal)))
	}
	// The model lives on its own dim line only for laps-backed headers,
	// which keep their title + laps format. Non-laps headers fold the model
	// into the role label line above.
	if opts.IsLapsBacked && strings.TrimSpace(opts.Model) != "" {
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

	// Interim marks a within-budget retry: instead of the coloured terminal
	// block, RenderFooter emits a single neutral (dim) line suitable for
	// in-place redraw, so a run that retries several times shows one updating
	// line rather than one red footer per attempt. The coloured ✓/✗ block is
	// reserved for the terminal outcome (Interim == false).
	Interim bool
	// Attempt and MaxAttempts annotate the terminal outcome ("passed on try
	// N/M", "failed after K tries") and the interim line ("retrying N/M").
	// Both zero keeps the bare "passed"/"failed" outcome text.
	Attempt     int
	MaxAttempts int
}

// RenderFooter renders a try footer. For a terminal outcome (Interim == false)
// it emits the three-line block with a coloured ✓/✗ outcome, runtime, files
// changed count, and commit hash. For an interim within-budget retry
// (Interim == true) it emits a single neutral line — `↻ retrying N/M · last:
// <reason> (<dur>, <files>)` — that the caller redraws in place.
func RenderFooter(opts FooterOptions) string {
	if opts.Interim {
		return renderRetryLine(opts)
	}

	w := sepWidth()

	var outcomeIcon, outcomeText string
	var outcomeStyle lipgloss.Style

	if opts.Passed {
		outcomeIcon = "✓"
		outcomeText = "passed"
		if opts.Attempt > 1 {
			outcomeText = fmt.Sprintf("passed on try %d/%d", opts.Attempt, opts.MaxAttempts)
		}
		outcomeStyle = SuccessStyle
	} else {
		outcomeIcon = "✗"
		outcomeText = "failed"
		if opts.Attempt > 1 {
			outcomeText = fmt.Sprintf("failed after %d %s", opts.Attempt, tries(opts.Attempt))
		}
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

	var sb strings.Builder
	sb.WriteString(subSeparatorForWidth(w))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("%s  │  %s  │  %s  │  %s", outcome, durStr, filesStr, extraStr))
	sb.WriteString("\n")
	sb.WriteString(separatorForWidth(w))
	return sb.String()
}

// renderRetryLine renders the single neutral line shown while a run is retrying
// within budget. It is entirely dim — no green/red — because the attempt is not
// a terminal outcome; only the final footer is coloured.
func renderRetryLine(opts FooterOptions) string {
	reason := opts.FailReason
	if reason == "" {
		reason = "—"
	}
	line := fmt.Sprintf("↻ retrying %d/%d · last: %s (%s, %d file%s)",
		opts.Attempt, opts.MaxAttempts, reason,
		formatDuration(opts.Duration), opts.FilesChanged, plural(opts.FilesChanged))
	return DimStyle.Render(line)
}

// tries returns the noun for a try count: "try" for 1, "tries" otherwise.
func tries(n int) string {
	if n == 1 {
		return "try"
	}
	return "tries"
}

// RenderSummary renders a relay summary with total runs, pass/fail counts, and
// total runtime.
func RenderSummary(totalRuns, passedCount, failedCount int, totalDuration time.Duration) string {
	w := sepWidth()

	runsStr := fmt.Sprintf("%d run%s", totalRuns, plural(totalRuns))
	passedStr := SuccessStyle.Render(fmt.Sprintf("%d passed", passedCount))
	failedStr := FailureStyle.Render(fmt.Sprintf("%d failed", failedCount))
	durStr := DimStyle.Render(fmt.Sprintf("total %s", formatDuration(totalDuration)))

	var sb strings.Builder
	sb.WriteString(separatorForWidth(w))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Relay complete: %s  │  %s  │  %s  │  %s", runsStr, passedStr, failedStr, durStr))
	sb.WriteString("\n")
	sb.WriteString(separatorForWidth(w))
	return sb.String()
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
