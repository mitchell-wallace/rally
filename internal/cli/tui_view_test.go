package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/relay/runner/runtimeevent"
	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestSynthesizeRelayEvents(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("mkdir rally dir: %v", err)
	}
	lapsDir := filepath.Join(workspaceDir, ".laps")
	if err := os.MkdirAll(lapsDir, 0o755); err != nil {
		t.Fatalf("mkdir laps dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lapsDir, "laps.json"), []byte(`{"tasks":[{"id":"lap-pass","title":"Passing lap"},{"id":"lap-fail","title":"Failing lap"}]}`), 0o644); err != nil {
		t.Fatalf("write laps fixture: %v", err)
	}

	s, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	started := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	relay := store.RelayRecord{
		ID:                  1,
		TargetIterations:    2,
		CompletedIterations: 2,
		AgentMix:            "codex",
		StartedAt:           started.Format(time.RFC3339),
		EndedAt:             started.Add(9 * time.Second).Format(time.RFC3339),
	}
	if err := s.AppendRelay(relay); err != nil {
		t.Fatalf("AppendRelay: %v", err)
	}
	tries := []store.TryRecord{
		{
			ID:            1,
			OutingID:      1,
			RelayID:       1,
			AgentType:     "codex",
			Completed:     true,
			Outcome:       reliability.OutcomeCompleted,
			Summary:       "pass summary",
			FilesChanged:  []string{"a.go", "b.go"},
			CommitHash:    "abcdef123456",
			StartedAt:     started.Format(time.RFC3339),
			EndedAt:       started.Add(3 * time.Second).Format(time.RFC3339),
			AttemptNumber: 1,
			RuntimeMs:     3000,
			LapID:         "lap-pass",
			LapAssignee:   "senior",
		},
		{
			ID:            2,
			OutingID:      2,
			RelayID:       1,
			AgentType:     "claude",
			Completed:     false,
			Outcome:       reliability.OutcomeFailed,
			Summary:       "first failed",
			FilesChanged:  []string{"c.go"},
			FailReason:    "usage limit",
			StartedAt:     started.Add(3 * time.Second).Format(time.RFC3339),
			EndedAt:       started.Add(5 * time.Second).Format(time.RFC3339),
			AttemptNumber: 1,
			RuntimeMs:     2000,
			LapID:         "lap-fail",
			LapAssignee:   "verify",
		},
		{
			ID:            3,
			OutingID:      2,
			RelayID:       1,
			AgentType:     "claude",
			Completed:     false,
			Outcome:       reliability.OutcomeFailed,
			Summary:       "second failed",
			FilesChanged:  []string{"c.go", "d.go", "e.go"},
			CommitHash:    "123456789abc",
			FailReason:    "usage limit",
			StartedAt:     started.Add(5 * time.Second).Format(time.RFC3339),
			EndedAt:       started.Add(9 * time.Second).Format(time.RFC3339),
			AttemptNumber: 2,
			RuntimeMs:     4000,
			LapID:         "lap-fail",
			LapAssignee:   "verify",
		},
	}
	for _, tr := range tries {
		if err := s.AppendTry(tr); err != nil {
			t.Fatalf("AppendTry(%d): %v", tr.ID, err)
		}
	}

	events, relayID, err := synthesizeRelayEvents(workspaceDir, "latest")
	if err != nil {
		t.Fatalf("synthesizeRelayEvents: %v", err)
	}
	if relayID != 1 {
		t.Fatalf("relayID = %d, want 1", relayID)
	}

	wantKinds := []runtimeevent.Kind{
		runtimeevent.KindRelayStarted,
		runtimeevent.KindOutingHeaderReady,
		runtimeevent.KindAttemptFinished,
		runtimeevent.KindOutingHeaderReady,
		runtimeevent.KindRetryFooterUpdated,
		runtimeevent.KindAttemptFinished,
		runtimeevent.KindRelaySummaryReady,
		runtimeevent.KindRelayCompleted,
	}
	if len(events) != len(wantKinds) {
		t.Fatalf("got %d events, want %d: %#v", len(events), len(wantKinds), events)
	}
	for i, want := range wantKinds {
		if got := events[i].Kind(); got != want {
			t.Fatalf("event %d kind = %s, want %s", i, got, want)
		}
	}

	header := events[1].(runtimeevent.OutingHeaderReady)
	if header.LapTitle != "Passing lap" || header.RoleLabel != "senior" || header.LapsStarted != 1 {
		t.Fatalf("first header = %#v", header)
	}
	passFooter := events[2].(runtimeevent.AttemptFinished).FooterData
	if !passFooter.Passed || passFooter.Duration != 3*time.Second || passFooter.FilesChanged != 2 || passFooter.CommitHash != "abcdef1" {
		t.Fatalf("pass footer = %#v", passFooter)
	}
	retryFooter := events[4].(runtimeevent.RetryFooterUpdated).FooterData
	if !retryFooter.Interim || retryFooter.Duration != 2*time.Second || retryFooter.Attempt != 1 || retryFooter.MaxAttempts != 2 {
		t.Fatalf("retry footer = %#v", retryFooter)
	}
	failFooter := events[5].(runtimeevent.AttemptFinished).FooterData
	if failFooter.Passed || failFooter.Duration != 6*time.Second || failFooter.FilesChanged != 3 || failFooter.CommitHash != "1234567" || failFooter.FailReason != "usage limit" {
		t.Fatalf("failed footer = %#v", failFooter)
	}
	summary := events[6].(runtimeevent.RelaySummaryReady)
	if summary.TotalOutings != 2 || summary.Passed != 1 || summary.Failed != 1 || summary.Cancelled != 0 || summary.TotalDuration != 9*time.Second {
		t.Fatalf("summary = %#v", summary)
	}
}
