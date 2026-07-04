package tuipanels

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

func TestModelPanelNavigationSelectionPinAndRefollow(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = updateModel(t, m, eventMsg{event: headerEvent(0, "finished")})
	m = updateModel(t, m, eventMsg{event: runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Passed: true, Duration: time.Minute, FilesChanged: 1}}})
	m = updateModel(t, m, eventMsg{event: headerEvent(1, "live")})

	if m.selected != 1 || !m.following {
		t.Fatalf("selected/following = %d/%v, want live selected and following", m.selected, m.following)
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusDetail {
		t.Fatalf("focus = %v, want detail", m.focus)
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusFeed {
		t.Fatalf("focus = %v, want feed", m.focus)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.selected != 0 || m.following {
		t.Fatalf("selected/following = %d/%v, want pinned older row", m.selected, m.following)
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selected != 1 || !m.following {
		t.Fatalf("selected/following = %d/%v, want live row to refollow", m.selected, m.following)
	}
}

func TestModelViewRendersDemoFeedAtSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{
		{Width: 100, Height: 30},
		{Width: 80, Height: 24},
		{Width: 50, Height: 15},
	} {
		m := newTestModel()
		m = updateModel(t, m, size)
		m = updateModel(t, m, seedMsg{items: tuicore.DemoFeedSeed()})
		view := m.View()
		if strings.TrimSpace(view) == "" {
			t.Fatalf("%dx%d rendered blank view", size.Width, size.Height)
		}
		if !strings.Contains(view, "rally tui-2") && !strings.Contains(view, "relay #") {
			t.Fatalf("%dx%d view missing header context:\n%s", size.Width, size.Height, view)
		}
	}
}

func TestModelDemoScriptPlaybackHeadless(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updateModel(t, m, seedMsg{items: tuicore.DemoFeedSeed()})
	statuses := tuicore.DemoStatusFrames()
	for i, step := range tuicore.DemoScript() {
		if len(statuses) > 0 {
			m = updateModel(t, m, statusFrameMsg{line: statuses[i%len(statuses)]})
		}
		m = updateModel(t, m, eventMsg{event: step.Event})
		_ = m.View()
	}
	meta := m.feed.Meta()
	if !meta.Completed {
		t.Fatal("expected relay to complete")
	}
	if len(m.feed.Items()) != len(tuicore.DemoFeedSeed())+4 {
		t.Fatalf("items = %d, want seeded plus 4 live runs", len(m.feed.Items()))
	}
}

func newTestModel() model {
	m := newModel("rally tui-2", newControls())
	m.now = func() time.Time { return time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC) }
	return m
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("model type = %T, want tuipanels.model", next)
	}
	return got
}

func headerEvent(runIndex int, title string) runtimeevent.RunHeaderReady {
	return runtimeevent.RunHeaderReady{
		RunIndex:     runIndex,
		TotalRuns:    2,
		AgentName:    "codex",
		Attempt:      1,
		StartTime:    time.Date(2026, 7, 4, 14, runIndex, 0, 0, time.UTC),
		IsLapsBacked: true,
		LapTitle:     title,
		LapsStarted:  runIndex + 1,
		LapsTotal:    2,
		Model:        "gpt-5.5",
	}
}
