package tuitabs

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
)

type panelFocus int

const (
	focusFeed panelFocus = iota
	focusDetail
)

type dashboardModel struct {
	title    string
	controls *controls
	now      func() time.Time

	feed           tuicore.OutingFeed
	focus          panelFocus
	selected       int
	following      bool
	detailViewport viewport.Model
	detailOpen     bool

	statusLine string
	armedHint  string
	armedUntil time.Time

	done     bool
	workErr  error
	quitting bool

	width  int
	height int
}

func newDashboardModel(title string, controls *controls) dashboardModel {
	if title == "" {
		title = "rally tui"
	}
	return dashboardModel{
		title:          title,
		controls:       controls,
		now:            time.Now,
		following:      true,
		selected:       -1,
		detailViewport: viewport.New(48, 16),
		width:          100,
		height:         30,
	}
}

func (m dashboardModel) Init() tea.Cmd {
	return nil
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = dashboardMaxInt(1, msg.Width)
		m.height = dashboardMaxInt(1, msg.Height)
		m.refit()
		m.refreshDetail()
		return m, nil
	case seedFeedMsg:
		m.feed.Seed(msg.items)
		m.syncSelection()
		m.refreshDetail()
		return m, nil
	case enrichMsg:
		m.feed.Enrich(msg.outingIndex, msg.summary, msg.classification, msg.followups)
		m.refreshDetail()
		return m, nil
	case eventMsg:
		m.feed.Apply(msg.event)
		m.syncSelection()
		m.refreshDetail()
		return m, nil
	case statusFrameMsg:
		m.statusLine = msg.line
		m.updateLiveDuration()
		m.syncSelection()
		return m, nil
	case transcriptLineMsg:
		m.statusLine = msg.line
		return m, nil
	case doneMsg:
		m.done = true
		m.workErr = msg.err
		m.statusLine = ""
		m.feed.Apply(nil)
		m.syncSelection()
		m.refreshDetail()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m dashboardModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "ctrl+c", "ctrl+s", "ctrl+p", "ctrl+x":
		if feedback, ok := m.controls.press(actionForKey(key)); ok {
			m.armedHint = feedback.message
			m.armedUntil = feedback.deadline
		} else if m.done && key == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	case "enter":
		if m.isSmall() && !m.detailOpen && len(m.feed.Items()) > 0 {
			m.detailOpen = true
			m.focus = focusDetail
			m.refit()
			m.refreshDetail()
			return m, nil
		}
		m.controls.resume()
		return m, nil
	case "esc":
		if m.isSmall() && m.detailOpen {
			m.detailOpen = false
			m.focus = focusFeed
			return m, nil
		}
		if m.done {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	case "q":
		if m.done {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	case "tab", "shift+tab", "left", "right":
		if !m.isSmall() {
			m.toggleFocus()
		}
		return m, nil
	case "up", "k":
		if m.focus == focusDetail || (m.isSmall() && m.detailOpen) {
			m.detailViewport.LineUp(1)
			return m, nil
		}
		m.moveSelection(-1)
		return m, nil
	case "down", "j":
		if m.focus == focusDetail || (m.isSmall() && m.detailOpen) {
			m.detailViewport.LineDown(1)
			return m, nil
		}
		m.moveSelection(1)
		return m, nil
	case "pgup":
		m.detailViewport.PageUp()
		return m, nil
	case "pgdown":
		m.detailViewport.PageDown()
		return m, nil
	case "home":
		if m.focus == focusDetail {
			m.detailViewport.GotoTop()
		} else {
			m.selectIndex(0)
		}
		return m, nil
	case "end", "g":
		if m.focus == focusDetail {
			m.detailViewport.GotoBottom()
		} else {
			m.selectIndex(len(m.feed.Items()) - 1)
		}
		return m, nil
	}
	return m, nil
}

func (m *dashboardModel) refit() {
	_, detailWidth, panelHeight := m.panelGeometry()
	m.detailViewport.Width = dashboardMaxInt(1, detailWidth-4)
	m.detailViewport.Height = dashboardMaxInt(1, panelHeight-2)
	if m.isSmall() && m.detailOpen {
		m.detailViewport.Width = dashboardMaxInt(1, m.width-4)
		m.detailViewport.Height = dashboardMaxInt(1, m.height-4)
	}
}

func (m *dashboardModel) syncSelection() {
	items := m.feed.Items()
	if len(items) == 0 {
		m.selected = -1
		m.following = true
		return
	}
	live := m.feed.LiveIndex()
	if m.following && live >= 0 {
		m.selected = live
		return
	}
	if m.selected < 0 || m.selected >= len(items) {
		m.selected = len(items) - 1
	}
}

func (m *dashboardModel) moveSelection(delta int) {
	m.selectIndex(m.selected + delta)
}

func (m *dashboardModel) selectIndex(index int) {
	items := m.feed.Items()
	if len(items) == 0 {
		m.selected = -1
		m.following = true
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(items) {
		index = len(items) - 1
	}
	m.selected = index
	live := m.feed.LiveIndex()
	m.following = live >= 0 && m.selected == live
	m.refreshDetail()
}

func (m *dashboardModel) toggleFocus() {
	if m.focus == focusFeed {
		m.focus = focusDetail
	} else {
		m.focus = focusFeed
	}
}

func (m *dashboardModel) refreshDetail() {
	width := m.detailViewport.Width
	if width <= 0 {
		width = 48
	}
	m.detailViewport.SetContent(strings.Join(detailLines(m.selectedItem(), width), "\n"))
}

func (m *dashboardModel) updateLiveDuration() {
	live := m.feed.LiveIndex()
	if live < 0 {
		return
	}
	items := m.feed.Items()
	if live >= len(items) || items[live].StartedAt.IsZero() {
		return
	}
	duration := m.now().Sub(items[live].StartedAt).Round(time.Second)
	files := parseStatusFiles(m.statusLine)
	m.feed.SetLiveStats(duration, files)
}

func (m dashboardModel) selectedItem() *tuicore.FeedItem {
	items := m.feed.Items()
	if m.selected < 0 || m.selected >= len(items) {
		return nil
	}
	item := items[m.selected]
	return &item
}

func (m dashboardModel) isSmall() bool {
	return m.width < 60
}

func dashboardMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var statusFilesRe = regexp.MustCompile(`📁\s+(\d+)\s+files?`)

func parseStatusFiles(line string) int {
	m := statusFilesRe.FindStringSubmatch(line)
	if len(m) != 2 {
		return -1
	}
	files, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return files
}
