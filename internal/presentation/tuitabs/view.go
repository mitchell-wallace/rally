package tuitabs

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	tabStyle       = lipgloss.NewStyle().Padding(0, 1)
	activeTabStyle = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
	statusStyle    = lipgloss.NewStyle().Reverse(true)
	borderStyle    = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m model) View() string {
	if m.height <= 1 {
		return m.statusBar()
	}
	bodyHeight := maxInt(1, m.height-2)
	return m.tabBar() + "\n" + m.bodyView(bodyHeight) + "\n" + m.statusBar()
}

func (m model) tabBar() string {
	labels := []string{"[1] Dashboard", "[2] Transcript", "[3] Agents", "[4] Laps", "[5] Config"}
	parts := make([]string, len(labels))
	for i, label := range labels {
		style := tabStyle
		if tab(i) == m.active {
			style = activeTabStyle
		}
		parts[i] = style.Render(label)
	}
	return fitLine(strings.Join(parts, ""), m.width)
}

func (m model) bodyView(height int) string {
	switch m.active {
	case tabDashboard:
		return fitBlock(m.dashboard.View(m.width, height), m.width, height)
	case tabTranscript:
		vp := m.viewport
		vp.Width = m.width
		vp.Height = height
		return fitBlock(vp.View(), m.width, height)
	case tabAgents:
		return fitBlock(m.agentsView(height), m.width, height)
	case tabLaps:
		return fitBlock(m.laps.View(m.width, height), m.width, height)
	case tabConfig:
		return fitBlock(m.config.View(m.width, height), m.width, height)
	default:
		return strings.Repeat("\n", height-1)
	}
}

func (m model) agentsView(height int) string {
	width := maxInt(1, m.width-2)
	contentHeight := maxInt(1, height-2)
	items := m.agents.Items()
	lines := []string{fitLine("agent           model                 state       since       reset-at     reason", width)}
	for _, item := range items {
		resetAt := "-"
		if !item.ResetAt.IsZero() {
			resetAt = item.ResetAt.Format("15:04")
		}
		since := "-"
		if !item.Since.IsZero() {
			since = item.Since.Format("15:04")
		}
		line := fmt.Sprintf("%-15s %-21s %-11s %-10s %-10s %s", item.Agent, item.Model, item.State, since, resetAt, item.Reason)
		lines = append(lines, fitLine(line, width))
	}
	if len(items) == 0 {
		lines = append(lines, mutedStyle.Render(fitLine("no agent status events", width)))
	}
	return borderStyle.Width(width).Height(contentHeight).Render(fitLines(lines, width, contentHeight))
}

func (m model) statusBar() string {
	width := maxInt(1, m.width)
	left := m.title
	middle := m.middleStatus()
	right := m.tabLegend() + " · 1-5/Tab tabs · ^C quit  ^S skip  ^P pause  ^X stop"
	if m.done {
		right = "relay complete - q to exit"
		if m.workErr != nil {
			right = "relay failed - q to exit"
		} else if m.doneHint != "" {
			right = m.doneHint
		}
	}
	return statusStyle.Width(width).Render(composeStatus(width, left, middle, right))
}

func (m model) middleStatus() string {
	now := m.now()
	if m.armedHint != "" && now.Before(m.armedUntil) {
		return m.armedHint
	}
	for _, line := range m.transcript.LiveTail() {
		if strings.Contains(line, "Paused") || strings.Contains(line, "PAUSED") {
			return "PAUSED - Enter to resume"
		}
		if strings.Contains(line, "next run starts") || strings.Contains(line, "waiting") {
			return ansi.Strip(line)
		}
	}
	return m.statusLine
}

func (m model) tabLegend() string {
	switch m.active {
	case tabDashboard:
		return "j/k select"
	case tabTranscript:
		return "j/k scroll"
	case tabAgents:
		return "read-only status"
	case tabLaps:
		return "j/k scroll · r refresh"
	case tabConfig:
		return m.config.Legend()
	default:
		return "q quit"
	}
}

func wrapLine(s string, width int) []string {
	s = strings.TrimSpace(ansi.Strip(s))
	if s == "" {
		return []string{""}
	}
	var lines []string
	for _, paragraph := range strings.Split(s, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := ""
		for _, word := range words {
			if line == "" {
				line = truncateCell(word, width)
				continue
			}
			if cellWidth(line)+1+cellWidth(word) <= width {
				line += " " + word
				continue
			}
			lines = append(lines, line)
			line = truncateCell(word, width)
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func composeStatus(width int, left, middle, right string) string {
	left = ansi.Strip(left)
	middle = ansi.Strip(middle)
	right = ansi.Strip(right)
	if width <= 0 {
		return ""
	}
	right = truncateCell(right, width/2)
	left = truncateCell(left, maxInt(1, width-cellWidth(right)-2))
	remaining := width - cellWidth(left) - cellWidth(right)
	if remaining <= 0 {
		return truncateCell(left+right, width)
	}
	if middle == "" {
		return left + strings.Repeat(" ", remaining) + right
	}
	middle = truncateCell(middle, remaining-2)
	line := left + " " + middle
	padding := width - cellWidth(line) - cellWidth(right)
	if padding < 1 {
		padding = 1
	}
	return truncateCell(line+strings.Repeat(" ", padding)+right, width)
}

func fitBlock(block string, width, height int) string {
	lines := strings.Split(block, "\n")
	return fitLines(lines, width, height)
}

func fitLines(lines []string, width, height int) string {
	out := make([]string, 0, height)
	for i := 0; i < height && i < len(lines); i++ {
		out = append(out, fitLine(lines[i], width))
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", maxInt(1, width)))
	}
	return strings.Join(out, "\n")
}

func fitLine(s string, width int) string {
	s = truncateCell(s, width)
	if pad := width - cellWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func truncateCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if cellWidth(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && cellWidth(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func cellWidth(s string) int {
	return lipgloss.Width(s)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
