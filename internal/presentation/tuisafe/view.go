package tuisafe

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var statusStyle = lipgloss.NewStyle().Reverse(true)

func (m model) View() string {
	if m.height <= 1 {
		return m.statusBar()
	}
	return m.viewport.View() + "\n" + m.statusBar()
}

func (m model) statusBar() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	left := m.title
	if meta := m.relayMeta(); meta != "" {
		left += " · " + meta
	}

	middle := m.middleStatus()
	right := "^C quit  ^S skip  ^P pause  ^X stop · ↑/↓ PgUp/PgDn scroll"
	if m.done {
		right = m.doneHint
		if right == "" {
			right = "relay complete — q to exit"
		}
		if m.workErr != nil {
			right = "relay failed — q to exit"
		}
	} else if !m.following && !m.viewport.AtBottom() {
		if middle != "" {
			middle += " · ▼ new output"
		} else {
			middle = "▼ new output"
		}
	}

	line := composeStatus(width, left, middle, right)
	return statusStyle.Width(width).Render(line)
}

func (m model) middleStatus() string {
	now := m.now()
	if m.armedHint != "" && now.Before(m.armedUntil) {
		return m.armedHint
	}
	for _, line := range m.transcript.LiveTail() {
		if strings.Contains(line, "Paused") || strings.Contains(line, "PAUSED") {
			return "PAUSED — Enter to resume"
		}
		if strings.Contains(line, "next run starts") || strings.Contains(line, "waiting") {
			return ansi.Strip(line)
		}
	}
	return m.statusLine
}

func composeStatus(width int, left, middle, right string) string {
	left = ansi.Strip(left)
	middle = ansi.Strip(middle)
	right = ansi.Strip(right)
	if width <= 0 {
		return ""
	}
	if width <= 1 {
		return truncateCell(left, width)
	}

	right = truncateCell(right, width/2)
	leftMax := width - cellWidth(right) - 2
	if leftMax < 1 {
		leftMax = width - cellWidth(right) - 1
	}
	left = truncateCell(left, leftMax)

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
	line += strings.Repeat(" ", padding) + right
	return truncateCell(line, width)
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
