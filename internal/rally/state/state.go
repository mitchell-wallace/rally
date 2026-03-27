package state

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/mitchell-wallace/rally/internal/app"

	"gopkg.in/yaml.v3"
)

type BatchState struct {
	BatchID             int      `yaml:"batch_id"`
	TargetIterations    int      `yaml:"target_iterations"`
	CompletedIterations int      `yaml:"completed_iterations"`
	AgentMix            []string `yaml:"agent_mix"`
	StartedAt           string   `yaml:"started_at,omitempty"`
	EndedAt             string   `yaml:"ended_at,omitempty"`
}

type RepairMetadata struct {
	LastWarning string `yaml:"last_warning,omitempty"`
	LastRepair  string `yaml:"last_repair,omitempty"`
}

type State struct {
	SchemaVersion    int            `yaml:"schema_version"`
	ActiveBatch      *BatchState    `yaml:"active_batch,omitempty"`
	StopAfterCurrent bool           `yaml:"stop_after_current"`
	NextBatchID      int            `yaml:"next_batch_id"`
	NextSessionID    int            `yaml:"next_session_id"`
	NextMessageID    int            `yaml:"next_message_id"`
	NextEventID      int            `yaml:"next_event_id"`
	Repair           RepairMetadata `yaml:"repair"`
}

func Default() State {
	return State{
		SchemaVersion: app.SchemaVersion,
		NextBatchID:   1,
		NextSessionID: 1,
		NextMessageID: 1,
		NextEventID:   1,
	}
}

type Store struct {
	path string
}

func NewStore(dataDir string) *Store {
	return &Store{path: filepath.Join(dataDir, "state.yaml")}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return State{}, err
	}
	var st State
	if err := yaml.Unmarshal(data, &st); err != nil {
		return State{}, err
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = app.SchemaVersion
	}
	if st.NextBatchID == 0 {
		st.NextBatchID = 1
	}
	if st.NextSessionID == 0 {
		st.NextSessionID = 1
	}
	if st.NextMessageID == 0 {
		st.NextMessageID = 1
	}
	if st.NextEventID == 0 {
		st.NextEventID = 1
	}
	return st, nil
}

func (s *Store) Save(st State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	encoded, err := yaml.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, encoded, 0o644)
}
