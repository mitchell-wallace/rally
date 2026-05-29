package progress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

// RunState tracks the current run's mutable state in .rally/run-state.json.
type RunState struct {
	RunID         string       `json:"run_id"`
	HandoffState  int          `json:"handoff_state"`
	RecordedLaps  []string     `json:"recorded_laps"`
	PinnedLapID   string       `json:"pinned_lap_id,omitempty"`
	LapsAttempted []LapAttempt `json:"laps_attempted,omitempty"`
	SessionID     string       `json:"session_id,omitempty"`
}

// LapAttempt records a laps command observed during the current run.
type LapAttempt struct {
	LapID     string `json:"lap_id"`
	Timestamp string `json:"timestamp"`
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
