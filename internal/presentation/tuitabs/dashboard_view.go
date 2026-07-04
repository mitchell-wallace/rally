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
	dashboardHeaderStyle      = lipgloss.NewStyle().Bold(true)
	dashboardStatusStyle      = lipgloss.NewStyle().Reverse(true)
	dashboardBorderStyle      = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("8"))
	dashboardFocusedStyle     = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("12"))
	dashboardSelectedRowStyle = lipgloss.NewStyle().Reverse(true)
	dashboardMutedStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m dashboardModel) View() string {
	if m.height <= 1 {
		return m.statusBar()
	}
	if m.isSmall() {
		return m.smallView()
	}
	header := dashboardFitLine(m.headerLine(), m.width)
	body := m.panelsView()
	return header + "\n" + body + "\n" + m.statusBar()
}

func (m dashboardModel) smallView() string {
	header := dashboardFitLine(m.headerLine(), m.width)
	var body string
	if m.detailOpen {
		body = m.detailPanel(m.width, dashboardMaxInt(1, m.height-2), true)
	} else {
		body = m.feedPanel(m.width, dashboardMaxInt(1, m.height-2), true)
	}
	return header + "\n" + body + "\n" + m.statusBar()
}

func (m dashboardModel) panelsView() string {
	leftWidth, rightWidth, panelHeight := m.panelGeometry()
	left := m.feedPanel(leftWidth, panelHeight, m.focus == focusFeed)
	right := m.detailPanel(rightWidth, panelHeight, m.focus == focusDetail)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m dashboardModel) panelGeometry() (leftWidth, rightWidth, panelHeight int) {
	panelHeight = dashboardMaxInt(1, m.height-2)
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

func (m dashboardModel) feedPanel(totalWidth, totalHeight int, focused bool) string {
	contentWidth := dashboardMaxInt(1, totalWidth-2)
	contentHeight := dashboardMaxInt(1, totalHeight-2)
	rows := m.feedRows(contentWidth, contentHeight)
	style := dashboardBorderStyle
	if focused {
		style = dashboardFocusedStyle
	}
	return style.Width(contentWidth).Height(contentHeight).Render(strings.Join(rows, "\n"))
}

func (m dashboardModel) detailPanel(totalWidth, totalHeight int, focused bool) string {
	contentWidth := dashboardMaxInt(1, totalWidth-2)
	contentHeight := dashboardMaxInt(1, totalHeight-2)
	style := dashboardBorderStyle
	if focused {
		style = dashboardFocusedStyle
	}
	vp := m.detailViewport
	vp.Width = contentWidth
	vp.Height = contentHeight
	return style.Width(contentWidth).Height(contentHeight).Render(vp.View())
}

func (m dashboardModel) feedRows(width, height int) []string {
	items := m.feed.Items()
	if len(items) == 0 {
		return []string{dashboardMutedStyle.Render(dashboardFitLine("no outings yet", width))}
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
	end := dashboardMinInt(len(items), start+height)
	rows := make([]string, 0, height)
	for i := start; i < end; i++ {
		row := m.feedRow(items[i], i, width)
		if i == m.selected {
			row = dashboardSelectedRowStyle.Width(width).Render(row)
		}
		rows = append(rows, row)
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows
}

func (m dashboardModel) feedRow(item tuicore.FeedItem, index, width int) string {
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
		title = "outing"
	}
	right := strings.TrimSpace(fmt.Sprintf("%s  %s", shortDuration(item.Duration), fileCountLabel(item.Files)))
	available := width - dashboardCellWidth(left) - dashboardCellWidth(right) - 3
	if available < 1 {
		return dashboardFitLine(left+" "+right, width)
	}
	row := left + " · " + dashboardTruncateCell(title, available) + " " + right
	return dashboardFitLine(row, width)
}

func (m dashboardModel) headerLine() string {
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
	return dashboardHeaderStyle.Render(left)
}

func (m dashboardModel) statusBar() string {
	width := dashboardMaxInt(1, m.width)
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
	return dashboardStatusStyle.Width(width).Render(dashboardComposeStatus(width, left, "", right))
}

func (m dashboardModel) middleStatus() string {
	now := m.now()
	if m.armedHint != "" && now.Before(m.armedUntil) {
		return m.armedHint
	}
	return m.statusLine
}

func detailLines(item *tuicore.FeedItem, width int) []string {
	if item == nil {
		return []string{"no outing selected"}
	}
	if width < 1 {
		width = 1
	}
	var lines []string
	title := item.Title
	if title == "" {
		title = "Outing"
	}
	lines = append(lines, dashboardWrapLine(title, width)...)
	lines = append(lines, "")
	agent := item.Agent
	if item.Model != "" {
		agent += " · model " + item.Model
	}
	if item.RoleLabel != "" {
		agent += " · role " + item.RoleLabel
	}
	lines = append(lines, dashboardWrapLine(agent, width)...)
	outcome := outcomeIcon(item.Outcome) + " " + item.Outcome
	if item.Duration > 0 {
		outcome += " · " + shortDuration(item.Duration)
	}
	if item.Files > 0 {
		outcome += " · " + fileCountLabel(item.Files)
	}
	if item.Attempt > 0 {
		outcome += fmt.Sprintf(" · attempt %d/%d", item.Attempt, dashboardMaxInt(item.MaxAttempts, item.Attempt))
	}
	lines = append(lines, dashboardWrapLine(outcome, width)...)
	if item.CommitHash != "" {
		commit := "commit " + item.CommitHash
		if item.CommitTitle != "" {
			commit += " " + item.CommitTitle
		}
		lines = append(lines, dashboardWrapLine(commit, width)...)
	}
	if item.FailReason != "" {
		lines = append(lines, dashboardWrapLine("reason: "+item.FailReason, width)...)
	}
	if item.Summary != "" {
		lines = append(lines, "", "summary:")
		lines = append(lines, dashboardWrapLine(item.Summary, width)...)
	}
	if item.Classification != "" {
		lines = append(lines, "", "classification:")
		lines = append(lines, dashboardWrapLine(item.Classification, width)...)
	}
	if len(item.Followups) > 0 {
		lines = append(lines, "", "followups:")
		for _, followup := range item.Followups {
			for i, line := range dashboardWrapLine(followup, dashboardMaxInt(1, width-2)) {
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

func fileCountLabel(count int) string {
	if count == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", count)
}

func dashboardWrapLine(s string, width int) []string {
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
				line = dashboardTruncateCell(word, width)
				continue
			}
			if dashboardCellWidth(line)+1+dashboardCellWidth(word) <= width {
				line += " " + word
				continue
			}
			lines = append(lines, line)
			line = dashboardTruncateCell(word, width)
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func dashboardComposeStatus(width int, left, middle, right string) string {
	left = ansi.Strip(left)
	middle = ansi.Strip(middle)
	right = ansi.Strip(right)
	if width <= 0 {
		return ""
	}
	if width <= 1 {
		return dashboardTruncateCell(left, width)
	}

	right = dashboardTruncateCell(right, width/2)
	leftMax := width - dashboardCellWidth(right) - 2
	if leftMax < 1 {
		leftMax = width - dashboardCellWidth(right) - 1
	}
	left = dashboardTruncateCell(left, leftMax)

	remaining := width - dashboardCellWidth(left) - dashboardCellWidth(right)
	if remaining <= 0 {
		return dashboardTruncateCell(left+right, width)
	}
	if middle == "" {
		return left + strings.Repeat(" ", remaining) + right
	}
	middle = dashboardTruncateCell(middle, remaining-2)
	line := left + " " + middle
	padding := width - dashboardCellWidth(line) - dashboardCellWidth(right)
	if padding < 1 {
		padding = 1
	}
	line += strings.Repeat(" ", padding) + right
	return dashboardTruncateCell(line, width)
}

func dashboardFitLine(s string, width int) string {
	s = dashboardTruncateCell(s, width)
	if pad := width - dashboardCellWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func dashboardTruncateCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if dashboardCellWidth(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && dashboardCellWidth(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func dashboardCellWidth(s string) int {
	return lipgloss.Width(s)
}

func dashboardMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
