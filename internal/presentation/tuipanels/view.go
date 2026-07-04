package tuipanels

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
)

var (
	headerStyle      = lipgloss.NewStyle().Bold(true)
	statusStyle      = lipgloss.NewStyle().Reverse(true)
	borderStyle      = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
	focusedStyle     = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("12"))
	selectedRowStyle = lipgloss.NewStyle().Reverse(true)
	mutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m model) View() string {
	if m.height <= 1 {
		return m.statusBar()
	}
	if m.isSmall() {
		return m.smallView()
	}
	header := fitLine(m.headerLine(), m.width)
	body := m.panelsView()
	return header + "\n" + body + "\n" + m.statusBar()
}

func (m model) smallView() string {
	header := fitLine(m.headerLine(), m.width)
	var body string
	if m.detailOpen {
		body = m.detailPanel(m.width, maxInt(1, m.height-2), true)
	} else {
		body = m.feedPanel(m.width, maxInt(1, m.height-2), true)
	}
	return header + "\n" + body + "\n" + m.statusBar()
}

func (m model) panelsView() string {
	leftWidth, rightWidth, panelHeight := m.panelGeometry()
	left := m.feedPanel(leftWidth, panelHeight, m.focus == focusFeed)
	right := m.detailPanel(rightWidth, panelHeight, m.focus == focusDetail)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m model) panelGeometry() (leftWidth, rightWidth, panelHeight int) {
	panelHeight = maxInt(1, m.height-2)
	if m.width < 60 {
		return m.width, 0, panelHeight
	}
	leftWidth = m.width * 40 / 100
	if leftWidth < 30 {
		leftWidth = 30
	}
	if leftWidth > m.width-30 {
		leftWidth = m.width - 30
	}
	rightWidth = m.width - leftWidth
	return leftWidth, rightWidth, panelHeight
}

func (m model) feedPanel(totalWidth, totalHeight int, focused bool) string {
	contentWidth := maxInt(1, totalWidth-2)
	contentHeight := maxInt(1, totalHeight-2)
	rows := m.feedRows(contentWidth, contentHeight)
	style := borderStyle
	if focused {
		style = focusedStyle
	}
	return style.Width(contentWidth).Height(contentHeight).Render(strings.Join(rows, "\n"))
}

func (m model) detailPanel(totalWidth, totalHeight int, focused bool) string {
	contentWidth := maxInt(1, totalWidth-2)
	contentHeight := maxInt(1, totalHeight-2)
	style := borderStyle
	if focused {
		style = focusedStyle
	}
	vp := m.detailViewport
	vp.Width = contentWidth
	vp.Height = contentHeight
	return style.Width(contentWidth).Height(contentHeight).Render(vp.View())
}

func (m model) feedRows(width, height int) []string {
	items := m.feed.Items()
	if len(items) == 0 {
		return []string{mutedStyle.Render(fitLine("no runs yet", width))}
	}
	start := 0
	if len(items) > height {
		start = len(items) - height
		if m.selected >= 0 && m.selected < start {
			start = m.selected
		}
		if m.selected >= start+height {
			start = m.selected - height + 1
		}
	}
	end := minInt(len(items), start+height)
	rows := make([]string, 0, height)
	for i := start; i < end; i++ {
		row := m.feedRow(items[i], i, width)
		if i == m.selected {
			row = selectedRowStyle.Width(width).Render(row)
		}
		rows = append(rows, row)
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows
}

func (m model) feedRow(item tuicore.FeedItem, index, width int) string {
	prefix := "  "
	if index == m.selected {
		prefix = "▶ "
	}
	icon := outcomeIcon(item.Outcome)
	left := strings.TrimSpace(fmt.Sprintf("%s%s %s", prefix, icon, item.Agent))
	if item.RoleLabel != "" {
		left += "/" + item.RoleLabel
	}
	title := item.Title
	if title == "" {
		title = "run"
	}
	right := strings.TrimSpace(fmt.Sprintf("%s  %d files", shortDuration(item.Duration), item.Files))
	available := width - cellWidth(left) - cellWidth(right) - 3
	if available < 1 {
		return fitLine(left+" "+right, width)
	}
	row := left + " · " + truncateCell(title, available) + " " + right
	return fitLine(row, width)
}

func (m model) headerLine() string {
	meta := m.feed.Meta()
	left := m.title
	if meta.RelayID > 0 {
		left = fmt.Sprintf("relay #%d", meta.RelayID)
	}
	if meta.Mix != "" {
		left += " · mix " + meta.Mix
	}
	if meta.LapsTotal > 0 {
		left += fmt.Sprintf(" · laps %d/%d", meta.LapsStarted, meta.LapsTotal)
	} else if meta.Target > 0 {
		left += fmt.Sprintf(" · target %d", meta.Target)
	}
	counts := fmt.Sprintf(" · %d✓ %d✗", meta.Passed, meta.Failed)
	if meta.Cancelled > 0 {
		counts += fmt.Sprintf(" %d cancelled", meta.Cancelled)
	}
	left += counts
	if meta.TotalDuration > 0 {
		left += " · elapsed " + shortDuration(meta.TotalDuration)
	}
	return headerStyle.Render(left)
}

func (m model) statusBar() string {
	width := maxInt(1, m.width)
	left := m.middleStatus()
	right := "^C quit  ^S skip  ^P pause  ^X stop · Tab panes · j/k select · q quit"
	if m.isSmall() {
		right = "Enter detail · j/k select · q quit"
		if m.detailOpen {
			right = "Esc back · j/k scroll · q quit"
		}
	}
	if m.done {
		right = "relay complete — q to exit"
		if m.workErr != nil {
			right = "relay failed — q to exit"
		}
	}
	return statusStyle.Width(width).Render(composeStatus(width, left, "", right))
}

func (m model) middleStatus() string {
	now := m.now()
	if m.armedHint != "" && now.Before(m.armedUntil) {
		return m.armedHint
	}
	return m.statusLine
}

func detailLines(item *tuicore.FeedItem, width int) []string {
	if item == nil {
		return []string{"no run selected"}
	}
	if width < 1 {
		width = 1
	}
	var lines []string
	title := item.Title
	if title == "" {
		title = "Run"
	}
	lines = append(lines, wrapLine(title, width)...)
	lines = append(lines, "")
	agent := item.Agent
	if item.Model != "" {
		agent += " · model " + item.Model
	}
	if item.RoleLabel != "" {
		agent += " · role " + item.RoleLabel
	}
	lines = append(lines, wrapLine(agent, width)...)
	outcome := outcomeIcon(item.Outcome) + " " + item.Outcome
	if item.Duration > 0 {
		outcome += " · " + shortDuration(item.Duration)
	}
	if item.Files > 0 {
		outcome += fmt.Sprintf(" · %d files", item.Files)
	}
	if item.Attempt > 0 {
		outcome += fmt.Sprintf(" · attempt %d/%d", item.Attempt, maxInt(item.MaxAttempts, item.Attempt))
	}
	lines = append(lines, wrapLine(outcome, width)...)
	if item.CommitHash != "" {
		commit := "commit " + item.CommitHash
		if item.CommitTitle != "" {
			commit += " " + item.CommitTitle
		}
		lines = append(lines, wrapLine(commit, width)...)
	}
	if item.FailReason != "" {
		lines = append(lines, wrapLine("reason: "+item.FailReason, width)...)
	}
	if item.Summary != "" {
		lines = append(lines, "", "summary:")
		lines = append(lines, wrapLine(item.Summary, width)...)
	}
	if item.Classification != "" {
		lines = append(lines, "", "classification:")
		lines = append(lines, wrapLine(item.Classification, width)...)
	}
	if len(item.Followups) > 0 {
		lines = append(lines, "", "followups:")
		for _, followup := range item.Followups {
			for i, line := range wrapLine(followup, maxInt(1, width-2)) {
				prefix := "  "
				if i == 0 {
					prefix = "- "
				}
				lines = append(lines, prefix+line)
			}
		}
	}
	return lines
}

func outcomeIcon(outcome string) string {
	switch outcome {
	case tuicore.OutcomeRunning:
		return "⣷"
	case tuicore.OutcomePassed:
		return "✓"
	case tuicore.OutcomeFailed:
		return "✗"
	case tuicore.OutcomeCancelled:
		return "■"
	case tuicore.OutcomeHandoff:
		return "⚑"
	default:
		return "·"
	}
}

func shortDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
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
