package store

import (
	"path/filepath"
	"sort"
	"testing"
)

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
	read, err := readJSONL[MessageRecord](filepath.Join(rallyDir, "state", "messages.jsonl"))
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
