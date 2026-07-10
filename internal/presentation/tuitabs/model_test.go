package tuitabs

import (
	"context"
	"fmt"
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
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")}, tabLaps},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5")}, tabConfig},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")}, tabDashboard},
		{tea.KeyMsg{Type: tea.KeyTab}, tabTranscript},
		{tea.KeyMsg{Type: tea.KeyTab}, tabAgents},
		{tea.KeyMsg{Type: tea.KeyTab}, tabLaps},
		{tea.KeyMsg{Type: tea.KeyTab}, tabConfig},
		{tea.KeyMsg{Type: tea.KeyTab}, tabDashboard},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, tabConfig},
	} {
		m = updateModel(t, m, tc.key)
		if m.active != tc.want {
			t.Fatalf("after %q active = %v, want %v", tc.key.String(), m.active, tc.want)
		}
	}
}

func TestModelLapsRenderingFromSnapshot(t *testing.T) {
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, lapsSnapshotMsg{snapshot: testLapsSnapshot()})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})

	view := m.View()
	for _, want := range []string{
		"state held",
		"todo 4",
		"claim rall-3 age 1m",
		"✓ rall-1 Done root (review)",
		"· rall-3 Todo root (senior)",
		"alpha/ (stint 1/2) ⛔ held",
		"⛔ finish alpha first",
		"  · rall-a Alpha lap (junior)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("laps view missing %q:\n%s", want, view)
		}
	}
}

func TestModelLapsEmptyAndMissingStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot tuicore.LapsSnapshot
		want     string
	}{
		{name: "missing", snapshot: tuicore.LapsSnapshot{Missing: true}, want: "no laps workspace"},
		{name: "empty", snapshot: tuicore.LapsSnapshot{State: "empty"}, want: "queue empty"},
		{name: "complete", snapshot: tuicore.LapsSnapshot{State: "complete"}, want: "queue complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel()
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 8})
			m = updateModel(t, m, lapsSnapshotMsg{snapshot: tc.snapshot})
			m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
			if view := m.View(); !strings.Contains(view, tc.want) {
				t.Fatalf("view missing %q:\n%s", tc.want, view)
			}
		})
	}
}

func TestModelLapsFortyColumnTruncationAndScroll(t *testing.T) {
	snapshot := tuicore.LapsSnapshot{
		State:  "active",
		Counts: tuicore.LapsCounts{Todo: 20, Total: 20},
	}
	for i := 0; i < 20; i++ {
		snapshot.Entries = append(snapshot.Entries, tuicore.LapsEntry{
			Kind:     "lap",
			ID:       fmt.Sprintf("rall-%02d", i),
			Title:    "A very long title that should be truncated at forty columns cleanly",
			Assignee: "senior",
		})
	}
	m := newTestModel()
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 7})
	m = updateModel(t, m, lapsSnapshotMsg{snapshot: snapshot})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	view := m.View()
	for i, line := range strings.Split(view, "\n") {
		if cellWidth(line) > 40 {
			t.Fatalf("line %d width = %d, want <= 40: %q", i, cellWidth(line), line)
		}
	}
	if !strings.Contains(view, "rall-00") {
		t.Fatalf("initial view missing first lap:\n%s", view)
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if view = m.View(); strings.Contains(view, "rall-00") {
		t.Fatalf("scroll did not move viewport:\n%s", view)
	}
}

func TestModelLapsFetchTriggersAndCoalesces(t *testing.T) {
	calls := 0
	fetch := func(context.Context) (tuicore.LapsSnapshot, error) {
		calls++
		return tuicore.LapsSnapshot{State: "active"}, nil
	}
	m := newTestModel()
	m.laps = newLapsModel(fetch)

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init cmd = nil, want fetch")
	}
	if cmd2 := m.laps.FetchCmd(); cmd2 != nil {
		t.Fatal("second fetch while in flight should coalesce")
	}
	msg := cmd()
	if _, ok := msg.(lapsSnapshotMsg); !ok {
		t.Fatalf("fetch msg type = %T, want lapsSnapshotMsg", msg)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
	m = updateModel(t, m, msg)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	var ok bool
	m, ok = next.(model)
	if !ok {
		t.Fatalf("model type = %T", next)
	}
	if m.active != tabLaps || cmd == nil {
		t.Fatalf("activate laps active/cmd = %v/%v, want tabLaps and cmd", m.active, cmd)
	}
	m = updateModel(t, m, cmd())

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = next.(model)
	if cmd == nil {
		t.Fatal("r on active laps tab did not issue fetch")
	}
	m = updateModel(t, m, cmd())

	next, cmd = m.Update(eventMsg{event: runtimeevent.OutingHeaderReady{LapTitle: "boundary"}})
	m = next.(model)
	if cmd == nil {
		t.Fatal("outing boundary did not issue fetch")
	}
	m = updateModel(t, m, cmd())
	if calls != 4 {
		t.Fatalf("fetch calls = %d, want 4", calls)
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

func testLapsSnapshot() tuicore.LapsSnapshot {
	stintLaps := []tuicore.LapsEntry{{Kind: "lap", ID: "rall-a", Title: "Alpha lap", Assignee: "junior"}}
	stint := &tuicore.LapsStint{Name: "alpha", Done: 1, Total: 2, Laps: stintLaps}
	return tuicore.LapsSnapshot{
		State:  "held",
		Counts: tuicore.LapsCounts{Todo: 4, Done: 3, Total: 7},
		Claim:  tuicore.LapsClaim{Valid: true, Lap: "rall-3", AgeSeconds: 72},
		Gate:   &tuicore.LapsGate{Stint: "alpha", Message: "finish alpha first"},
		Entries: []tuicore.LapsEntry{
			{Kind: "lap", ID: "rall-1", Title: "Done root", Assignee: "review", IsDone: true},
			{Kind: "lap", ID: "rall-3", Title: "Todo root", Assignee: "senior"},
			{Kind: "stint", ID: "stint-alpha", Ref: "alpha", Title: "Alpha", Stint: stint, Laps: stintLaps},
		},
	}
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
