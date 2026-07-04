package tuipanels

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

// Dashboard exposes prototype 2's run feed and detail panels as an embeddable
// component for richer TUI layouts.
type Dashboard model

func NewDashboard(title string) Dashboard {
	return Dashboard(newModel(title, nil))
}

func (d Dashboard) Update(msg tea.Msg) (Dashboard, tea.Cmd) {
	m := model(d)
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = maxInt(1, msg.Width)
		m.height = maxInt(1, msg.Height)
		m.refit()
		m.refreshDetail()
	case seedMsg:
		m.feed.Seed(msg.items)
		m.syncSelection()
		m.refreshDetail()
	case enrichMsg:
		m.feed.Enrich(msg.runIndex, msg.summary, msg.classification, msg.followups)
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
	return Dashboard(m), nil
}

func (d Dashboard) View(width, height int) string {
	m := model(d)
	m.width = maxInt(1, width)
	m.height = maxInt(2, height+1)
	m.refit()
	m.refreshDetail()
	if height <= 1 {
		return fitLine(m.headerLine(), m.width)
	}
	if m.isSmall() {
		header := fitLine(m.headerLine(), m.width)
		var body string
		if m.detailOpen {
			body = m.detailPanel(m.width, maxInt(1, height-1), true)
		} else {
			body = m.feedPanel(m.width, maxInt(1, height-1), true)
		}
		return header + "\n" + body
	}
	return fitLine(m.headerLine(), m.width) + "\n" + m.panelsView()
}

func (d Dashboard) Seed(items []tuicore.FeedItem) Dashboard {
	next, _ := d.Update(seedMsg{items: items})
	return next
}

func (d Dashboard) Enrich(runIndex int, summary, classification string, followups []string) Dashboard {
	next, _ := d.Update(enrichMsg{runIndex: runIndex, summary: summary, classification: classification, followups: followups})
	return next
}

func (d Dashboard) Apply(event runtimeevent.Event) Dashboard {
	next, _ := d.Update(eventMsg{event: event})
	return next
}

func (d Dashboard) SetStatusLine(line string) Dashboard {
	next, _ := d.Update(statusFrameMsg{line: line})
	return next
}

func (d Dashboard) Feed() tuicore.RunFeed {
	return model(d).feed
}
