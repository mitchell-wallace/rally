package relay

import (
	"os"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/agent"
	"github.com/mitchell-wallace/rally/internal/store"
)

func newResilienceTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testResilience(s *store.Store, now time.Time) *Resilience {
	return &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return now },
	}
}

func key(harness, model string) ResilienceKey {
	return ResilienceKey{Harness: harness, Model: model}
}

func TestResilience_GetState_ActiveByDefault(t *testing.T) {
	s := newResilienceTestStore(t)
	r := testResilience(s, time.Now())
	st, _ := r.getState(key("claude", ""))
	if st != StateActive {
		t.Fatalf("expected StateActive for agent with no events, got %s", st)
	}
}

func TestResilience_GetState_PausedAfterPauseEvent(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "sonnet")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "paused",
		Timestamp: now.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	st, since := r.getState(k)
	if st != StatePaused {
		t.Fatalf("expected StatePaused, got %s", st)
	}
	if since.IsZero() {
		t.Fatal("expected non-zero since time")
	}
}

func TestResilience_GetState_FrozenAfterFreezeEvent(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "sonnet")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "frozen",
		Timestamp: now.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	st, _ := r.getState(k)
	if st != StateFrozen {
		t.Fatalf("expected StateFrozen, got %s", st)
	}
}

func TestResilience_PauseAgent_WritesPausedEvent(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("opencode", "gemini-2.5-pro")

	if err := r.PauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}

	st, _ := r.getState(k)
	if st != StatePaused {
		t.Fatalf("expected StatePaused after PauseAgent, got %s", st)
	}

	events := s.GetAgentStatus(k.Harness, k.Model)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "paused" {
		t.Fatalf("expected event type paused, got %s", events[0].EventType)
	}
}

func TestResilience_PauseAgent_NoOpWhenAlreadyPaused(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "")

	if err := r.PauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.PauseAgent(k, 2); err != nil {
		t.Fatal(err)
	}

	events := s.GetAgentStatus(k.Harness, k.Model)
	if len(events) != 1 {
		t.Fatalf("expected 1 event (no-op on second pause), got %d", len(events))
	}
}

func TestResilience_UnpauseAgent_RestoresActive(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "opus")

	if err := r.PauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}
	st, _ := r.getState(k)
	if st != StatePaused {
		t.Fatalf("expected paused after PauseAgent, got %s", st)
	}

	if err := r.UnpauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}
	st, _ = r.getState(k)
	if st != StateActive {
		t.Fatalf("expected StateActive after UnpauseAgent, got %s", st)
	}
}

func TestResilience_UnpauseAgent_NoOpWhenActive(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "")

	if err := r.UnpauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}

	events := s.GetAgentStatus(k.Harness, k.Model)
	if len(events) != 0 {
		t.Fatalf("expected 0 events (no-op when already active), got %d", len(events))
	}
}

func TestResilience_FreezeAgent_WritesFrozenEvent(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("codex", "")

	if err := r.FreezeAgent(k, 1); err != nil {
		t.Fatal(err)
	}

	st, _ := r.getState(k)
	if st != StateFrozen {
		t.Fatalf("expected StateFrozen after FreezeAgent, got %s", st)
	}

	events := s.GetAgentStatus(k.Harness, k.Model)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "frozen" {
		t.Fatalf("expected event type frozen, got %s", events[0].EventType)
	}
}

func TestResilience_FreezeAgent_NoOpWhenAlreadyFrozen(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("codex", "")

	if err := r.FreezeAgent(k, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.FreezeAgent(k, 2); err != nil {
		t.Fatal(err)
	}

	events := s.GetAgentStatus(k.Harness, k.Model)
	if len(events) != 1 {
		t.Fatalf("expected 1 event (no-op on second freeze), got %d", len(events))
	}
}

func TestResilience_StateTransition_PausedToFrozen(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "sonnet")

	if err := r.PauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}
	st, _ := r.getState(k)
	if st != StatePaused {
		t.Fatalf("expected paused, got %s", st)
	}

	if err := r.FreezeAgent(k, 1); err != nil {
		t.Fatal(err)
	}
	st, _ = r.getState(k)
	if st != StateFrozen {
		t.Fatalf("expected frozen after freeze of paused agent, got %s", st)
	}
}

func TestResilience_StateTransition_FrozenStaysFrozen(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "")

	if err := r.FreezeAgent(k, 1); err != nil {
		t.Fatal(err)
	}

	if err := r.UnpauseAgent(k, 2); err != nil {
		t.Fatal(err)
	}

	st, _ := r.getState(k)
	if st != StateActive {
		t.Fatalf("expected StateActive after unfrozen event, got %s", st)
	}
}

func TestResilience_RecordHourlyFailure_CountsAndAutoFreezes(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	r.HourlyRetriesBeforeFreeze = 3
	k := key("claude", "sonnet")

	for i := 0; i < 2; i++ {
		if err := r.RecordHourlyFailure(k, 1); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := r.getState(k)
	if st != StatePaused {
		t.Fatalf("expected paused after 2 retry_failed events, got %s", st)
	}

	if err := r.RecordHourlyFailure(k, 1); err != nil {
		t.Fatal(err)
	}
	st, _ = r.getState(k)
	if st != StateFrozen {
		t.Fatalf("expected frozen after 3rd retry_failed (>= threshold), got %s", st)
	}
}

func TestResilience_RecordHourlyFailure_CountBreaksAtActiveBoundary(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	r.HourlyRetriesBeforeFreeze = 3
	k := key("claude", "opus")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "retry_failed",
		Timestamp: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "active",
		Timestamp: now.Add(-time.Hour).UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := r.RecordHourlyFailure(k, 1); err != nil {
			t.Fatal(err)
		}
	}

	st, _ := r.getState(k)
	if st != StatePaused {
		t.Fatalf("expected paused (not frozen — old failures before 'active' should not count), got %s", st)
	}
}

func TestResilience_RecordHourlyFailure_CountBreaksAtFrozenBoundary(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	r.HourlyRetriesBeforeFreeze = 3
	k := key("codex", "")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "retry_failed",
		Timestamp: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "frozen",
		Timestamp: now.Add(-time.Hour).UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	if err := r.RecordHourlyFailure(k, 1); err != nil {
		t.Fatal(err)
	}

	st, _ := r.getState(k)
	if st != StatePaused {
		t.Fatalf("expected paused (old retry_failed before frozen should not count), got %s", st)
	}
}

func TestResilience_SelectActiveAgent_SkipsPausedAndFrozen(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)

	claudeKey := key("claude", "")
	codexKey := key("codex", "")

	if err := r.PauseAgent(claudeKey, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.FreezeAgent(codexKey, 1); err != nil {
		t.Fatal(err)
	}

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude"},
			{Harness: "codex"},
			{Harness: "gemini"},
		},
	}

	selected, newIdx, retrying, err := r.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Harness != "gemini" {
		t.Fatalf("expected gemini (only active agent), got %s", selected.Harness)
	}
	if newIdx != 3 {
		t.Fatalf("expected runIndex 3, got %d", newIdx)
	}
	if retrying {
		t.Fatal("expected retrying=false for active agent")
	}
}

func TestResilience_SelectActiveAgent_CyclesThroughActive(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude"},
			{Harness: "codex"},
		},
	}

	selected, newIdx, _, err := r.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Harness != "claude" {
		t.Fatalf("expected claude at index 0, got %s", selected.Harness)
	}
	if newIdx != 1 {
		t.Fatalf("expected runIndex 1, got %d", newIdx)
	}

	selected, newIdx, _, err = r.SelectActiveAgent(mix, newIdx)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Harness != "codex" {
		t.Fatalf("expected codex at index 1, got %s", selected.Harness)
	}
	if newIdx != 2 {
		t.Fatalf("expected runIndex 2, got %d", newIdx)
	}

	selected, _, _, err = r.SelectActiveAgent(mix, newIdx)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Harness != "claude" {
		t.Fatalf("expected claude wrapping around, got %s", selected.Harness)
	}
}

func TestResilience_SelectActiveAgent_AllFrozenError(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)

	claudeKey := key("claude", "")
	codexKey := key("codex", "")

	if err := r.FreezeAgent(claudeKey, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.FreezeAgent(codexKey, 1); err != nil {
		t.Fatal(err)
	}

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude"},
			{Harness: "codex"},
		},
	}

	_, _, _, err := r.SelectActiveAgent(mix, 0)
	if err == nil {
		t.Fatal("expected error when all agents frozen")
	}
}

func TestResilience_SelectActiveAgent_PausedButExpired_ReturnsAsRetry(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	r.PauseDuration = time.Hour

	pausedAt := now.Add(-2 * time.Hour)
	k := key("claude", "sonnet")
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "paused",
		Timestamp: pausedAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude", Model: "sonnet"},
		},
	}

	selected, _, retrying, err := r.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Harness != "claude" {
		t.Fatalf("expected claude, got %s", selected.Harness)
	}
	if !retrying {
		t.Fatal("expected retrying=true for paused agent with expired duration")
	}
}

func TestResilience_SelectActiveAgent_PausedNotExpired_SkipsAgent(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	r.PauseDuration = time.Hour

	pausedAt := now.Add(-10 * time.Minute)
	k := key("claude", "sonnet")
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "paused",
		Timestamp: pausedAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude", Model: "sonnet"},
			{Harness: "codex"},
		},
	}

	selected, _, retrying, err := r.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Harness != "codex" {
		t.Fatalf("expected codex (claude still in pause window), got %s", selected.Harness)
	}
	if retrying {
		t.Fatal("expected retrying=false for active agent")
	}
}

func TestResilience_SelectActiveAgent_EmptyCycle(t *testing.T) {
	s := newResilienceTestStore(t)
	r := testResilience(s, time.Now())

	mix := AgentMix{Cycle: nil}
	selected, newIdx, _, err := r.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Harness != "claude" {
		t.Fatalf("expected default claude for empty cycle, got %s", selected.Harness)
	}
	if newIdx != 1 {
		t.Fatalf("expected runIndex 1, got %d", newIdx)
	}
}

func TestResilience_ResilienceKey_String(t *testing.T) {
	tests := []struct {
		key    ResilienceKey
		expect string
	}{
		{ResilienceKey{Harness: "claude"}, "claude"},
		{ResilienceKey{Harness: "claude", Model: "sonnet"}, "claude:sonnet"},
		{ResilienceKey{Harness: "opencode", Model: "gemini-2.5-pro"}, "opencode:gemini-2.5-pro"},
	}
	for _, tt := range tests {
		got := tt.key.String()
		if got != tt.expect {
			t.Errorf("ResilienceKey{%q, %q}.String() = %q, want %q", tt.key.Harness, tt.key.Model, got, tt.expect)
		}
	}
}

func TestResilience_GetState_FrozenDecaysToProbation(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	frozenAt := now.Add(-6 * time.Hour)
	r := &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		FreezeDuration:            5 * time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return now },
	}
	k := key("claude", "sonnet")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	st, since := r.getState(k)
	if st != StateProbation {
		t.Fatalf("expected StateProbation after freeze decay, got %s", st)
	}
	if since.IsZero() {
		t.Fatal("expected non-zero since time")
	}
}

func TestResilience_GetState_FrozenNotDecayed(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	frozenAt := now.Add(-2 * time.Hour)
	r := &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		FreezeDuration:            5 * time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return now },
	}
	k := key("claude", "sonnet")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	st, _ := r.getState(k)
	if st != StateFrozen {
		t.Fatalf("expected StateFrozen (not yet decayed), got %s", st)
	}
}

func TestResilience_SelectActiveAgent_AllFrozenButDecayable(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	frozenAt := now.Add(-6 * time.Hour)
	r := &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		FreezeDuration:            5 * time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return now },
	}

	claudeKey := key("claude", "")
	codexKey := key("codex", "")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: claudeKey.Harness,
		Model:     claudeKey.Model,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: codexKey.Harness,
		Model:     codexKey.Model,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	mix := AgentMix{
		Cycle: []agent.ResolvedAgent{
			{Harness: "claude"},
			{Harness: "codex"},
		},
	}

	selected, _, _, err := r.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("expected probation agent to be selectable, got error: %v", err)
	}
	if selected.Harness != "claude" {
		t.Fatalf("expected claude (first in cycle) to be selected for probation, got %s", selected.Harness)
	}
}
