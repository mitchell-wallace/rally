package progress

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestProgressPath(t *testing.T) {
	got := ProgressPath("/tmp/ws")
	want := store.ProgressPath("/tmp/ws")
	if got != want {
		t.Errorf("ProgressPath() = %q, want %q", got, want)
	}
}

func TestLoadProgressMissingFile(t *testing.T) {
	tmp := t.TempDir()
	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress unexpected error: %v", err)
	}
	if pl.Version != 1 {
		t.Errorf("Version = %d, want 1", pl.Version)
	}
	if pl.HistoryWindow != 50 {
		t.Errorf("HistoryWindow = %d, want 50", pl.HistoryWindow)
	}
	if len(pl.RecentRuns) != 0 {
		t.Errorf("RecentRuns = %v, want empty", pl.RecentRuns)
	}
}

func TestSaveAndLoadProgress(t *testing.T) {
	tmp := t.TempDir()
	pl := &ProgressLog{
		Version:       1,
		UpdatedAt:     "2026-01-01T00:00:00Z",
		HistoryWindow: 50,
		RecentRuns: []RunEntry{
			{
				RunID:         "run-1",
				Summary:       "first run",
				UpdatedAt:     "2026-01-01T00:00:00Z",
				LapsCompleted: []string{"lap-a"},
				Handoff: &HandoffEntry{
					Summary:       "blocked",
					Followups:     []string{"fix auth"},
					CreatedLapIDs: []string{"lap-new"},
				},
			},
		},
	}
	if err := SaveProgress(tmp, pl); err != nil {
		t.Fatalf("SaveProgress error: %v", err)
	}

	loaded, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}
	if len(loaded.RecentRuns) != 1 {
		t.Fatalf("len(RecentRuns) = %d, want 1", len(loaded.RecentRuns))
	}
	entry := loaded.RecentRuns[0]
	if entry.RunID != "run-1" {
		t.Errorf("RunID = %q, want run-1", entry.RunID)
	}
	if entry.Summary != "first run" {
		t.Errorf("Summary = %q, want first run", entry.Summary)
	}
	if entry.Handoff == nil {
		t.Fatal("expected Handoff to be present")
	}
	if entry.Handoff.Summary != "blocked" {
		t.Errorf("Handoff.Summary = %q, want blocked", entry.Handoff.Summary)
	}
}

func TestAppendRunEntry(t *testing.T) {
	tmp := t.TempDir()
	if err := AppendRunEntry(tmp, RunEntry{RunID: "run-1", Summary: "s1"}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}
	if err := AppendRunEntry(tmp, RunEntry{RunID: "run-2", Summary: "s2"}); err != nil {
		t.Fatalf("AppendRunEntry error: %v", err)
	}

	pl, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(pl.RecentRuns) != 2 {
		t.Fatalf("len(RecentRuns) = %d, want 2", len(pl.RecentRuns))
	}
	if pl.RecentRuns[0].RunID != "run-1" {
		t.Errorf("RecentRuns[0].RunID = %q, want run-1", pl.RecentRuns[0].RunID)
	}
	if pl.RecentRuns[1].RunID != "run-2" {
		t.Errorf("RecentRuns[1].RunID = %q, want run-2", pl.RecentRuns[1].RunID)
	}
}

func TestAppendRunEntryTrimsHistoryWindow(t *testing.T) {
	tmp := t.TempDir()
	pl := &ProgressLog{Version: 1, HistoryWindow: 3, RecentRuns: []RunEntry{}}
	if err := SaveProgress(tmp, pl); err != nil {
		t.Fatalf("SaveProgress error: %v", err)
	}

	for i := 1; i <= 5; i++ {
		if err := AppendRunEntry(tmp, RunEntry{RunID: ""}); err != nil {
			t.Fatalf("AppendRunEntry error: %v", err)
		}
	}

	loaded, err := LoadProgress(tmp)
	if err != nil {
		t.Fatalf("LoadProgress error: %v", err)
	}
	if len(loaded.RecentRuns) != 3 {
		t.Fatalf("len(RecentRuns) = %d, want 3", len(loaded.RecentRuns))
	}
}

func TestLapsCompletedOmitted(t *testing.T) {
	entry := RunEntry{
		RunID:         "run-1",
		Summary:       "summary",
		LapsCompleted: nil,
	}
	data, err := yaml.Marshal(&entry)
	if err != nil {
		t.Fatalf("yaml.Marshal error: %v", err)
	}
	if strings.Contains(string(data), "laps_completed") {
		t.Errorf("expected laps_completed to be omitted, got:\n%s", string(data))
	}
}

func TestLapsCompletedNone(t *testing.T) {
	entry := RunEntry{
		RunID:         "run-1",
		Summary:       "summary",
		LapsCompleted: "none",
	}
	data, err := yaml.Marshal(&entry)
	if err != nil {
		t.Fatalf("yaml.Marshal error: %v", err)
	}
	if !strings.Contains(string(data), "laps_completed: none") {
		t.Errorf("expected laps_completed: none, got:\n%s", string(data))
	}
}

func TestLapsCompletedIDs(t *testing.T) {
	entry := RunEntry{
		RunID:         "run-1",
		Summary:       "summary",
		LapsCompleted: []string{"lap-a", "lap-b"},
	}
	data, err := yaml.Marshal(&entry)
	if err != nil {
		t.Fatalf("yaml.Marshal error: %v", err)
	}
	if !strings.Contains(string(data), "laps_completed:") {
		t.Errorf("expected laps_completed section, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "- lap-a") {
		t.Errorf("expected lap-a, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "- lap-b") {
		t.Errorf("expected lap-b, got:\n%s", string(data))
	}
}

func TestHandoffPresentAbsent(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		entry := RunEntry{
			RunID:   "run-1",
			Summary: "s",
			Handoff: &HandoffEntry{
				Summary:   "blocked",
				Followups: []string{"a", "b"},
			},
		}
		data, err := yaml.Marshal(&entry)
		if err != nil {
			t.Fatalf("yaml.Marshal error: %v", err)
		}
		if !strings.Contains(string(data), "handoff:") {
			t.Errorf("expected handoff section, got:\n%s", string(data))
		}
		if !strings.Contains(string(data), "followups:") {
			t.Errorf("expected followups section, got:\n%s", string(data))
		}
	})

	t.Run("absent", func(t *testing.T) {
		entry := RunEntry{RunID: "run-1", Summary: "s"}
		data, err := yaml.Marshal(&entry)
		if err != nil {
			t.Fatalf("yaml.Marshal error: %v", err)
		}
		if strings.Contains(string(data), "handoff:") {
			t.Errorf("expected no handoff section, got:\n%s", string(data))
		}
	})
}
