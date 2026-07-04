package tuitabs

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
)

var (
	tabStyle         = lipgloss.NewStyle().Padding(0, 1)
	activeTabStyle   = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
	statusStyle      = lipgloss.NewStyle().Reverse(true)
	borderStyle      = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
	selectedRowStyle = lipgloss.NewStyle().Reverse(true)
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	badgePending     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	badgeAddressed   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	badgeCancelled   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m model) View() string {
	if m.height <= 1 {
		return m.statusBar()
	}
	bodyHeight := maxInt(1, m.height-2)
	return m.tabBar() + "\n" + m.bodyView(bodyHeight) + "\n" + m.statusBar()
}

func (m model) tabBar() string {
	labels := []string{"[1] Dashboard", "[2] Transcript", "[3] Messages", "[4] Agents"}
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
	case tabMessages:
		return fitBlock(m.messagesView(height), m.width, height)
	case tabAgents:
		return fitBlock(m.agentsView(height), m.width, height)
	default:
		return strings.Repeat("\n", height-1)
	}
}

func (m model) messagesView(height int) string {
	if m.width < 70 {
		return m.messageListPanel(m.width, height)
	}
	leftWidth := m.width * 42 / 100
	if leftWidth < 32 {
		leftWidth = 32
	}
	rightWidth := m.width - leftWidth
	left := m.messageListPanel(leftWidth, height)
	right := m.messageDetailPanel(rightWidth, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m model) messageListPanel(totalWidth, totalHeight int) string {
	width := maxInt(1, totalWidth-2)
	height := maxInt(1, totalHeight-2)
	items := m.messages.Items()
	rows := make([]string, 0, height)
	if len(items) == 0 {
		rows = append(rows, mutedStyle.Render(fitLine("no messages", width)))
	} else {
		start := 0
		if len(items) > height {
			start = m.selected - height + 1
			if start < 0 {
				start = 0
			}
		}
		end := minInt(len(items), start+height)
		for i := start; i < end; i++ {
			row := m.messageRow(items[i], i, width)
			if i == m.selected {
				row = selectedRowStyle.Width(width).Render(row)
			}
			rows = append(rows, row)
		}
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return borderStyle.Width(width).Height(height).Render(strings.Join(rows, "\n"))
}

func (m model) messageRow(item tuicore.MessageItem, index, width int) string {
	prefix := "  "
	if index == m.selected {
		prefix = "> "
	}
	scope := item.Scope
	if scope == "" {
		scope = "run"
	}
	left := fmt.Sprintf("%s#%d %s %s", prefix, item.ID, badge(item.Status), scope)
	right := fmt.Sprintf("pos %d", item.Position)
	available := width - cellWidth(left) - cellWidth(right) - 2
	if available < 4 {
		return fitLine(left, width)
	}
	return fitLine(left+" "+truncateCell(firstLine(item.Body), available)+" "+right, width)
}

func (m model) messageDetailPanel(totalWidth, totalHeight int) string {
	width := maxInt(1, totalWidth-2)
	height := maxInt(1, totalHeight-2)
	var lines []string
	if item, ok := m.selectedMessage(); ok {
		lines = append(lines, fmt.Sprintf("message #%d  %s", item.ID, badge(item.Status)))
		scope := item.Scope
		if scope == "" {
			scope = "run"
		}
		lines = append(lines, "scope: "+scope, fmt.Sprintf("position: %d", item.Position))
		if !item.CreatedAt.IsZero() {
			lines = append(lines, "created: "+item.CreatedAt.Format(time.RFC822))
		}
		lines = append(lines, "")
		lines = append(lines, wrapLine(item.Body, width)...)
	} else {
		lines = append(lines, "no message selected")
	}
	return borderStyle.Width(width).Height(height).Render(fitLines(lines, width, height))
}

func (m model) selectedMessage() (tuicore.MessageItem, bool) {
	items := m.messages.Items()
	if m.selected < 0 || m.selected >= len(items) {
		return tuicore.MessageItem{}, false
	}
	return items[m.selected], true
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
	right := m.tabLegend() + " · 1-4/Tab tabs · ^C quit  ^S skip  ^P pause  ^X stop"
	if m.done {
		right = "relay complete - q to exit"
		if m.workErr != nil {
			right = "relay failed - q to exit"
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
	case tabMessages:
		return "j/k select · read-only prototype"
	case tabAgents:
		return "read-only status"
	default:
		return "q quit"
	}
}

func badge(status string) string {
	switch status {
	case "pending":
		return badgePending.Render("pending")
	case "addressed":
		return badgeAddressed.Render("addressed")
	case "cancelled":
		return badgeCancelled.Render("cancelled")
	default:
		if status == "" {
			status = "unknown"
		}
		return status
	}
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return value
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
