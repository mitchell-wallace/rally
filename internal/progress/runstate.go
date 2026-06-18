package progress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

// RunState tracks the current run's mutable state in .rally/state/run-state.json.
type RunState struct {
	RunID           string       `json:"run_id"`
	HandoffState    int          `json:"handoff_state"`
	RecordedLaps    []string     `json:"recorded_laps"`
	PinnedLapID     string       `json:"pinned_lap_id,omitempty"`
	LapsAttempted   []LapAttempt `json:"laps_attempted,omitempty"`
	SessionID       string       `json:"session_id,omitempty"`
	ActiveRelayID   int          `json:"active_relay_id,omitempty"`
	ActiveRunID     int          `json:"active_run_id,omitempty"`
	ActiveTryID     int          `json:"active_try_id,omitempty"`
	ActiveLogPath   string       `json:"active_log_path,omitempty"`
	ActiveStartedAt string       `json:"active_started_at,omitempty"`
}

// LapAttempt records a laps command observed during the current run.
type LapAttempt struct {
	LapID     string `json:"lap_id"`
	Timestamp string `json:"timestamp"`
}

// ActiveTryMetadata identifies the in-flight try log before the try record is
// appended to history.
type ActiveTryMetadata struct {
	RelayID   int
	RunID     int
	TryID     int
	LogPath   string
	StartedAt time.Time
}

// RunStatePath returns the path to run-state.json for a workspace.
func RunStatePath(workspaceDir string) string {
	return store.RunStatePath(workspaceDir)
}

// LoadRunState reads the run-state file. If it does not exist, a fresh
// RunState with HandoffState=0 and an empty RecordedLaps slice is returned.
func LoadRunState(workspaceDir string) (*RunState, error) {
	path := RunStatePath(workspaceDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RunState{HandoffState: 0, RecordedLaps: []string{}}, nil
		}
		return nil, err
	}
	var rs RunState
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("parse run-state.json: %w", err)
	}
	return &rs, nil
}

// SaveRunState writes the run-state file as indented JSON.
func SaveRunState(workspaceDir string, rs *RunState) error {
	path := RunStatePath(workspaceDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ClearRunState removes the run-state file if it exists.
func ClearRunState(workspaceDir string) error {
	path := RunStatePath(workspaceDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SetActiveTry records the try currently executing so live tailing can target
// its log before the final try record exists.
func SetActiveTry(workspaceDir string, active ActiveTryMetadata) error {
	rs, err := LoadRunState(workspaceDir)
	if err != nil {
		return err
	}
	rs.ActiveRelayID = active.RelayID
	rs.ActiveRunID = active.RunID
	rs.ActiveTryID = active.TryID
	rs.ActiveLogPath = active.LogPath
	rs.ActiveStartedAt = active.StartedAt.UTC().Format(time.RFC3339)
	return SaveRunState(workspaceDir, rs)
}

// ClearActiveTry clears only active-tail metadata from run-state. It preserves
// run, lap, handoff, and session fields needed by finalization/retry handling.
func ClearActiveTry(workspaceDir string) error {
	if _, err := os.Stat(RunStatePath(workspaceDir)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rs, err := LoadRunState(workspaceDir)
	if err != nil {
		return err
	}
	rs.ClearActiveTry()
	return SaveRunState(workspaceDir, rs)
}

// ClearActiveTry clears only active-tail fields on an in-memory run-state.
func (rs *RunState) ClearActiveTry() {
	rs.ActiveRelayID = 0
	rs.ActiveRunID = 0
	rs.ActiveTryID = 0
	rs.ActiveLogPath = ""
	rs.ActiveStartedAt = ""
}

// RecordLap appends a lap ID to the run state's RecordedLaps.
func RecordLap(workspaceDir string, lapID string) error {
	rs, err := LoadRunState(workspaceDir)
	if err != nil {
		return err
	}
	rs.RecordedLaps = append(rs.RecordedLaps, lapID)
	rs.LapsAttempted = append(rs.LapsAttempted, LapAttempt{
		LapID:     lapID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return SaveRunState(workspaceDir, rs)
}

// SetHandoff sets HandoffState to 1.
func SetHandoff(workspaceDir string) error {
	rs, err := LoadRunState(workspaceDir)
	if err != nil {
		return err
	}
	rs.HandoffState = 1
	rs.LapsAttempted = append(rs.LapsAttempted, LapAttempt{
		LapID:     rs.PinnedLapID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return SaveRunState(workspaceDir, rs)
}
