package monitor

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// RenderStatus formats a status line.
func RenderStatus(elapsed time.Duration, dirtyCount int, lastActivity time.Duration, warnings []string) string {
	return RenderStatusExt(elapsed, dirtyCount, lastActivity, warnings, Indicators{})
}

// RenderStatusExt is like RenderStatus but appends reliability and token
// indicators when set.
func RenderStatusExt(elapsed time.Duration, dirtyCount int, lastActivity time.Duration, warnings []string, ind Indicators) string {
	elapsedStr := formatDuration(elapsed)
	activityStr := "—"
	if lastActivity >= 0 {
		activityStr = formatLastActivity(lastActivity)
	}

	parts := []string{
		fmt.Sprintf("⏱ %s", elapsedStr),
		fmt.Sprintf("📁 %d file%s", dirtyCount, plural(dirtyCount)),
		fmt.Sprintf("last activity: %s", activityStr),
	}
	if ind.Retry != "" {
		parts = append(parts, ind.Retry)
	}

	line := strings.Join(parts, "  │  ")
	if len(warnings) > 0 {
		line += "  │  " + strings.Join(warnings, " ")
	}
	if ind.Reliability != "" {
		line += "  │  " + ind.Reliability
	}
	if ind.Action != "" {
		line += "  │  " + ind.Action
	}
	return line
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// formatLastActivity formats a "time since last activity" duration with
// minute-precision so the status line doesn't churn every tick. Anything
// under one minute reads as "< 1m ago".
func formatLastActivity(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return "< 1m ago"
	}
	m := int(d.Minutes())
	if m < 60 {
		return fmt.Sprintf("%dm ago", m)
	}
	h := m / 60
	m = m % 60
	if m == 0 {
		return fmt.Sprintf("%dh ago", h)
	}
	return fmt.Sprintf("%dh %dm ago", h, m)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (m *Monitor) render(out io.Writer, line string) {
	m.mu.Lock()
	cursorUpLines := m.cursorUpLines
	m.mu.Unlock()

	if cursorUpLines <= 0 {
		fmt.Fprintf(out, "\r%s", line)
		return
	}
	fmt.Fprintf(out, "\x1b[%dA\r\x1b[2K%s\x1b[%dB\r", cursorUpLines, line, cursorUpLines)
}

func (m *Monitor) clear(out io.Writer) {
	m.mu.Lock()
	cursorUpLines := m.cursorUpLines
	m.mu.Unlock()

	if cursorUpLines <= 0 {
		fmt.Fprint(out, "\n")
		return
	}
	// Move up to the first reserved line and clear each reserved line in turn.
	// After clearing the last one, return the cursor to the first reserved
	// line so the next print writes immediately under the header rather than
	// leaving the cleared lines as blank gaps.
	fmt.Fprintf(out, "\x1b[%dA\r", cursorUpLines)
	for i := 0; i < cursorUpLines; i++ {
		fmt.Fprint(out, "\x1b[2K")
		if i < cursorUpLines-1 {
			fmt.Fprint(out, "\x1b[1B")
		}
	}
	if cursorUpLines > 1 {
		fmt.Fprintf(out, "\x1b[%dA", cursorUpLines-1)
	}
	fmt.Fprint(out, "\r")
}
