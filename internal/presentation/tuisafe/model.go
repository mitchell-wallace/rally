package tuisafe

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

type model struct {
	title    string
	controls *controls
	now      func() time.Time

	transcript tuicore.Transcript
	viewport   viewport.Model

	statusLine string
	armedHint  string
	armedUntil time.Time

	following bool
	done      bool
	workErr   error
	quitting  bool

	width  int
	height int
}

func newModel(title string, controls *controls) model {
	if title == "" {
		title = "rally tui-1"
	}
	return model{
		title:     title,
		controls:  controls,
		now:       time.Now,
		viewport:  viewport.New(80, 23),
		following: true,
		width:     80,
		height:    24,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.width < 1 {
			m.width = 1
		}
		if m.height < 1 {
			m.height = 1
		}
		m.refitViewport()
		m.refreshContent()
		return m, nil
	case eventMsg:
		m.transcript.Apply(msg.event)
		m.refreshContent()
		return m, nil
	case statusFrameMsg:
		m.statusLine = msg.line
		return m, nil
	case transcriptLineMsg:
		m.appendPlainLine(msg.line)
		return m, nil
	case doneMsg:
		m.done = true
		m.workErr = msg.err
		m.statusLine = ""
		m.refreshContent()
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
	case "enter":
		m.controls.resume()
		return m, nil
	case "q", "esc":
		if m.done {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	case "up", "k":
		m.following = false
		m.viewport.LineUp(1)
		return m, nil
	case "down", "j":
		m.following = false
		m.viewport.LineDown(1)
		return m, nil
	case "pgup":
		m.following = false
		m.viewport.PageUp()
		return m, nil
	case "pgdown":
		m.following = false
		m.viewport.PageDown()
		return m, nil
	case "home":
		m.following = false
		m.viewport.GotoTop()
		return m, nil
	case "end", "g":
		m.following = true
		m.viewport.GotoBottom()
		return m, nil
	}
	return m, nil
}

func (m *model) refitViewport() {
	m.viewport.Width = m.width
	m.viewport.Height = m.height - 1
	if m.viewport.Height < 0 {
		m.viewport.Height = 0
	}
}

func (m *model) refreshContent() {
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
	m.refreshContent()
}

func (m model) relayMeta() string {
	relayID, target, mix := m.transcript.RelayMeta()
	if relayID == 0 && target == 0 && mix == "" {
		return ""
	}
	if mix == "" {
		mix = "mix"
	}
	if target <= 0 {
		return fmt.Sprintf("relay #%d · %s", relayID, mix)
	}
	return fmt.Sprintf("relay #%d · %s · target %d", relayID, mix, target)
}
