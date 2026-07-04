package progress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	if rs.OutingID != "" {
		t.Errorf("OutingID = %q, want empty", rs.OutingID)
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
	rs := &OutingState{
		OutingID:     "run-42",
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
	if loaded.OutingID != "run-42" {
		t.Errorf("OutingID = %q, want run-42", loaded.OutingID)
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
	rs := &OutingState{OutingID: "run-1"}
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

func TestClearActiveTryPreservesOutingStateFields(t *testing.T) {
	rs := &OutingState{
		OutingID:        "relay-7-run-3",
		PinnedLapID:     "lap-pin",
		RecordedLaps:    []string{"lap-a", "lap-b"},
		LapsAttempted:   []LapAttempt{{LapID: "lap-a", Timestamp: "2026-06-18T12:00:00Z"}},
		HandoffState:    1,
		SessionID:       "sess-123",
		ActiveRelayID:   7,
		ActiveOutingID:  3,
		ActiveTryID:     11,
		ActiveLogPath:   "/tmp/try-11.log",
		ActiveStartedAt: "2026-06-18T12:01:00Z",
	}

	rs.ClearActiveTry()

	if rs.ActiveRelayID != 0 || rs.ActiveOutingID != 0 || rs.ActiveTryID != 0 || rs.ActiveLogPath != "" || rs.ActiveStartedAt != "" {
		t.Fatalf("active fields not cleared: relay=%d run=%d try=%d log=%q started=%q", rs.ActiveRelayID, rs.ActiveOutingID, rs.ActiveTryID, rs.ActiveLogPath, rs.ActiveStartedAt)
	}
	if rs.OutingID != "relay-7-run-3" {
		t.Fatalf("OutingID = %q, want preserved", rs.OutingID)
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

func TestOutingStateSessionID(t *testing.T) {
	tmp := t.TempDir()
	rs := &OutingState{
		OutingID:  "run-1",
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

func TestOutingStateOutingIDJSONCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "old only", input: `{"run_id":"legacy"}`, want: "legacy"},
		{name: "new only", input: `{"outing_id":"new"}`, want: "new"},
		{name: "equal both", input: `{"run_id":"same","outing_id":"same"}`, want: "same"},
		{name: "conflicting both", input: `{"run_id":"old","outing_id":"new"}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state OutingState
			err := json.Unmarshal([]byte(tt.input), &state)
			if tt.wantErr {
				if err == nil {
					t.Fatal("json.Unmarshal succeeded, want error")
				}
				if !strings.Contains(err.Error(), "outing_id") || !strings.Contains(err.Error(), "run_id") {
					t.Fatalf("error %q does not name both keys", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal error: %v", err)
			}
			if state.OutingID != tt.want {
				t.Fatalf("OutingID = %q, want %q", state.OutingID, tt.want)
			}
		})
	}
}

func TestOutingStateActiveOutingIDJSONCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "old only", input: `{"active_run_id":7}`, want: 7},
		{name: "new only", input: `{"active_outing_id":8}`, want: 8},
		{name: "equal both", input: `{"active_run_id":9,"active_outing_id":9}`, want: 9},
		{name: "conflicting both", input: `{"active_run_id":9,"active_outing_id":10}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state OutingState
			err := json.Unmarshal([]byte(tt.input), &state)
			if tt.wantErr {
				if err == nil {
					t.Fatal("json.Unmarshal succeeded, want error")
				}
				if !strings.Contains(err.Error(), "active_outing_id") || !strings.Contains(err.Error(), "active_run_id") {
					t.Fatalf("error %q does not name both keys", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal error: %v", err)
			}
			if state.ActiveOutingID != tt.want {
				t.Fatalf("ActiveOutingID = %d, want %d", state.ActiveOutingID, tt.want)
			}
		})
	}
}

func TestOutingStateMarshalWritesOnlyOutingKeys(t *testing.T) {
	data, err := json.Marshal(OutingState{OutingID: "new", ActiveOutingID: 7})
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	for _, key := range []string{"outing_id", "active_outing_id"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("state JSON missing %s: %s", key, data)
		}
	}
	for _, key := range []string{"run_id", "active_run_id"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("state JSON unexpectedly contains %s: %s", key, data)
		}
	}
}

func TestLegacyRunStateLoadsAndFreshWriteUsesOnlyOutingKeys(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(RunStatePath(tmp)), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	legacy := `{"run_id":"legacy","active_run_id":7,"active_relay_id":1,"active_try_id":2,"handoff_state":1,"recorded_laps":["lap-1"]}`
	if err := os.WriteFile(RunStatePath(tmp), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
	state, err := LoadRunState(tmp)
	if err != nil {
		t.Fatalf("LoadRunState legacy error: %v", err)
	}
	if state.OutingID != "legacy" || state.ActiveOutingID != 7 {
		t.Fatalf("legacy state outing IDs = %q/%d, want legacy/7", state.OutingID, state.ActiveOutingID)
	}
	state.OutingID = "fresh"
	state.ActiveOutingID = 8
	if err := SaveRunState(tmp, state); err != nil {
		t.Fatalf("SaveRunState error: %v", err)
	}
	data := mustReadRunStateString(t, RunStatePath(tmp))
	for _, key := range []string{`"run_id"`, `"active_run_id"`} {
		if strings.Contains(data, key) {
			t.Fatalf("fresh state contains legacy key %s: %s", key, data)
		}
	}
	for _, want := range []string{`"outing_id": "fresh"`, `"active_outing_id": 8`} {
		if !strings.Contains(data, want) {
			t.Fatalf("fresh state missing %s: %s", want, data)
		}
	}
}

func mustReadRunStateString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
