package tuitabs

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

func TestDashboardPanelNavigationSelectionPinAndRefollow(t *testing.T) {
	m := newDashboardTestModel()
	m = updateDashboardModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = updateDashboardModel(t, m, eventMsg{event: dashboardHeaderEvent(0, "finished")})
	m = updateDashboardModel(t, m, eventMsg{event: runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Passed: true, Duration: time.Minute, FilesChanged: 1}}})
	m = updateDashboardModel(t, m, eventMsg{event: dashboardHeaderEvent(1, "live")})

	if m.selected != 1 || !m.following {
		t.Fatalf("selected/following = %d/%v, want live selected and following", m.selected, m.following)
	}
	m = updateDashboardModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusDetail {
		t.Fatalf("focus = %v, want detail", m.focus)
	}
	m = updateDashboardModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusFeed {
		t.Fatalf("focus = %v, want feed", m.focus)
	}

	m = updateDashboardModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.selected != 0 || m.following {
		t.Fatalf("selected/following = %d/%v, want pinned older row", m.selected, m.following)
	}
	m = updateDashboardModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.selected != 1 || !m.following {
		t.Fatalf("selected/following = %d/%v, want live row to refollow", m.selected, m.following)
	}
}

func TestDashboardViewRendersDemoFeedAtSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{
		{Width: 100, Height: 30},
		{Width: 80, Height: 24},
		{Width: 50, Height: 15},
	} {
		m := newDashboardTestModel()
		m = updateDashboardModel(t, m, size)
		m = updateDashboardModel(t, m, seedFeedMsg{items: tuicore.DemoFeedSeed()})
		view := m.View()
		if strings.TrimSpace(view) == "" {
			t.Fatalf("%dx%d rendered blank view", size.Width, size.Height)
		}
		if !strings.Contains(view, "rally tui") && !strings.Contains(view, "relay #") {
			t.Fatalf("%dx%d view missing header context:\n%s", size.Width, size.Height, view)
		}
	}
}

func TestDashboardDemoScriptPlaybackHeadless(t *testing.T) {
	m := newDashboardTestModel()
	m = updateDashboardModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updateDashboardModel(t, m, seedFeedMsg{items: tuicore.DemoFeedSeed()})
	statuses := tuicore.DemoStatusFrames()
	for i, step := range tuicore.DemoScript() {
		if len(statuses) > 0 {
			m = updateDashboardModel(t, m, statusFrameMsg{line: statuses[i%len(statuses)]})
		}
		m = updateDashboardModel(t, m, eventMsg{event: step.Event})
		_ = m.View()
	}
	meta := m.feed.Meta()
	if !meta.Completed {
		t.Fatal("expected relay to complete")
	}
	if len(m.feed.Items()) != len(tuicore.DemoFeedSeed())+4 {
		t.Fatalf("items = %d, want seeded plus 4 live outings", len(m.feed.Items()))
	}
}

func newDashboardTestModel() dashboardModel {
	m := newDashboardModel("rally tui", newControls())
	m.now = func() time.Time { return time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC) }
	return m
}

func updateDashboardModel(t *testing.T, m dashboardModel, msg tea.Msg) dashboardModel {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(dashboardModel)
	if !ok {
		t.Fatalf("dashboardModel type = %T, want tuitabs.dashboardModel", next)
	}
	return got
}

func dashboardHeaderEvent(outingIndex int, title string) runtimeevent.OutingHeaderReady {
	return runtimeevent.OutingHeaderReady{
		OutingIndex:  outingIndex,
		TotalOutings: 2,
		AgentName:    "codex",
		Attempt:      1,
		StartTime:    time.Date(2026, 7, 4, 14, outingIndex, 0, 0, time.UTC),
		IsLapsBacked: true,
		LapTitle:     title,
		LapsStarted:  outingIndex + 1,
		LapsTotal:    2,
		Model:        "gpt-5.5",
	}
}
