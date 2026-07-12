package tuitabs

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

type lapsFetcher func(context.Context) (tuicore.LapsSnapshot, error)

type lapsModel struct {
	snapshot tuicore.LapsSnapshot
	viewport viewport.Model
	fetch    lapsFetcher
	loading  *bool
	err      error
	width    int
	height   int
}

var _ tea.Model = lapsModel{}

func newLapsModel(fetch lapsFetcher) lapsModel {
	loading := false
	return lapsModel{
		fetch:    fetch,
		loading:  &loading,
		viewport: viewport.New(80, 22),
		width:    100,
		height:   30,
	}
}

// Init makes the laps view directly composable as a Bubble Tea child model.
// The active Rally TUI still initializes it through the legacy parent until
// every tab is ready for the chassis shell.
func (m lapsModel) Init() tea.Cmd {
	return m.FetchCmd()
}

// Update accepts content-sized window messages and the same runtime messages
// the legacy parent currently fans into the laps view. This is the migration
// seam used by chassis, while the old shell remains active.
func (m lapsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
	case lapsSnapshotMsg:
		if msg.err != nil {
			m.SetError(msg.err)
		} else {
			m.SetSnapshot(msg.snapshot)
		}
	case eventMsg:
		switch msg.event.(type) {
		case runtimeevent.OutingHeaderReady, runtimeevent.AttemptFinished, runtimeevent.AttemptCancelled, runtimeevent.HandoffAttemptFinished:
			return m, m.FetchCmd()
		}
	case tea.KeyMsg:
		if msg.String() == "r" {
			return m, m.FetchCmd()
		}
		m.HandleKey(msg.String())
	}
	return m, nil
}

func (m lapsModel) WithSnapshot(snapshot tuicore.LapsSnapshot) lapsModel {
	m.snapshot = tuicore.CloneLapsSnapshot(snapshot)
	m.refresh()
	return m
}

func (m *lapsModel) SetSize(width, height int) {
	m.width = maxInt(1, width)
	m.height = maxInt(1, height)
	m.viewport.Width = m.width
	m.viewport.Height = maxInt(1, height)
	m.refresh()
}

func (m *lapsModel) SetSnapshot(snapshot tuicore.LapsSnapshot) {
	m.snapshot = tuicore.CloneLapsSnapshot(snapshot)
	m.setLoading(false)
	m.err = nil
	m.refresh()
}

func (m *lapsModel) SetError(err error) {
	m.setLoading(false)
	m.err = err
	m.refresh()
}

func (m *lapsModel) FetchCmd() tea.Cmd {
	if m.fetch == nil || m.isLoading() {
		return nil
	}
	m.setLoading(true)
	return func() tea.Msg {
		snapshot, err := m.fetch(context.Background())
		return lapsSnapshotMsg{snapshot: snapshot, err: err}
	}
}

func (m *lapsModel) Render(width, height int) string {
	m.SetSize(width, height)
	return m.View()
}

func (m lapsModel) View() string {
	vp := m.viewport
	vp.Width = m.width
	vp.Height = m.height
	return vp.View()
}

func (m *lapsModel) HandleKey(key string) {
	switch key {
	case "up", "k":
		m.viewport.LineUp(1)
	case "down", "j":
		m.viewport.LineDown(1)
	case "pgup":
		m.viewport.PageUp()
	case "pgdown":
		m.viewport.PageDown()
	case "home":
		m.viewport.GotoTop()
	case "end", "g":
		m.viewport.GotoBottom()
	}
}

func (m *lapsModel) refresh() {
	width := maxInt(1, m.width)
	lines := m.lines(width)
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m lapsModel) lines(width int) []string {
	if m.err != nil {
		return []string{fitLine("laps unavailable: "+m.err.Error(), width)}
	}
	snapshot := m.snapshot
	if snapshot.Missing {
		return []string{fitLine("no laps workspace", width)}
	}
	switch snapshot.State {
	case "empty":
		return []string{fitLine("queue empty", width)}
	case "complete":
		return []string{fitLine("queue complete", width)}
	}
	if len(snapshot.Entries) == 0 && snapshot.Counts.Total == 0 {
		if m.isLoading() {
			return []string{fitLine("loading laps queue", width)}
		}
		return []string{fitLine("queue empty", width)}
	}

	lines := []string{fitLine(lapsSummaryLine(snapshot), width)}
	for _, entry := range snapshot.Entries {
		lines = appendLapsEntry(lines, entry, snapshot.Gate, width, 0)
	}
	return lines
}

func (m *lapsModel) isLoading() bool {
	return m.loading != nil && *m.loading
}

func (m *lapsModel) setLoading(loading bool) {
	if m.loading == nil {
		m.loading = new(bool)
	}
	*m.loading = loading
}

func lapsSummaryLine(snapshot tuicore.LapsSnapshot) string {
	parts := []string{
		"state " + valueOr(snapshot.State, "unknown"),
		fmt.Sprintf("todo %d", snapshot.Counts.Todo),
		fmt.Sprintf("done %d/%d", snapshot.Counts.Done, snapshot.Counts.Total),
	}
	if snapshot.Claim.Valid {
		parts = append(parts, fmt.Sprintf("claim %s age %s", snapshot.Claim.Lap, formatAge(snapshot.Claim.AgeSeconds)))
	}
	return strings.Join(parts, " | ")
}

func appendLapsEntry(lines []string, entry tuicore.LapsEntry, gate *tuicore.LapsGate, width, indent int) []string {
	switch entry.Kind {
	case "stint":
		lines = append(lines, fitLine(stintHeader(entry, gate, indent), width))
		if gate != nil && gate.Stint == entry.Ref && gate.Message != "" {
			lines = append(lines, fitLine(strings.Repeat(" ", indent+2)+"⛔ "+gate.Message, width))
		}
		for _, lap := range entry.Laps {
			lines = appendLapsEntry(lines, lap, gate, width, indent+2)
		}
	default:
		lines = append(lines, fitLine(lapLine(entry, indent), width))
	}
	return lines
}

func stintHeader(entry tuicore.LapsEntry, gate *tuicore.LapsGate, indent int) string {
	name := valueOr(entry.Ref, entry.Title)
	done, total := 0, 0
	if entry.Stint != nil {
		name = valueOr(entry.Stint.Name, name)
		done = entry.Stint.Done
		total = entry.Stint.Total
	}
	marker := ""
	if gate != nil && gate.Stint == name {
		marker = " ⛔ held"
	}
	return fmt.Sprintf("%s%s/ (stint %d/%d)%s", strings.Repeat(" ", indent), name, done, total, marker)
}

func lapLine(entry tuicore.LapsEntry, indent int) string {
	glyph := "·"
	if entry.IsDone {
		glyph = "✓"
	}
	assignee := ""
	if entry.Assignee != "" {
		assignee = " (" + entry.Assignee + ")"
	}
	title := entry.Title
	if title == "" {
		title = entry.ID
	}
	return fmt.Sprintf("%s%s %s %s%s", strings.Repeat(" ", indent), glyph, entry.ID, title, assignee)
}

func formatAge(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%02dm", minutes/60, minutes%60)
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
