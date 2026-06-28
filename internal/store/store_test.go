package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/textutil"
)

func setupTempStore(t *testing.T) (string, *Store) {
	t.Helper()
	dir := t.TempDir()
	rallyDir := RallyDir(dir)
	if err := os.MkdirAll(rallyDir, 0755); err != nil {
		t.Fatal(err)
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

func TestTryRecordPersistsOutcomeAndHandoffOnly(t *testing.T) {
	rallyDir, s := setupTempStore(t)

	if err := s.AppendTry(TryRecord{
		ID:                     1,
		RunID:                  1,
		AgentType:              "codex",
		Completed:              true,
		Outcome:                reliability.OutcomeHandoffRequested,
		HandoffOnly:            true,
		ResolvedRoute:          "senior",
		DirtyHandoff:           true,
		RecoveryClassification: "repair_plan",
		HandoffCreatedLapIDs:   []string{"lap-followup"},
		Category:               "",
	}); err != nil {
		t.Fatalf("AppendTry: %v", err)
	}

	read, err := readJSONL[TryRecord](filepath.Join(rallyDir, "state", "tries.jsonl"))
	if err != nil {
		t.Fatalf("read tries: %v", err)
	}
	if len(read) != 1 {
		t.Fatalf("tries = %d, want 1", len(read))
	}
	if read[0].Outcome != reliability.OutcomeHandoffRequested {
		t.Fatalf("Outcome = %q, want %q", read[0].Outcome, reliability.OutcomeHandoffRequested)
	}
	if !read[0].HandoffOnly {
		t.Fatal("HandoffOnly = false, want true")
	}
	if read[0].ResolvedRoute != "senior" {
		t.Fatalf("ResolvedRoute = %q, want senior", read[0].ResolvedRoute)
	}
	if !read[0].DirtyHandoff {
		t.Fatal("DirtyHandoff = false, want true")
	}
	if read[0].RecoveryClassification != "repair_plan" {
		t.Fatalf("RecoveryClassification = %q, want repair_plan", read[0].RecoveryClassification)
	}
	if len(read[0].HandoffCreatedLapIDs) != 1 || read[0].HandoffCreatedLapIDs[0] != "lap-followup" {
		t.Fatalf("HandoffCreatedLapIDs = %v, want [lap-followup]", read[0].HandoffCreatedLapIDs)
	}
	if read[0].Category != "" {
		t.Fatalf("Category = %q, want empty for non-failed outcome", read[0].Category)
	}
}

func TestTryRecordCancelledOutcomeAndSource(t *testing.T) {
	sources := []string{"skip", "graceful_stop", "quit_now"}
	for _, src := range sources {
		t.Run(src, func(t *testing.T) {
			rallyDir, s := setupTempStore(t)

			if err := s.AppendTry(TryRecord{
				ID:                 1,
				RunID:              1,
				AgentType:          "claude",
				Completed:          false,
				Outcome:            reliability.OutcomeCancelled,
				CancellationSource: src,
				Summary:            "operator cancelled",
			}); err != nil {
				t.Fatalf("AppendTry: %v", err)
			}

			read, err := readJSONL[TryRecord](filepath.Join(rallyDir, "state", "tries.jsonl"))
			if err != nil {
				t.Fatalf("read tries: %v", err)
			}
			if len(read) != 1 {
				t.Fatalf("tries = %d, want 1", len(read))
			}
			if read[0].Outcome != reliability.OutcomeCancelled {
				t.Fatalf("Outcome = %q, want %q", read[0].Outcome, reliability.OutcomeCancelled)
			}
			if read[0].CancellationSource != src {
				t.Fatalf("CancellationSource = %q, want %q", read[0].CancellationSource, src)
			}
			if read[0].Completed {
				t.Fatal("Completed = true, want false for cancelled outcome")
			}
			if read[0].Category != "" {
				t.Fatalf("Category = %q, want empty for cancelled outcome", read[0].Category)
			}
		})
	}
}

func TestRecoveryPendingForLapUsesResolvingTryOfMostRecentRun(t *testing.T) {
	_, s := setupTempStore(t)

	mustAppendTry(t, s, TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeRunTimeout})
	mustAppendTry(t, s, TryRecord{ID: 2, RunID: 1, LapID: "lap-1", AttemptNumber: 2, HandoffOnly: true, Outcome: reliability.OutcomeHandoffTimeout})

	status := s.RecoveryPendingForLap("lap-1")
	if !status.Pending {
		t.Fatalf("Pending = false, want true: %+v", status)
	}
	if status.ResolvingTryID != 2 || status.ResolvingRunID != 1 {
		t.Fatalf("resolver = run %d try %d, want run 1 try 2", status.ResolvingRunID, status.ResolvingTryID)
	}
	if status.ResolvingOutcome != reliability.OutcomeHandoffTimeout {
		t.Fatalf("ResolvingOutcome = %q, want handoff_timeout", status.ResolvingOutcome)
	}
}

func TestRecoveryPendingForLapTriggers(t *testing.T) {
	tests := []struct {
		name string
		rec  TryRecord
		want bool
	}{
		{
			name: "dirty handoff",
			rec:  TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffRequested, DirtyHandoff: true},
			want: true,
		},
		{
			name: "handoff timeout",
			rec:  TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout},
			want: true,
		},
		{
			name: "ordinary failed",
			rec:  TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeFailed},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, s := setupTempStore(t)
			mustAppendTry(t, s, tt.rec)
			if got := s.RecoveryPendingForLap("lap-1").Pending; got != tt.want {
				t.Fatalf("Pending = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecoveryPendingForLapMatchesDirtyHandoffFollowupAfterReload(t *testing.T) {
	rallyDir, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})

	reloaded, err := NewStore(rallyDir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}

	status := reloaded.RecoveryPendingForLap("followup")
	if !status.Pending {
		t.Fatalf("Pending = false, want true: %+v", status)
	}
	if status.TriggerLapID != "original" {
		t.Fatalf("TriggerLapID = %q, want original", status.TriggerLapID)
	}
	if !status.HandoffContinuationMatch {
		t.Fatal("HandoffContinuationMatch = false, want true")
	}
}

func TestRecoveryPendingForLapFollowupClearsAfterNewerCleanRun(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})
	mustAppendTry(t, s, TryRecord{
		ID:            2,
		RunID:         2,
		LapID:         "followup",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeCompleted,
		ResolvedRoute: "recovery",
	})

	status := s.RecoveryPendingForLap("followup")
	if status.Pending || status.CapHit {
		t.Fatalf("status = %+v, want recovery cleared by newer followup run", status)
	}
}

func TestRecoveryPendingForLapFollowupClearsAfterNewerOriginalRun(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})
	mustAppendTry(t, s, TryRecord{
		ID:            2,
		RunID:         2,
		LapID:         "original",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeCompleted,
		ResolvedRoute: "recovery",
	})

	status := s.RecoveryPendingForLap("followup")
	if status.Pending || status.CapHit {
		t.Fatalf("status = %+v, want recovery cleared by newer original-lap recovery run", status)
	}
}

func TestRecoveryPendingForLapCapCountsOriginalAndFollowupGroup(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})
	mustAppendTry(t, s, TryRecord{
		ID:            2,
		RunID:         2,
		LapID:         "original",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeHandoffTimeout,
		ResolvedRoute: "recovery",
	})
	mustAppendTry(t, s, TryRecord{
		ID:            3,
		RunID:         3,
		LapID:         "followup",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeHandoffTimeout,
		ResolvedRoute: "recovery",
	})

	status := s.RecoveryPendingForLap("original")
	if !status.CapHit {
		t.Fatalf("CapHit = false, want grouped recovery cap hit: %+v", status)
	}
	if status.ConsecutiveRecoveryRuns != RecoveryRouteConsecutiveCap {
		t.Fatalf("ConsecutiveRecoveryRuns = %d, want %d", status.ConsecutiveRecoveryRuns, RecoveryRouteConsecutiveCap)
	}
}

func TestRecoveryPendingForLapConsecutiveRecoveryCap(t *testing.T) {
	_, s := setupTempStore(t)
	mustAppendTry(t, s, TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"})
	mustAppendTry(t, s, TryRecord{ID: 2, RunID: 2, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "RECOVERY"})

	status := s.RecoveryPendingForLap("lap-1")
	if !status.CapHit {
		t.Fatalf("CapHit = false, want true: %+v", status)
	}
	if status.Pending {
		t.Fatal("Pending = true, want false on cap hit")
	}
	if status.ConsecutiveRecoveryRuns != RecoveryRouteConsecutiveCap {
		t.Fatalf("ConsecutiveRecoveryRuns = %d, want %d", status.ConsecutiveRecoveryRuns, RecoveryRouteConsecutiveCap)
	}
}

func TestRecoveryPendingForLapSelectsMostRecentRunAcrossRelays(t *testing.T) {
	_, s := setupTempStore(t)
	// Relay 1 reached run 2 and completed the lap cleanly before exiting.
	mustAppendTry(t, s, TryRecord{ID: 1, RelayID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeRunTimeout})
	mustAppendTry(t, s, TryRecord{ID: 2, RelayID: 1, RunID: 2, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeCompleted})
	// A later relay restarts run numbering at 1 (RunID collides with relay 1's
	// first run) and hands off. The newest run owns the routing decision.
	mustAppendTry(t, s, TryRecord{ID: 3, RelayID: 2, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout})

	status := s.RecoveryPendingForLap("lap-1")
	if !status.Pending {
		t.Fatalf("Pending = false, want true: newest run (relay 2 run 1) handed off: %+v", status)
	}
	if status.ResolvingTryID != 3 || status.ResolvingRunID != 1 {
		t.Fatalf("resolver = run %d try %d, want run 1 try 3 (relay 2)", status.ResolvingRunID, status.ResolvingTryID)
	}
}

func TestRecoveryPendingForLapCapCountsRunsAcrossRelayRestart(t *testing.T) {
	rallyDir, s := setupTempStore(t)
	// Relay 1, run 1: recovery-routed handoff.
	mustAppendTry(t, s, TryRecord{ID: 1, RelayID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"})
	// Relay restarts; run numbering resets so this run shares RunID 1 with the
	// relay-1 run above. It must count as a distinct consecutive recovery run.
	mustAppendTry(t, s, TryRecord{ID: 2, RelayID: 2, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"})

	reloaded, err := NewStore(rallyDir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}

	status := reloaded.RecoveryPendingForLap("lap-1")
	if !status.CapHit {
		t.Fatalf("CapHit = false, want true: two recovery runs across a relay restart should hit the cap: %+v", status)
	}
	if status.Pending {
		t.Fatal("Pending = true, want false on cap hit")
	}
	if status.ConsecutiveRecoveryRuns != RecoveryRouteConsecutiveCap {
		t.Fatalf("ConsecutiveRecoveryRuns = %d, want %d (runs sharing RunID across relays must count separately)", status.ConsecutiveRecoveryRuns, RecoveryRouteConsecutiveCap)
	}
}

func mustAppendTry(t *testing.T, s *Store, rec TryRecord) {
	t.Helper()
	if err := s.AppendTry(rec); err != nil {
		t.Fatalf("AppendTry(%+v): %v", rec, err)
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

func TestAppendTryCapsFinalSnippetFields(t *testing.T) {
	rallyDir, store := setupTempStore(t)

	longSummary := strings.Repeat("界", FinalSnippetRuneLimit) + "middle" + strings.Repeat("終", FinalSnippetRuneLimit)
	longRemainingWork := strings.Repeat("前", FinalSnippetRuneLimit) + "middle" + strings.Repeat("後", FinalSnippetRuneLimit)
	smallSummary := "short summary\nkept verbatim"
	smallRemainingWork := "small remaining work"

	if err := store.AppendTry(TryRecord{
		ID:            1,
		Summary:       longSummary,
		RemainingWork: longRemainingWork,
	}); err != nil {
		t.Fatalf("AppendTry oversized record: %v", err)
	}
	if err := store.AppendTry(TryRecord{
		ID:            2,
		Summary:       smallSummary,
		RemainingWork: smallRemainingWork,
	}); err != nil {
		t.Fatalf("AppendTry small record: %v", err)
	}

	stored, err := readJSONL[TryRecord](filepath.Join(rallyDir, "state", "tries.jsonl"))
	if err != nil {
		t.Fatalf("read tries.jsonl: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored try count = %d, want 2", len(stored))
	}

	assertCappedFinalSnippet(t, stored[0].Summary, "界", "終")
	assertCappedFinalSnippet(t, stored[0].RemainingWork, "前", "後")
	if stored[1].Summary != smallSummary {
		t.Fatalf("small summary = %q, want verbatim %q", stored[1].Summary, smallSummary)
	}
	if stored[1].RemainingWork != smallRemainingWork {
		t.Fatalf("small remaining work = %q, want verbatim %q", stored[1].RemainingWork, smallRemainingWork)
	}

	cached := store.AllTries()
	if cached[0].Summary != stored[0].Summary || cached[0].RemainingWork != stored[0].RemainingWork {
		t.Fatal("cached try fields do not match the capped persisted values")
	}
}

func assertCappedFinalSnippet(t *testing.T, got, wantHead, wantTail string) {
	t.Helper()

	if !utf8.ValidString(got) {
		t.Fatalf("capped text is not valid UTF-8: %q", got)
	}
	if gotRunes := len([]rune(got)); gotRunes != FinalSnippetRuneLimit {
		t.Fatalf("capped text rune length = %d, want %d", gotRunes, FinalSnippetRuneLimit)
	}
	if !strings.Contains(got, textutil.HeadTailTruncationMarker) {
		t.Fatalf("capped text is missing marker %q", textutil.HeadTailTruncationMarker)
	}
	if !strings.HasPrefix(got, wantHead) {
		t.Fatalf("capped text does not preserve head %q", wantHead)
	}
	if !strings.HasSuffix(got, wantTail) {
		t.Fatalf("capped text does not preserve tail %q", wantTail)
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

func TestTryCommitHistory(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Single commit -> populates CommitHistory with one element
	try1 := TryRecord{ID: 1, CommitHash: "singlehash"}
	if err := s.AppendTry(try1); err != nil {
		t.Fatal(err)
	}

	// 2. Multiple commits -> backward compat sets CommitHash to last element
	try2 := TryRecord{ID: 2, CommitHistory: []string{"hash1", "hash2", "hash3"}}
	if err := s.AppendTry(try2); err != nil {
		t.Fatal(err)
	}

	// Verify
	tries := s.RecentTries(10)
	if len(tries) != 2 {
		t.Fatalf("expected 2 tries, got %d", len(tries))
	}

	// try1 (returned first because RecentTries does not reverse)
	t1 := tries[0]
	if t1.CommitHash != "singlehash" {
		t.Errorf("try1 CommitHash = %q, want singlehash", t1.CommitHash)
	}
	if len(t1.CommitHistory) != 1 || t1.CommitHistory[0] != "singlehash" {
		t.Errorf("try1 CommitHistory = %v, want [singlehash]", t1.CommitHistory)
	}

	// try2 (returned second)
	t2 := tries[1]
	if t2.CommitHash != "hash3" {
		t.Errorf("try2 CommitHash = %q, want hash3", t2.CommitHash)
	}
	if len(t2.CommitHistory) != 3 || t2.CommitHistory[0] != "hash1" {
		t.Errorf("try2 CommitHistory = %v", t2.CommitHistory)
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
