package tuisafe

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

func TestModelRendersHeaderAndFooter(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})
	m = updateModel(t, m, eventMsg{event: runtimeevent.RelayStarted{RelayID: 3, TargetIterations: 5, AgentMix: "mix"}})
	header := runtimeevent.RunHeaderReady{
		RunIndex:     0,
		TotalRuns:    5,
		AgentName:    "codex",
		Attempt:      1,
		StartTime:    time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC),
		IsLapsBacked: true,
		LapTitle:     "Build prototype",
		LapsStarted:  1,
		LapsTotal:    5,
		Model:        "gpt-5",
	}
	m = updateModel(t, m, eventMsg{event: header})
	footer := runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{
		Passed:       true,
		Duration:     time.Minute,
		FilesChanged: 2,
		CommitHash:   "abc1234",
		Attempt:      1,
		MaxAttempts:  1,
	}}
	m = updateModel(t, m, eventMsg{event: footer})

	view := m.View()
	for _, want := range []string{"codex", "Build prototype", "abc1234", "rally tui-1", "relay #3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestModelFollowModeDisengageAndReengage(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 4})
	for i := 0; i < 20; i++ {
		m = updateModel(t, m, transcriptLineMsg{line: "line"})
	}
	if !m.following {
		t.Fatal("expected initial content to follow bottom")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.following {
		t.Fatal("expected manual scroll to disengage following")
	}
	m = updateModel(t, m, transcriptLineMsg{line: "new line"})
	if !strings.Contains(m.View(), "new output") {
		t.Fatalf("expected new output indicator, view:\n%s", m.View())
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if !m.following {
		t.Fatal("expected End to reengage following")
	}
}

func TestModelDoneEnablesQ(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, doneMsg{})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected q after done to return tea.Quit command")
	}
	got := next.(model)
	if !got.quitting {
		t.Fatal("expected model to mark operator quit")
	}
}

func TestModelDemoScriptPlaybackHeadless(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	statuses := tuicore.DemoStatusFrames()
	for i, step := range tuicore.DemoScript() {
		if len(statuses) > 0 {
			m = updateModel(t, m, statusFrameMsg{line: statuses[i%len(statuses)]})
		}
		m = updateModel(t, m, eventMsg{event: step.Event})
		_ = m.View()
	}
	if !m.transcript.RelayCompleted() {
		t.Fatal("expected demo playback to complete relay")
	}
}

func newTestModel() model {
	m := newModel("rally tui-1", newControls())
	m.now = func() time.Time { return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC) }
	return m
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("model type = %T, want tuisafe.model", next)
	}
	return got
}
