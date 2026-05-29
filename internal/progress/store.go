package progress

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mitchell-wallace/rally/internal/store"
)

// ProgressLog is the top-level structure for .rally/progress.yaml.
type ProgressLog struct {
	Version       int        `yaml:"version"`
	UpdatedAt     string     `yaml:"updated_at"`
	HistoryWindow int        `yaml:"history_window"`
	RecentRuns    []RunEntry `yaml:"recent_runs"`
}

// RunEntry represents a single finalized run in the progress log.
type RunEntry struct {
	RunID         string        `yaml:"run_id"`
	Summary       string        `yaml:"summary"`
	UpdatedAt     string        `yaml:"updated_at"`
	LapsCompleted interface{}   `yaml:"laps_completed,omitempty"`
	Handoff       *HandoffEntry `yaml:"handoff,omitempty"`
}

// HandoffEntry contains handoff-specific metadata.
type HandoffEntry struct {
	Summary       string   `yaml:"summary"`
	Followups     []string `yaml:"followups"`
	CreatedLapIDs []string `yaml:"created_lap_ids"`
}

// ProgressPath returns the path to progress.yaml for a workspace.
func ProgressPath(workspaceDir string) string {
	return store.ProgressPath(workspaceDir)
}

// LoadProgress reads the progress log. If it does not exist, a fresh log with
// version 1, history_window 50 and an empty recent_runs slice is returned.
func LoadProgress(workspaceDir string) (*ProgressLog, error) {
	path := ProgressPath(workspaceDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ProgressLog{
				Version:       1,
				UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
				HistoryWindow: 50,
				RecentRuns:    []RunEntry{},
			}, nil
		}
		return nil, err
	}
	var pl ProgressLog
	if err := yaml.Unmarshal(data, &pl); err != nil {
		return nil, fmt.Errorf("parse progress.yaml: %w", err)
	}
	return &pl, nil
}

// SaveProgress writes the progress log as YAML.
func SaveProgress(workspaceDir string, pl *ProgressLog) error {
	path := ProgressPath(workspaceDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(pl)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// AppendRunEntry loads the progress log, appends the entry, updates
// updated_at, trims recent_runs to history_window, and saves.
func AppendRunEntry(workspaceDir string, entry RunEntry) error {
	pl, err := LoadProgress(workspaceDir)
	if err != nil {
		return err
	}
	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	pl.RecentRuns = append(pl.RecentRuns, entry)
	pl.UpdatedAt = entry.UpdatedAt
	if len(pl.RecentRuns) > pl.HistoryWindow {
		pl.RecentRuns = pl.RecentRuns[len(pl.RecentRuns)-pl.HistoryWindow:]
	}
	return SaveProgress(workspaceDir, pl)
}
