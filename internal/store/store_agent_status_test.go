package store

import (
	"testing"
	"time"
)

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
		Model:     "glm-5.1",
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

	events, err := store.GetAgentStatus("opencode", "glm-5.1")
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

func TestAgentStatusTruncationPreservesBenchedEvent(t *testing.T) {
	_, store := setupTempStore(t)

	benchedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetAt := benchedAt.Add(72 * time.Hour)
	_ = store.AppendAgentStatus(AgentStatusEvent{
		AgentType:  "claude",
		Model:      "opus",
		EventType:  "benched",
		Timestamp:  benchedAt.Format(time.RFC3339),
		ResetAt:    resetAt.Format(time.RFC3339),
		QuotaScope: "claude",
		RelayID:    1,
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

	events, err := store.GetAgentStatus("claude", "opus")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected benched event to survive truncation (multi-day reset must not be dropped)")
	}

	foundSummary := false
	for _, e := range events {
		if e.EventType == "benched" && e.Reason == "truncation summary" {
			foundSummary = true
			if e.Timestamp != benchedAt.Format(time.RFC3339) {
				t.Fatalf("expected preserved timestamp %q, got %q", benchedAt.Format(time.RFC3339), e.Timestamp)
			}
			if e.ResetAt != resetAt.Format(time.RFC3339) {
				t.Fatalf("expected preserved reset_at %q, got %q", resetAt.Format(time.RFC3339), e.ResetAt)
			}
			if e.QuotaScope != "claude" {
				t.Fatalf("expected preserved quota_scope %q, got %q", "claude", e.QuotaScope)
			}
		}
	}
	if !foundSummary {
		t.Fatal("expected truncation summary event for benched agent")
	}
}
