package tuitabs

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

// dashboard exposes prototype 2's outing feed and detail panels as an embeddable
// component for richer TUI layouts.
type dashboard dashboardModel

func newDashboard(title string) dashboard {
	return dashboard(newDashboardModel(title, nil))
}

func (d dashboard) Update(msg tea.Msg) (dashboard, tea.Cmd) {
	m := dashboardModel(d)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = dashboardMaxInt(1, msg.Width)
		m.height = dashboardMaxInt(1, msg.Height)
		m.refit()
		m.refreshDetail()
	case seedFeedMsg:
		m.feed.Seed(msg.items)
		m.syncSelection()
		m.refreshDetail()
	case enrichMsg:
		m.feed.Enrich(msg.outingIndex, msg.summary, msg.classification, msg.followups)
		m.refreshDetail()
	case eventMsg:
		m.feed.Apply(msg.event)
		m.syncSelection()
		m.refreshDetail()
	case statusFrameMsg:
		m.statusLine = msg.line
		m.updateLiveDuration()
		m.syncSelection()
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "shift+tab", "left", "right":
			if !m.isSmall() {
				m.toggleFocus()
			}
		case "enter":
			if m.isSmall() && !m.detailOpen && len(m.feed.Items()) > 0 {
				m.detailOpen = true
				m.focus = focusDetail
				m.refit()
				m.refreshDetail()
			}
		case "esc":
			if m.isSmall() && m.detailOpen {
				m.detailOpen = false
				m.focus = focusFeed
			}
		case "up", "k":
			if m.focus == focusDetail || (m.isSmall() && m.detailOpen) {
				m.detailViewport.LineUp(1)
			} else {
				m.moveSelection(-1)
			}
		case "down", "j":
			if m.focus == focusDetail || (m.isSmall() && m.detailOpen) {
				m.detailViewport.LineDown(1)
			} else {
				m.moveSelection(1)
			}
		case "pgup":
			m.detailViewport.PageUp()
		case "pgdown":
			m.detailViewport.PageDown()
		case "home":
			if m.focus == focusDetail {
				m.detailViewport.GotoTop()
			} else {
				m.selectIndex(0)
			}
		case "end", "g":
			if m.focus == focusDetail {
				m.detailViewport.GotoBottom()
			} else {
				m.selectIndex(len(m.feed.Items()) - 1)
			}
		}
	}
	return dashboard(m), nil
}

func (d dashboard) View(width, height int) string {
	m := dashboardModel(d)
	m.width = dashboardMaxInt(1, width)
	m.height = dashboardMaxInt(2, height+1)
	m.refit()
	m.refreshDetail()
	if height <= 1 {
		return dashboardFitLine(m.headerLine(), m.width)
	}
	if m.isSmall() {
		header := dashboardFitLine(m.headerLine(), m.width)
		var body string
		if m.detailOpen {
			body = m.detailPanel(m.width, dashboardMaxInt(1, height-1), true)
		} else {
			body = m.feedPanel(m.width, dashboardMaxInt(1, height-1), true)
		}
		return header + "\n" + body
	}
	return dashboardFitLine(m.headerLine(), m.width) + "\n" + m.panelsView()
}

func (d dashboard) Seed(items []tuicore.FeedItem) dashboard {
	next, _ := d.Update(seedFeedMsg{items: items})
	return next
}

func (d dashboard) Enrich(outingIndex int, summary, classification string, followups []string) dashboard {
	next, _ := d.Update(enrichMsg{outingIndex: outingIndex, summary: summary, classification: classification, followups: followups})
	return next
}

func (d dashboard) Apply(event runtimeevent.Event) dashboard {
	next, _ := d.Update(eventMsg{event: event})
	return next
}

func (d dashboard) SetStatusLine(line string) dashboard {
	next, _ := d.Update(statusFrameMsg{line: line})
	return next
}

func (d dashboard) Feed() tuicore.OutingFeed {
	return dashboardModel(d).feed
}
