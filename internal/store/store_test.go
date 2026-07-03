package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheReload(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	_ = store.AppendTry(TryRecord{ID: 1, AgentType: "claude", Summary: "try1"})
	_ = store.AppendRelay(RelayRecord{ID: 1, TargetIterations: 5})
	_ = store.AddMessage(MessageRecord{ID: 1, Body: "hello", Status: "pending"})
	_ = store.AppendAgentStatus(AgentStatusEvent{AgentType: "claude", EventType: "active"})

	// Reload cache
	store2, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(store2.cache.Tries) != 1 || store2.cache.Tries[0].ID != 1 {
		t.Fatalf("tries not reloaded: %v", store2.cache.Tries)
	}
	if len(store2.cache.Relays) != 1 || store2.cache.Relays[0].ID != 1 {
		t.Fatalf("relays not reloaded: %v", store2.cache.Relays)
	}
	if len(store2.cache.Messages) != 1 || store2.cache.Messages[0].ID != 1 {
		t.Fatalf("messages not reloaded: %v", store2.cache.Messages)
	}
	if len(store2.cache.AgentStatus) != 1 {
		t.Fatalf("agent status not reloaded: %v", store2.cache.AgentStatus)
	}

	if store2.GetTry(1) == nil {
		t.Fatal("GetTry(1) returned nil")
	}
	if store2.GetRelay(1) == nil {
		t.Fatal("GetRelay(1) returned nil")
	}
}

func TestStoreWindowing(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	// Tries are append-only and never windowed.
	tryCount := 505
	for i := 1; i <= tryCount; i++ {
		if err := store.AppendTry(TryRecord{ID: i}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.cache.Tries) != tryCount {
		t.Fatalf("expected %d tries, got %d", tryCount, len(store.cache.Tries))
	}
	if store.GetTry(1) == nil {
		t.Fatal("old try should not have been truncated")
	}
	if store.GetTry(tryCount) == nil {
		t.Fatal("newest try should exist")
	}

	// Relays are append-only and never windowed.
	relayCount := 53
	for i := 1; i <= relayCount; i++ {
		if err := store.AppendRelay(RelayRecord{ID: i}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.cache.Relays) != relayCount {
		t.Fatalf("expected %d relays, got %d", relayCount, len(store.cache.Relays))
	}

	// Agent status window
	for i := 1; i <= agentStatusWindowSize+3; i++ {
		if err := store.AppendAgentStatus(AgentStatusEvent{AgentType: "claude", EventType: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.cache.AgentStatus) != agentStatusWindowSize {
		t.Fatalf("expected %d agent status after window, got %d", agentStatusWindowSize, len(store.cache.AgentStatus))
	}

	// Verify files on disk
	read, _ := readJSONL[TryRecord](filepath.Join(rallyDir, "state", "tries.jsonl"))
	if len(read) != tryCount {
		t.Fatalf("tries file has %d records, expected %d", len(read), tryCount)
	}
}

func TestNewStoreAutoMigration(t *testing.T) {
	workspaceDir := t.TempDir()
	rallyDir := RallyDir(workspaceDir)
	if err := os.MkdirAll(rallyDir, 0o755); err != nil {
		t.Fatalf("failed to create rally dir: %v", err)
	}

	// Create a legacy tries.jsonl file
	legacyFile := filepath.Join(rallyDir, "tries.jsonl")
	recordJSON := `{"id":42,"run_id":1,"agent_type":"claude"}`
	if err := os.WriteFile(legacyFile, []byte(recordJSON+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write legacy tries: %v", err)
	}

	// Initialize store - this should trigger migration
	s, err := NewStore(rallyDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Verify the legacy file was moved
	if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
		t.Errorf("legacy tries.jsonl should have been moved from root")
	}

	migratedFile := filepath.Join(rallyDir, "state", "tries.jsonl")
	if _, err := os.Stat(migratedFile); err != nil {
		t.Errorf("migrated tries.jsonl should exist in state dir: %v", err)
	}

	// Verify cache contains the loaded try
	tries := s.RecentTries(10)
	if len(tries) != 1 || tries[0].ID != 42 {
		t.Errorf("expected 1 try with ID 42 in cache, got: %v", tries)
	}
}
