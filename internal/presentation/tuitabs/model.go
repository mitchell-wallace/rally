package tuitabs

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

type tab int

const (
	tabDashboard tab = iota
	tabTranscript
	tabAgents
)

type model struct {
	title    string
	controls *controls
	now      func() time.Time

	active     tab
	dashboard  dashboard
	transcript tuicore.Transcript
	viewport   viewport.Model
	following  bool
	agents     tuicore.AgentStatusList

	statusLine string
	armedHint  string
	armedUntil time.Time

	done     bool
	doneHint string
	workErr  error
	quitting bool

	width  int
	height int
}

func newModel(title string, controls *controls) model {
	if title == "" {
		title = "rally tui"
	}
	return model{
		title:     title,
		controls:  controls,
		now:       time.Now,
		dashboard: newDashboard(title),
		viewport:  viewport.New(80, 22),
		following: true,
		width:     100,
		height:    30,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = maxInt(1, msg.Width)
		m.height = maxInt(1, msg.Height)
		m.refit()
		return m, nil
	case eventMsg:
		m.dashboard = m.dashboard.Apply(msg.event)
		m.transcript.Apply(msg.event)
		m.refreshTranscript()
		return m, nil
	case enrichMsg:
		m.dashboard = m.dashboard.Enrich(msg.runIndex, msg.summary, msg.classification, msg.followups)
		return m, nil
	case statusFrameMsg:
		m.statusLine = msg.line
		m.dashboard = m.dashboard.SetStatusLine(msg.line)
		return m, nil
	case transcriptLineMsg:
		m.appendPlainLine(msg.line)
		return m, nil
	case seedAgentsMsg:
		m.agents.Seed(msg.items)
		return m, nil
	case doneMsg:
		m.done = true
		m.workErr = msg.err
		m.statusLine = ""
		m.refreshTranscript()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "1":
		m.active = tabDashboard
		return m, nil
	case "2":
		m.active = tabTranscript
		return m, nil
	case "3":
		m.active = tabAgents
		return m, nil
	case "tab":
		m.active = (m.active + 1) % 3
		return m, nil
	case "shift+tab":
		m.active = (m.active + 2) % 3
		return m, nil
	case "enter":
		m.controls.resume()
		return m, nil
	case "q", "esc":
		if m.done {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}
	switch m.active {
	case tabDashboard:
		next, _ := m.dashboard.Update(msg)
		m.dashboard = next
	case tabTranscript:
		m.handleTranscriptKey(key)
	}
	return m, nil
}

func (m *model) refit() {
	bodyHeight := maxInt(1, m.height-2)
	m.viewport.Width = m.width
	m.viewport.Height = bodyHeight
	m.refreshTranscript()
}

func (m *model) refreshTranscript() {
	lines := append(m.transcript.Lines(), m.transcript.LiveTail()...)
	m.viewport.SetContent(strings.Join(lines, "\n"))
	if m.following {
		m.viewport.GotoBottom()
	}
}

func (m *model) appendPlainLine(line string) {
	if line == "" {
		return
	}
	for _, part := range strings.Split(strings.TrimRight(line, "\n"), "\n") {
		m.transcript.Apply(runtimeevent.TryStatusSnapshot{Status: part})
	}
	m.refreshTranscript()
}

func (m *model) handleTranscriptKey(key string) {
	switch key {
	case "up", "k":
		m.following = false
		m.viewport.LineUp(1)
	case "down", "j":
		m.following = false
		m.viewport.LineDown(1)
	case "pgup":
		m.following = false
		m.viewport.PageUp()
	case "pgdown":
		m.following = false
		m.viewport.PageDown()
	case "home":
		m.following = false
		m.viewport.GotoTop()
	case "end", "g":
		m.following = true
		m.viewport.GotoBottom()
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
