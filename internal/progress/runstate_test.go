package progress

import (
	"os"
	"reflect"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRunStatePath(t *testing.T) {
	got := RunStatePath("/tmp/ws")
	want := store.RunStatePath("/tmp/ws")
	if got != want {
		t.Errorf("RunStatePath() = %q, want %q", got, want)
	}
	if got != "/tmp/ws/.rally/state/run-state.json" {
		t.Errorf("RunStatePath() = %q, want state path", got)
	}
}

func TestLoadRunStateMissingFile(t *testing.T) {
	tmp := t.TempDir()
	rs, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState unexpected error: %v", err)
	}
	if rs.RunID != "" {
		t.Errorf("RunID = %q, want empty", rs.RunID)
	}
	if rs.HandoffState != 0 {
		t.Errorf("HandoffState = %d, want 0", rs.HandoffState)
	}
	if len(rs.RecordedLaps) != 0 {
		t.Errorf("RecordedLaps = %v, want empty", rs.RecordedLaps)
	}
}

func TestSaveAndLoadRunState(t *testing.T) {
	tmp := t.TempDir()
	rs := &RunState{
		RunID:        "run-42",
		HandoffState: 1,
		RecordedLaps: []string{"lap-a", "lap-b"},
	}
	if err := SaveRunState(tmp, rs); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	loaded, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if loaded.RunID != "run-42" {
		t.Errorf("RunID = %q, want run-42", loaded.RunID)
	}
	if loaded.HandoffState != 1 {
		t.Errorf("HandoffState = %d, want 1", loaded.HandoffState)
	}
	if len(loaded.RecordedLaps) != 2 || loaded.RecordedLaps[0] != "lap-a" || loaded.RecordedLaps[1] != "lap-b" {
		t.Errorf("RecordedLaps = %v, want [lap-a lap-b]", loaded.RecordedLaps)
	}
}

func TestClearRunState(t *testing.T) {
	tmp := t.TempDir()
	rs := &RunState{RunID: "run-1"}
	if err := SaveRunState(tmp, rs); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}
	if err := ClearRunState(tmp); err != nil {
		t.Fatalf("ClearRunState error: %v", err)
	}
	_, err := os.Stat(RunStatePath(tmp))
	if !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, got err=%v", err)
	}
	// Clearing again should be a no-op.
	if err := ClearRunState(tmp); err != nil {
		t.Fatalf("ClearRunState on missing file error: %v", err)
	}
}

func TestClearActiveTryPreservesRunStateFields(t *testing.T) {
	rs := &RunState{
		RunID:           "relay-7-run-3",
		PinnedLapID:     "lap-pin",
		RecordedLaps:    []string{"lap-a", "lap-b"},
		LapsAttempted:   []LapAttempt{{LapID: "lap-a", Timestamp: "2026-06-18T12:00:00Z"}},
		HandoffState:    1,
		SessionID:       "sess-123",
		ActiveRelayID:   7,
		ActiveRunID:     3,
		ActiveTryID:     11,
		ActiveLogPath:   "/tmp/try-11.log",
		ActiveStartedAt: "2026-06-18T12:01:00Z",
	}

	rs.ClearActiveTry()

	if rs.ActiveRelayID != 0 || rs.ActiveRunID != 0 || rs.ActiveTryID != 0 || rs.ActiveLogPath != "" || rs.ActiveStartedAt != "" {
		t.Fatalf("active fields not cleared: relay=%d run=%d try=%d log=%q started=%q", rs.ActiveRelayID, rs.ActiveRunID, rs.ActiveTryID, rs.ActiveLogPath, rs.ActiveStartedAt)
	}
	if rs.RunID != "relay-7-run-3" {
		t.Fatalf("RunID = %q, want preserved", rs.RunID)
	}
	if rs.PinnedLapID != "lap-pin" {
		t.Fatalf("PinnedLapID = %q, want preserved", rs.PinnedLapID)
	}
	if got, want := rs.RecordedLaps, []string{"lap-a", "lap-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RecordedLaps = %v, want %v", got, want)
	}
	if got, want := rs.LapsAttempted, []LapAttempt{{LapID: "lap-a", Timestamp: "2026-06-18T12:00:00Z"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LapsAttempted = %v, want %v", got, want)
	}
	if rs.HandoffState != 1 {
		t.Fatalf("HandoffState = %d, want preserved", rs.HandoffState)
	}
	if rs.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q, want preserved", rs.SessionID)
	}
}

func TestRecordLap(t *testing.T) {
	tmp := t.TempDir()
	if err := RecordLap(tmp, "lap-1"); err != nil {
		t.Fatalf("RecordLap error: %v", err)
	}
	if err := RecordLap(tmp, "lap-2"); err != nil {
		t.Fatalf("RecordLap error: %v", err)
	}

	rs, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	want := []string{"lap-1", "lap-2"}
	if len(rs.RecordedLaps) != len(want) {
		t.Fatalf("RecordedLaps = %v, want %v", rs.RecordedLaps, want)
	}
	for i := range want {
		if rs.RecordedLaps[i] != want[i] {
			t.Errorf("RecordedLaps[%d] = %q, want %q", i, rs.RecordedLaps[i], want[i])
		}
	}
}

func TestSetHandoff(t *testing.T) {
	tmp := t.TempDir()
	if err := SetHandoff(tmp); err != nil {
		t.Fatalf("SetHandoff error: %v", err)
	}
	rs, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if rs.HandoffState != 1 {
		t.Errorf("HandoffState = %d, want 1", rs.HandoffState)
	}
}

func TestRunStateSessionID(t *testing.T) {
	tmp := t.TempDir()
	rs := &RunState{
		RunID:     "run-1",
		SessionID: "sess-abc",
	}
	if err := SaveRunState(tmp, rs); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	loaded, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if loaded.SessionID != "sess-abc" {
		t.Errorf("SessionID = %q, want sess-abc", loaded.SessionID)
	}

	loaded.SessionID = "sess-updated"
	if err := SaveRunState(tmp, loaded); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}

	loaded2, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState error: %v", err)
	}
	if loaded2.SessionID != "sess-updated" {
		t.Errorf("SessionID = %q, want sess-updated", loaded2.SessionID)
	}
}
