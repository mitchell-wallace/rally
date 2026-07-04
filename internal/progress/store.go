package progress

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

// OutingEntry represents a single finalized outing in the append-only summary log.
type OutingEntry struct {
	OutingID       string        `json:"outing_id"`
	Summary        string        `json:"summary"`
	Classification string        `json:"classification,omitempty"`
	UpdatedAt      string        `json:"updated_at"`
	LapsCompleted  interface{}   `json:"laps_completed,omitempty"`
	Handoff        *HandoffEntry `json:"handoff,omitempty"`
}

// HandoffEntry contains handoff-specific metadata.
type HandoffEntry struct {
	Summary       string   `json:"summary"`
	Followups     []string `json:"followups"`
	CreatedLapIDs []string `json:"created_lap_ids"`
}

// SummaryPath returns the path to summary.jsonl for a workspace.
func SummaryPath(workspaceDir string) string {
	return store.SummaryPath(workspaceDir)
}

// ProgressPath returns the current run-summary path. The name is retained for
// callers that still use the old progress terminology.
func ProgressPath(workspaceDir string) string {
	return SummaryPath(workspaceDir)
}

// UnmarshalJSON accepts legacy run_id while new writes use outing_id.
func (e *OutingEntry) UnmarshalJSON(data []byte) error {
	type outingEntryAlias OutingEntry
	var aux struct {
		outingEntryAlias
		OutingID *string `json:"outing_id"`
		RunID    *string `json:"run_id"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*e = OutingEntry(aux.outingEntryAlias)
	if err := assignCompatString(&e.OutingID, aux.OutingID, "outing_id", aux.RunID, "run_id"); err != nil {
		return err
	}
	return nil
}

// LoadSummaryEntries reads the append-only outing summary log. Missing files
// return an empty slice. Blank lines are ignored so a manually edited trailing
// newline is harmless.
func LoadSummaryEntries(workspaceDir string) ([]OutingEntry, error) {
	path := SummaryPath(workspaceDir)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []OutingEntry{}, nil
		}
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var entries []OutingEntry
	lineNumber := 0
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			if len(bytes.TrimSpace(line)) > 0 {
				var entry OutingEntry
				if unmarshalErr := json.Unmarshal(line, &entry); unmarshalErr != nil {
					return nil, fmt.Errorf("parse summary.jsonl line %d: %w", lineNumber, unmarshalErr)
				}
				entries = append(entries, entry)
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return nil, err
	}
	return entries, nil
}

// AppendOutingEntry appends a finalized outing or handoff summary as one JSON line.
func AppendOutingEntry(workspaceDir string, entry OutingEntry) error {
	path := SummaryPath(workspaceDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	entry.Summary = store.TruncateFinalSnippet(entry.Summary)
	if entry.Handoff != nil {
		entry.Handoff.Summary = store.TruncateFinalSnippet(entry.Handoff.Summary)
		for i := range entry.Handoff.Followups {
			entry.Handoff.Followups[i] = store.TruncateFinalSnippet(entry.Handoff.Followups[i])
		}
		if entry.Handoff.Followups == nil {
			entry.Handoff.Followups = []string{}
		}
		if entry.Handoff.CreatedLapIDs == nil {
			entry.Handoff.CreatedLapIDs = []string{}
		}
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func assignCompatString(dst *string, newValue *string, newKey string, oldValue *string, oldKey string) error {
	if newValue != nil && oldValue != nil && *newValue != *oldValue {
		return fmt.Errorf("conflicting %s and %s", newKey, oldKey)
	}
	switch {
	case newValue != nil:
		*dst = *newValue
	case oldValue != nil:
		*dst = *oldValue
	}
	return nil
}

func assignCompatInt(dst *int, newValue *int, newKey string, oldValue *int, oldKey string) error {
	if newValue != nil && oldValue != nil && *newValue != *oldValue {
		return fmt.Errorf("conflicting %s and %s", newKey, oldKey)
	}
	switch {
	case newValue != nil:
		*dst = *newValue
	case oldValue != nil:
		*dst = *oldValue
	}
	return nil
}
