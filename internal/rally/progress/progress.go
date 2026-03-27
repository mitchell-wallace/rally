package progress

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mitchell-wallace/rally/internal/app"

	"gopkg.in/yaml.v3"
)

type SessionProgress struct {
	SessionID      int      `yaml:"session_id"`
	BatchID        int      `yaml:"batch_id"`
	IterationIndex int      `yaml:"iteration_index"`
	Agent          string   `yaml:"agent"`
	Status         string   `yaml:"status"`
	StartedAt      string   `yaml:"started_at,omitempty"`
	EndedAt        string   `yaml:"ended_at,omitempty"`
	RuntimeSeconds int      `yaml:"runtime_seconds,omitempty"`
	Summary        string   `yaml:"summary,omitempty"`
	FilesTouched   []string `yaml:"files_touched,omitempty"`
	Commits        []string `yaml:"commits,omitempty"`
	FollowUps      []string `yaml:"follow_ups,omitempty"`
	MessageIDs     []int    `yaml:"message_ids,omitempty"`
	TranscriptPath string   `yaml:"transcript_path,omitempty"`
}

type RepoProgress struct {
	Version        int               `yaml:"version"`
	UpdatedAt      string            `yaml:"updated_at"`
	ActiveBatch    map[string]any    `yaml:"active_batch,omitempty"`
	HistoryWindow  int               `yaml:"history_window"`
	RecentSessions []SessionProgress `yaml:"recent_sessions"`
}

type SessionMeta struct {
	Version int             `yaml:"version"`
	Session SessionProgress `yaml:"session"`
}

type RecordInput struct {
	Summary      string   `yaml:"summary"`
	FilesTouched []string `yaml:"files_touched"`
	Commits      []string `yaml:"commits"`
	FollowUps    []string `yaml:"follow_ups"`
	Status       string   `yaml:"status"`
}

func SessionMetaPath(dataDir string, sessionID int) string {
	return filepath.Join(app.SessionDir(dataDir, sessionID), "meta.yaml")
}

func TranscriptPath(dataDir string, sessionID int) string {
	return filepath.Join(app.SessionDir(dataDir, sessionID), "terminal.log")
}

func EnsureSessionDir(dataDir string, sessionID int) (string, error) {
	dir := app.SessionDir(dataDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func ReadSessionMeta(path string) (SessionMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionMeta{}, err
	}
	var meta SessionMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return SessionMeta{}, err
	}
	return meta, nil
}

func WriteSessionMeta(path string, meta SessionMeta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func UpdateSessionMeta(dataDir string, sessionID int, update func(*SessionMeta) error) error {
	path := SessionMetaPath(dataDir, sessionID)
	meta := SessionMeta{Version: app.SchemaVersion}
	if _, err := os.Stat(path); err == nil {
		loaded, loadErr := ReadSessionMeta(path)
		if loadErr != nil {
			return loadErr
		}
		meta = loaded
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if meta.Version == 0 {
		meta.Version = app.SchemaVersion
	}
	if err := update(&meta); err != nil {
		return err
	}
	return WriteSessionMeta(path, meta)
}

func ParseRecordInput(r io.Reader) (RecordInput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return RecordInput{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return RecordInput{}, nil
	}
	var input RecordInput
	if err := yaml.Unmarshal(data, &input); err != nil {
		return RecordInput{}, err
	}
	return input, nil
}

func ApplyRecord(meta *SessionMeta, input RecordInput) {
	if input.Summary != "" {
		meta.Session.Summary = input.Summary
	}
	if len(input.FilesTouched) > 0 {
		meta.Session.FilesTouched = dedupe(input.FilesTouched)
	}
	if len(input.Commits) > 0 {
		meta.Session.Commits = dedupe(input.Commits)
	}
	if len(input.FollowUps) > 0 {
		meta.Session.FollowUps = dedupe(input.FollowUps)
	}
	if input.Status != "" {
		meta.Session.Status = input.Status
	}
}

func RebuildRepoProgress(dataDir, repoProgressPath string, activeBatch map[string]any) (RepoProgress, error) {
	sessionPattern := filepath.Join(dataDir, "sessions", "session-*", "meta.yaml")
	paths, err := filepath.Glob(sessionPattern)
	if err != nil {
		return RepoProgress{}, err
	}

	sessions := make([]SessionProgress, 0, len(paths))
	for _, path := range paths {
		meta, err := ReadSessionMeta(path)
		if err != nil {
			return RepoProgress{}, fmt.Errorf("read session meta %s: %w", path, err)
		}
		sessions = append(sessions, meta.Session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].SessionID < sessions[j].SessionID
	})
	if len(sessions) > app.RepoHistoryWindow {
		sessions = sessions[len(sessions)-app.RepoHistoryWindow:]
	}

	repo := RepoProgress{
		Version:        app.SchemaVersion,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
		ActiveBatch:    activeBatch,
		HistoryWindow:  app.RepoHistoryWindow,
		RecentSessions: sessions,
	}

	if err := os.MkdirAll(filepath.Dir(repoProgressPath), 0o755); err != nil {
		return RepoProgress{}, err
	}
	encoded, err := yaml.Marshal(repo)
	if err != nil {
		return RepoProgress{}, err
	}
	if err := os.WriteFile(repoProgressPath, encoded, 0o644); err != nil {
		return RepoProgress{}, err
	}
	return repo, nil
}

func ValidateRepoProgress(repoProgressPath string) error {
	data, err := os.ReadFile(repoProgressPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var repo RepoProgress
	return yaml.Unmarshal(data, &repo)
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}
