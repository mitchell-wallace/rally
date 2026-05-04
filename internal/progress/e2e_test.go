package progress

import (
	"testing"
)

func TestE2E_LapsEnabledComplete(t *testing.T) {
	installFakeLaps(t)
	tmp := setupTempWorkspace(t)
	enableLapsInWorkspace(t, tmp)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{
		RunID:        "run-1",
		RecordedLaps: []string{"lap-a"},
	}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--complete",
		"--summary", "Did X",
		"--followup", "Next",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	entry := pl.RecentRuns[0]
	if entry.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", entry.RunID)
	}
	if entry.Summary != "Did X" {
		t.Errorf("Summary = %q, want Did X", entry.Summary)
	}
	laps, ok := entry.LapsCompleted.([]interface{})
	if !ok || len(laps) != 1 || laps[0] != "lap-a" {
		t.Errorf("LapsCompleted = %v, want [lap-a]", entry.LapsCompleted)
	}
	if entry.Handoff != nil {
		t.Errorf("expected no Handoff for complete, got %+v", entry.Handoff)
	}
}

func TestE2E_LapsEnabledHandoff(t *testing.T) {
	installFakeLaps(t)
	tmp := setupTempWorkspace(t)
	enableLapsInWorkspace(t, tmp)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{
		RunID:        "run-2",
		HandoffState: 1,
	}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--handoff",
		"--summary", "Blocked",
		"--followup", "Fix auth",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	entry := pl.RecentRuns[0]
	if entry.RunID != "run-2" {
		t.Errorf("RunID = %q, want run-2", entry.RunID)
	}
	if entry.Handoff == nil {
		t.Fatal("expected Handoff to be present")
	}
	if entry.Handoff.Summary != "Blocked" {
		t.Errorf("Handoff.Summary = %q, want Blocked", entry.Handoff.Summary)
	}
	if len(entry.Handoff.Followups) != 1 || entry.Handoff.Followups[0] != "Fix auth" {
		t.Errorf("Handoff.Followups = %v, want [Fix auth]", entry.Handoff.Followups)
	}
	if len(entry.Handoff.CreatedLapIDs) != 1 || entry.Handoff.CreatedLapIDs[0] != "lap-fake-id" {
		t.Errorf("CreatedLapIDs = %v, want [lap-fake-id]", entry.Handoff.CreatedLapIDs)
	}
}

func TestE2E_LapsEnabledStub(t *testing.T) {
	installFakeLaps(t)
	tmp := setupTempWorkspace(t)
	enableLapsInWorkspace(t, tmp)
	overrideWorkspaceDir(t, tmp)

	if err := SaveRunState(tmp, &RunState{
		RunID:        "run-3",
		RecordedLaps: []string{"lap-b"},
	}); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	// Simulate runner writing stub without finalizing
	entry := RunEntry{
		RunID:         "run-3",
		Summary:       "agent stopped",
		LapsCompleted: []string{"lap-b"},
	}
	if err := AppendRunEntry(tmp, entry); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	if pl.RecentRuns[0].Summary != "agent stopped" {
		t.Errorf("Summary = %q, want agent stopped", pl.RecentRuns[0].Summary)
	}
}

func TestE2E_NoBackendComplete(t *testing.T) {
	tmp := setupTempWorkspace(t)
	overrideWorkspaceDir(t, tmp)

	cmd := NewProgressCmd()
	cmd.SetArgs([]string{
		"--complete",
		"--summary", "Did Y",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(pl.RecentRuns))
	}
	entry := pl.RecentRuns[0]
	if entry.Summary != "Did Y" {
		t.Errorf("Summary = %q, want Did Y", entry.Summary)
	}
	if entry.LapsCompleted != nil {
		t.Errorf("expected no LapsCompleted, got %v", entry.LapsCompleted)
	}
}
