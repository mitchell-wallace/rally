package tuitabs

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
)

func TestModelTabSwitching(t *testing.T) {
	m := newTestModel()
	for _, tc := range []struct {
		key  tea.KeyMsg
		want tab
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}, tabTranscript},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")}, tabAgents},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")}, tabDashboard},
		{tea.KeyMsg{Type: tea.KeyTab}, tabTranscript},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, tabDashboard},
	} {
		m = updateModel(t, m, tc.key)
		if m.active != tc.want {
			t.Fatalf("after %q active = %v, want %v", tc.key.String(), m.active, tc.want)
		}
	}
}

func TestModelEventFanoutUpdatesDashboardAndTranscript(t *testing.T) {
	m := newTestModel()
	event := runtimeevent.OutingHeaderReady{
		OutingIndex:  1,
		TotalOutings: 2,
		AgentName:    "codex",
		Attempt:      1,
		StartTime:    time.Date(2026, 7, 4, 14, 0, 0, 0, time.UTC),
		IsLapsBacked: true,
		LapTitle:     "fan out event",
		LapsStarted:  1,
		LapsTotal:    2,
		Model:        "gpt-5.5",
	}
	m = updateModel(t, m, eventMsg{event: event})
	feed := m.dashboard.Feed()
	if got := len(feed.Items()); got != 1 {
		t.Fatalf("dashboard feed items = %d, want 1", got)
	}
	if !strings.Contains(strings.Join(m.transcript.Lines(), "\n"), "fan out event") {
		t.Fatalf("transcript did not include event header: %v", m.transcript.Lines())
	}
}

func TestModelAgentsRenderAtSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 110, Height: 30}, {Width: 80, Height: 24}} {
		m := newTestModel()
		m = updateModel(t, m, size)
		m = updateModel(t, m, seedAgentsMsg{items: tuicore.DemoAgentStatuses()})

		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
		agentsView := m.View()
		if !strings.Contains(agentsView, "benched") || !strings.Contains(agentsView, "codex") {
			t.Fatalf("%dx%d agents view missing expected content:\n%s", size.Width, size.Height, agentsView)
		}
	}
}

func TestModelDemoScriptPlaybackHeadless(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 110, Height: 30})
	m.dashboard = m.dashboard.Seed(tuicore.DemoFeedSeed())
	m.agents.Seed(tuicore.DemoAgentStatuses())
	statuses := tuicore.DemoStatusFrames()
	enrichments := demoEnrichments()
	currentOuting := -1
	for i, step := range tuicore.DemoScript() {
		if len(statuses) > 0 {
			m = updateModel(t, m, statusFrameMsg{line: statuses[i%len(statuses)]})
		}
		if header, ok := step.Event.(runtimeevent.OutingHeaderReady); ok {
			currentOuting = header.OutingIndex
		}
		m = updateModel(t, m, eventMsg{event: step.Event})
		switch step.Event.(type) {
		case runtimeevent.AttemptFinished, runtimeevent.AttemptCancelled, runtimeevent.HandoffAttemptFinished:
			if enrichment, ok := enrichments[currentOuting]; ok {
				m = updateModel(t, m, enrichMsg{outingIndex: currentOuting, summary: enrichment.summary, classification: enrichment.classification, followups: enrichment.followups})
			}
		}
		_ = m.View()
	}
	if !m.transcript.RelayCompleted() {
		t.Fatal("expected transcript relay completion")
	}
	feed := m.dashboard.Feed()
	if !feed.Meta().Completed {
		t.Fatal("expected dashboard feed relay completion")
	}
}

func TestModelTranscriptFollowMode(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 8})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	for i := 0; i < 12; i++ {
		m = updateModel(t, m, transcriptLineMsg{line: strings.Repeat("line ", 3) + time.Duration(i).String()})
	}
	if !m.following {
		t.Fatal("expected transcript to start in follow mode")
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.following {
		t.Fatal("expected manual scroll to disable follow mode")
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if !m.following {
		t.Fatal("expected end key to restore follow mode")
	}
}

func TestModelSynthesizedReplayPopulatesTranscriptAndFeed(t *testing.T) {
	m := newTestModel()
	events := []runtimeevent.Event{
		runtimeevent.RelayStarted{RelayID: 7, TargetIterations: 1, AgentMix: "codex"},
		runtimeevent.OutingHeaderReady{
			OutingIndex:  0,
			TotalOutings: 1,
			AgentName:    "codex",
			Attempt:      1,
			StartTime:    time.Date(2026, 7, 4, 16, 0, 0, 0, time.UTC),
			IsLapsBacked: true,
			LapTitle:     "historical replay",
			LapsStarted:  1,
			LapsTotal:    1,
			Model:        "gpt-5",
		},
		runtimeevent.AttemptFinished{FooterData: runtimeevent.FooterData{Passed: true, Duration: time.Minute, FilesChanged: 1, CommitHash: "abc1234"}},
		runtimeevent.RelaySummaryReady{TotalOutings: 1, Passed: 1, TotalDuration: time.Minute},
		runtimeevent.RelayCompleted{RelayID: 7, TotalOutings: 1, Passed: 1, TotalDuration: time.Minute},
	}
	for _, event := range events {
		m = updateModel(t, m, eventMsg{event: event})
	}
	feed := m.dashboard.Feed()
	if got := len(feed.Items()); got != 1 {
		t.Fatalf("dashboard feed items = %d, want 1", got)
	}
	if !strings.Contains(strings.Join(m.transcript.Lines(), "\n"), "historical replay") {
		t.Fatalf("transcript missing synthesized replay: %v", m.transcript.Lines())
	}
}

func newTestModel() model {
	m := newModel("rally tui", newControls())
	m.now = func() time.Time { return time.Date(2026, 7, 4, 15, 0, 0, 0, time.UTC) }
	return m
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("model type = %T, want tuitabs.model", next)
	}
	return got
}
