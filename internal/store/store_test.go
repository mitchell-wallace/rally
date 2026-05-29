package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func setupTempStore(t *testing.T) (string, *Store) {
	t.Helper()
	dir := t.TempDir()
	rallyDir := filepath.Join(dir, ".rally")
	if err := os.MkdirAll(rallyDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Init a git repo so commit-then-truncate works.
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config email: %v\n%s", err, out)
	}
	cmd = exec.Command("git", "config", "user.name", "Test")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config name: %v\n%s", err, out)
	}

	store, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	return rallyDir, store
}

func TestJSONLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tries.jsonl")

	recs := []TryRecord{
		{ID: 1, RunID: 1, AgentType: "claude", Summary: "first"},
		{ID: 2, RunID: 1, AgentType: "codex", Summary: "second"},
	}

	for _, r := range recs {
		if err := appendJSONL(path, r); err != nil {
			t.Fatal(err)
		}
	}

	read, err := readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("expected 2 records, got %d", len(read))
	}
	if read[0].ID != 1 || read[1].ID != 2 {
		t.Fatalf("unexpected IDs: %v", read)
	}

	// Test rewrite
	if err := rewriteJSONL(path, recs[:1]); err != nil {
		t.Fatal(err)
	}
	read, err = readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 1 || read[0].ID != 1 {
		t.Fatalf("expected 1 record with ID 1, got %v", read)
	}
}

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

func TestCommitThenTruncate(t *testing.T) {
	rallyDir, _ := setupTempStore(t)
	path := filepath.Join(rallyDir, "tries.jsonl")

	// Write more records than window
	for i := 1; i <= triesWindowSize+5; i++ {
		if err := appendJSONL(path, TryRecord{ID: i}); err != nil {
			t.Fatal(err)
		}
	}

	if err := commitThenTruncate(path, triesWindowSize); err != nil {
		t.Fatal(err)
	}

	read, err := readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != triesWindowSize {
		t.Fatalf("expected %d records after truncate, got %d", triesWindowSize, len(read))
	}
	// Should keep the most recent records
	if read[0].ID != 6 {
		t.Fatalf("expected first kept ID to be 6, got %d", read[0].ID)
	}
	if read[len(read)-1].ID != triesWindowSize+5 {
		t.Fatalf("expected last kept ID to be %d, got %d", triesWindowSize+5, read[len(read)-1].ID)
	}

	// Verify git commits exist
	cmd := exec.Command("git", "-C", filepath.Dir(path), "log", "--oneline")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("expected git commits")
	}
}

func TestMessageInPlaceUpdate(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	_ = store.AddMessage(MessageRecord{ID: 1, Body: "hello", Status: "pending", Position: 1})
	_ = store.AddMessage(MessageRecord{ID: 2, Body: "world", Status: "pending", Position: 2})

	// Update message 1
	m1 := store.GetMessages()[0]
	m1.Status = "addressed"
	m1.ConsumedByRunID = intPtr(5)
	if err := store.UpdateMessage(m1); err != nil {
		t.Fatal(err)
	}

	// Reload and verify
	store2, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	msgs := store2.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	var found bool
	for _, m := range msgs {
		if m.ID == 1 {
			found = true
			if m.Status != "addressed" {
				t.Fatalf("expected status addressed, got %s", m.Status)
			}
			if m.ConsumedByRunID == nil || *m.ConsumedByRunID != 5 {
				t.Fatalf("unexpected ConsumedByRunID: %v", m.ConsumedByRunID)
			}
		}
	}
	if !found {
		t.Fatal("message 1 not found after reload")
	}
}

func TestAgentStatusReplay(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	_ = store.AppendAgentStatus(AgentStatusEvent{AgentType: "claude", Model: "test-model", EventType: "active", Timestamp: "t1"})
	_ = store.AppendAgentStatus(AgentStatusEvent{AgentType: "claude", Model: "test-model", EventType: "paused", Timestamp: "t2"})
	_ = store.AppendAgentStatus(AgentStatusEvent{AgentType: "codex", Model: "test-model", EventType: "active", Timestamp: "t3"})

	claudeEvents, err := store.GetAgentStatus("claude", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(claudeEvents) != 2 {
		t.Fatalf("expected 2 claude events, got %d", len(claudeEvents))
	}
	if claudeEvents[0].EventType != "active" || claudeEvents[1].EventType != "paused" {
		t.Fatalf("unexpected claude events: %v", claudeEvents)
	}

	codexEvents, err := store.GetAgentStatus("codex", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(codexEvents) != 1 || codexEvents[0].EventType != "active" {
		t.Fatalf("unexpected codex events: %v", codexEvents)
	}

	// Reload and verify
	store2, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store2.GetAgentStatus("claude", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatal("claude events not persisted")
	}
}

func TestPendingMessageExemption(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	// Add many pending messages beyond window
	for i := 1; i <= messagesWindowSize+10; i++ {
		if err := store.AddMessage(MessageRecord{ID: i, Body: "msg", Status: "pending", Position: i}); err != nil {
			t.Fatal(err)
		}
	}

	// All pending messages should still be present
	if len(store.GetMessages()) != messagesWindowSize+10 {
		t.Fatalf("expected %d pending messages, got %d", messagesWindowSize+10, len(store.GetMessages()))
	}

	// Now resolve a few messages - truncation should not drop pending
	for i := 1; i <= messagesWindowSize+5; i++ {
		m := store.GetMessages()[i-1]
		m.Status = "addressed"
		if err := store.UpdateMessage(m); err != nil {
			t.Fatal(err)
		}
	}

	// Should still have all messages (pending + recent addressed)
	msgs := store.GetMessages()
	pendingCount := 0
	for _, m := range msgs {
		if m.Status == "pending" {
			pendingCount++
		}
	}
	if pendingCount != 5 {
		t.Fatalf("expected 5 pending messages to survive, got %d", pendingCount)
	}

	// Reload and verify
	store2, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	msgs = store2.GetMessages()
	pendingCount = 0
	for _, m := range msgs {
		if m.Status == "pending" {
			pendingCount++
		}
	}
	if pendingCount != 5 {
		t.Fatalf("expected 5 pending messages after reload, got %d", pendingCount)
	}

	// Verify file was truncated for resolved messages
	read, err := readJSONL[MessageRecord](filepath.Join(rallyDir, "messages.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(read) > messagesWindowSize+5 {
		t.Fatalf("expected at most %d records after truncate, got %d", messagesWindowSize+5, len(read))
	}
}

func TestPendingMessagesSorted(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	_ = store.AddMessage(MessageRecord{ID: 1, Body: "a", Status: "pending", Position: 3})
	_ = store.AddMessage(MessageRecord{ID: 2, Body: "b", Status: "pending", Position: 1})
	_ = store.AddMessage(MessageRecord{ID: 3, Body: "c", Status: "pending", Position: 2})

	pending := store.PendingMessages()
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(pending))
	}
	for i, p := range pending {
		if p.Position != i+1 {
			t.Fatalf("expected position %d at index %d, got %d", i+1, i, p.Position)
		}
	}

	// After reload
	store2, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	pending = store2.PendingMessages()
	positions := make([]int, len(pending))
	for i, p := range pending {
		positions[i] = p.Position
	}
	if !sort.IntsAreSorted(positions) {
		t.Fatalf("pending messages not sorted: %v", positions)
	}
}

func TestRecentTriesAndRelays(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	for i := 1; i <= 5; i++ {
		_ = store.AppendTry(TryRecord{ID: i})
		_ = store.AppendRelay(RelayRecord{ID: i})
	}

	recent := store.RecentTries(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent tries, got %d", len(recent))
	}
	if recent[0].ID != 3 || recent[2].ID != 5 {
		t.Fatalf("unexpected recent tries: %v", recent)
	}

	recentRelays := store.RecentRelays(2)
	if len(recentRelays) != 2 || recentRelays[0].ID != 4 {
		t.Fatalf("unexpected recent relays: %v", recentRelays)
	}

	// Reload and verify
	store2, err := NewStore(rallyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(store2.RecentTries(10)) != 5 {
		t.Fatal("recent tries not correct after reload")
	}
}

func TestStoreWindowing(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	// Tries window
	for i := 1; i <= triesWindowSize+5; i++ {
		if err := store.AppendTry(TryRecord{ID: i}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.cache.Tries) != triesWindowSize {
		t.Fatalf("expected %d tries after window, got %d", triesWindowSize, len(store.cache.Tries))
	}
	if store.GetTry(1) != nil {
		t.Fatal("old try should have been truncated")
	}
	if store.GetTry(triesWindowSize+5) == nil {
		t.Fatal("newest try should exist")
	}

	// Relays window
	for i := 1; i <= relaysWindowSize+3; i++ {
		if err := store.AppendRelay(RelayRecord{ID: i}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.cache.Relays) != relaysWindowSize {
		t.Fatalf("expected %d relays after window, got %d", relaysWindowSize, len(store.cache.Relays))
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
	read, _ := readJSONL[TryRecord](filepath.Join(rallyDir, "tries.jsonl"))
	if len(read) != triesWindowSize {
		t.Fatalf("tries file has %d records, expected %d", len(read), triesWindowSize)
	}
}

func TestAddMessageAutoPosition(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	_ = store.AddMessage(MessageRecord{ID: 1, Status: "pending"})
	_ = store.AddMessage(MessageRecord{ID: 2, Status: "pending"})
	_ = store.AddMessage(MessageRecord{ID: 3, Status: "pending", Position: 10})
	_ = store.AddMessage(MessageRecord{ID: 4, Status: "pending"})

	msgs := store.GetMessages()
	if msgs[0].Position != 1 {
		t.Fatalf("expected position 1, got %d", msgs[0].Position)
	}
	if msgs[1].Position != 2 {
		t.Fatalf("expected position 2, got %d", msgs[1].Position)
	}
	if msgs[3].Position != 11 {
		t.Fatalf("expected position 11, got %d", msgs[3].Position)
	}

	store2, _ := NewStore(rallyDir)
	msgs = store2.GetMessages()
	if msgs[3].Position != 11 {
		t.Fatalf("position not persisted: %d", msgs[3].Position)
	}
}

func TestUpdateMessageNotFound(t *testing.T) {
	_, store := setupTempStore(t)
	err := store.UpdateMessage(MessageRecord{ID: 99, Status: "addressed"})
	if err == nil {
		t.Fatal("expected error for missing message")
	}
}

func intPtr(i int) *int {
	return &i
}

func TestRelayScopedMessages(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	_ = store.AddMessage(MessageRecord{ID: 1, Body: "relay-msg", Status: "pending", Position: 1, Scope: "relay"})
	_ = store.AddMessage(MessageRecord{ID: 2, Body: "run-msg", Status: "pending", Position: 2, Scope: "run"})
	_ = store.AddMessage(MessageRecord{ID: 3, Body: "relay-msg2", Status: "pending", Position: 3, Scope: "relay"})
	_ = store.AddMessage(MessageRecord{ID: 4, Body: "no-scope", Status: "pending", Position: 4})

	relayMsgs := store.RelayScopedMessages()
	if len(relayMsgs) != 2 {
		t.Fatalf("expected 2 relay-scoped messages, got %d", len(relayMsgs))
	}
	if relayMsgs[0].ID != 1 || relayMsgs[1].ID != 3 {
		t.Fatalf("unexpected relay-scoped messages: %v", relayMsgs)
	}

	// PendingMessages should still return all pending messages
	pending := store.PendingMessages()
	if len(pending) != 4 {
		t.Fatalf("expected 4 pending messages, got %d", len(pending))
	}

	// After reload
	store2, _ := NewStore(rallyDir)
	relayMsgs = store2.RelayScopedMessages()
	if len(relayMsgs) != 2 {
		t.Fatalf("expected 2 relay-scoped messages after reload, got %d", len(relayMsgs))
	}
}

func TestRelayScopedMessages_Empty(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	_ = store.AddMessage(MessageRecord{ID: 1, Body: "run-msg", Status: "pending", Position: 1, Scope: "run"})

	relayMsgs := store.RelayScopedMessages()
	if len(relayMsgs) != 0 {
		t.Fatalf("expected 0 relay-scoped messages, got %d", len(relayMsgs))
	}

	// Reload
	store2, _ := NewStore(rallyDir)
	relayMsgs = store2.RelayScopedMessages()
	if len(relayMsgs) != 0 {
		t.Fatalf("expected 0 relay-scoped messages after reload, got %d", len(relayMsgs))
	}
}

func TestCommitThenTruncateNoGitRepo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tries.jsonl")

	for i := 1; i <= 10; i++ {
		if err := appendJSONL(path, TryRecord{ID: i}); err != nil {
			t.Fatal(err)
		}
	}

	// Should not error even though not in a git repo
	if err := commitThenTruncate(path, 5); err != nil {
		t.Fatal(err)
	}

	read, err := readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 5 {
		t.Fatalf("expected 5 records, got %d", len(read))
	}
}

func TestReadJSONLMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.jsonl")
	recs, err := readJSONL[TryRecord](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected 0 records for missing file, got %d", len(recs))
	}
}

func TestMessagePositionTimeOrdering(t *testing.T) {
	// Ensure messages with same position are handled deterministically
	rallyDir, store := setupTempStore(t)

	_ = store.AddMessage(MessageRecord{ID: 1, Body: "a", Status: "pending", Position: 1})
	_ = store.AddMessage(MessageRecord{ID: 2, Body: "b", Status: "pending", Position: 1})

	pending := store.PendingMessages()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}
	// Should preserve insertion order for same position
	if pending[0].ID != 1 || pending[1].ID != 2 {
		t.Fatalf("unexpected order: %v", pending)
	}

	// Reload should preserve file order
	store2, _ := NewStore(rallyDir)
	pending = store2.PendingMessages()
	if pending[0].ID != 1 || pending[1].ID != 2 {
		t.Fatalf("order changed after reload: %v", pending)
	}
}

func TestAgentStatusPersistsAcrossRelays(t *testing.T) {
	rallyDir, _ := setupTempStore(t)

	store1, _ := NewStore(rallyDir)
	_ = store1.AppendAgentStatus(AgentStatusEvent{AgentType: "claude", Model: "test-model", EventType: "paused", Timestamp: time.Now().Format(time.RFC3339)})

	store2, _ := NewStore(rallyDir)
	_ = store2.AppendAgentStatus(AgentStatusEvent{AgentType: "claude", Model: "test-model", EventType: "unfrozen", Timestamp: time.Now().Format(time.RFC3339)})

	store3, _ := NewStore(rallyDir)
	events, err := store3.GetAgentStatus("claude", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events across reloads, got %d", len(events))
	}
}

func TestAgentStatusTruncationPreservesFreezeTimestamps(t *testing.T) {
	_, store := setupTempStore(t)

	frozenAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.AppendAgentStatus(AgentStatusEvent{
		AgentType: "claude",
		Model:     "sonnet",
		EventType: "frozen",
		Timestamp: frozenAt.Format(time.RFC3339),
		RelayID:   1,
	})

	for i := 0; i < agentStatusWindowSize+10; i++ {
		_ = store.AppendAgentStatus(AgentStatusEvent{
			AgentType: "codex",
			Model:     "",
			EventType: "paused",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			RelayID:   1,
		})
	}

	events, err := store.GetAgentStatus("claude", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected frozen event to be preserved after truncation")
	}

	foundSummary := false
	for _, e := range events {
		if e.EventType == "frozen" && e.Reason == "truncation summary" {
			foundSummary = true
			if e.Timestamp != frozenAt.Format(time.RFC3339) {
				t.Fatalf("expected preserved timestamp %q, got %q", frozenAt.Format(time.RFC3339), e.Timestamp)
			}
		}
	}
	if !foundSummary {
		t.Fatal("expected truncation summary event for frozen agent")
	}
}

func TestAgentStatusTruncationPreservesProbationTimestamps(t *testing.T) {
	_, store := setupTempStore(t)

	probationAt := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)
	_ = store.AppendAgentStatus(AgentStatusEvent{
		AgentType: "opencode",
		Model:     "gemini",
		EventType: "probation",
		Timestamp: probationAt.Format(time.RFC3339),
		RelayID:   1,
	})

	for i := 0; i < agentStatusWindowSize+10; i++ {
		_ = store.AppendAgentStatus(AgentStatusEvent{
			AgentType: "codex",
			Model:     "",
			EventType: "paused",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			RelayID:   1,
		})
	}

	events, err := store.GetAgentStatus("opencode", "gemini")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected probation event to be preserved after truncation")
	}

	foundSummary := false
	for _, e := range events {
		if e.EventType == "probation" && e.Reason == "truncation summary" {
			foundSummary = true
			if e.Timestamp != probationAt.Format(time.RFC3339) {
				t.Fatalf("expected preserved timestamp %q, got %q", probationAt.Format(time.RFC3339), e.Timestamp)
			}
		}
	}
	if !foundSummary {
		t.Fatal("expected truncation summary event for probation agent")
	}
}
