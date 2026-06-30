package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestInspectResumeCurrentLayout(t *testing.T) {
	workspaceDir := t.TempDir()
	if err := os.MkdirAll(store.RallyDir(workspaceDir), 0o755); err != nil {
		t.Fatalf("mkdir rally dir: %v", err)
	}

	s, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.AppendRelay(store.RelayRecord{
		ID:                  1,
		TargetIterations:    8,
		CompletedIterations: 3,
		AgentMix:            "cc cx",
	}); err != nil {
		t.Fatalf("append unfinished relay: %v", err)
	}
	if err := s.AppendRelay(store.RelayRecord{
		ID:                  2,
		TargetIterations:    2,
		CompletedIterations: 2,
		AgentMix:            "op",
		EndedAt:             "2026-06-30T00:00:00Z",
	}); err != nil {
		t.Fatalf("append completed relay: %v", err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "codex",
		Model:     "default",
		EventType: "frozen",
		Timestamp: "2026-06-30T00:00:00Z",
		RelayID:   1,
		Reason:    "test freeze",
	}); err != nil {
		t.Fatalf("append agent status: %v", err)
	}

	info, err := InspectResume(workspaceDir)
	if err != nil {
		t.Fatalf("InspectResume: %v", err)
	}
	if !info.HasUnfinished {
		t.Fatal("HasUnfinished = false, want true")
	}
	if info.RelayID != 1 || info.CompletedIterations != 3 || info.TargetIterations != 8 || info.AgentMix != "cc cx" {
		t.Fatalf("ResumeInfo = %+v, want relay 1 at 3/8 with mix cc cx", info)
	}

	reloaded, err := store.NewStore(store.RallyDir(workspaceDir))
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	relay := reloaded.GetRelay(1)
	if relay == nil {
		t.Fatal("relay 1 missing after InspectResume")
	}
	if relay.EndedAt != "" {
		t.Fatalf("relay EndedAt = %q, want empty", relay.EndedAt)
	}
	status := reloaded.AllAgentStatus()
	if len(status) != 1 || status[0].EventType != "frozen" {
		t.Fatalf("agent status = %+v, want one frozen event", status)
	}
}

func TestInspectResumeLegacyTopLevelLayoutMigratesAndDoesNotMutateStatus(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := store.RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("mkdir rally dir: %v", err)
	}

	writeLegacyJSONL(t, filepath.Join(rallyDir, "relays.jsonl"),
		store.RelayRecord{ID: 1, TargetIterations: 4, CompletedIterations: 4, AgentMix: "cc", EndedAt: "2026-06-30T00:00:00Z"},
		store.RelayRecord{ID: 2, TargetIterations: 10, CompletedIterations: 6, AgentMix: "__routes__"},
	)
	writeLegacyJSONL(t, filepath.Join(rallyDir, "agent_status.jsonl"),
		store.AgentStatusEvent{AgentType: "claude", Model: "opus", EventType: "benched", Timestamp: "2026-06-30T00:00:00Z", RelayID: 2},
	)

	info, err := InspectResume(workspaceDir)
	if err != nil {
		t.Fatalf("InspectResume: %v", err)
	}
	if !info.HasUnfinished {
		t.Fatal("HasUnfinished = false, want true")
	}
	if info.RelayID != 2 || info.CompletedIterations != 6 || info.TargetIterations != 10 || info.AgentMix != "__routes__" {
		t.Fatalf("ResumeInfo = %+v, want relay 2 at 6/10 with mix __routes__", info)
	}

	if _, err := os.Stat(filepath.Join(rallyDir, "relays.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("legacy relays.jsonl stat error = %v, want not exist after migration", err)
	}
	if _, err := os.Stat(filepath.Join(rallyDir, "state", "relays.jsonl")); err != nil {
		t.Fatalf("migrated relays.jsonl missing: %v", err)
	}

	reloaded, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	relay := reloaded.GetRelay(2)
	if relay == nil {
		t.Fatal("relay 2 missing after InspectResume")
	}
	if relay.EndedAt != "" {
		t.Fatalf("relay EndedAt = %q, want empty", relay.EndedAt)
	}
	status := reloaded.AllAgentStatus()
	if len(status) != 1 || status[0].EventType != "benched" {
		t.Fatalf("agent status = %+v, want one benched event", status)
	}
}

func writeLegacyJSONL[T any](t *testing.T, path string, records ...T) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	for _, record := range records {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}
