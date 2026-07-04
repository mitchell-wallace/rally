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
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")}, tabMessages},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")}, tabAgents},
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
	event := runtimeevent.RunHeaderReady{
		RunIndex:     1,
		TotalRuns:    2,
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

func TestModelMessagesAndAgentsRenderAtSizes(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 110, Height: 30}, {Width: 80, Height: 24}} {
		m := newTestModel()
		m = updateModel(t, m, size)
		m = updateModel(t, m, seedMessagesMsg{items: tuicore.DemoMessages()})
		m = updateModel(t, m, seedAgentsMsg{items: tuicore.DemoAgentStatuses()})

		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
		messagesView := m.View()
		if !strings.Contains(messagesView, "read-only prototype") || !strings.Contains(messagesView, "pending") {
			t.Fatalf("%dx%d messages view missing expected content:\n%s", size.Width, size.Height, messagesView)
		}

		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
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
	m.messages.Seed(tuicore.DemoMessages())
	m.agents.Seed(tuicore.DemoAgentStatuses())
	statuses := tuicore.DemoStatusFrames()
	enrichments := demoEnrichments()
	currentRun := -1
	for i, step := range tuicore.DemoScript() {
		if len(statuses) > 0 {
			m = updateModel(t, m, statusFrameMsg{line: statuses[i%len(statuses)]})
		}
		if header, ok := step.Event.(runtimeevent.RunHeaderReady); ok {
			currentRun = header.RunIndex
		}
		m = updateModel(t, m, eventMsg{event: step.Event})
		switch step.Event.(type) {
		case runtimeevent.AttemptFinished, runtimeevent.AttemptCancelled, runtimeevent.HandoffAttemptFinished:
			if enrichment, ok := enrichments[currentRun]; ok {
				m = updateModel(t, m, enrichMsg{runIndex: currentRun, summary: enrichment.summary, classification: enrichment.classification, followups: enrichment.followups})
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

func newTestModel() model {
	m := newModel("rally tui-3", newControls())
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
