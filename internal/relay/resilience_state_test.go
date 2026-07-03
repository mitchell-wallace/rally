package relay

import (
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

func TestResilience_GetState_ActiveByDefault(t *testing.T) {
	s := newResilienceTestStore(t)
	r := testResilience(s, time.Now())
	st, _ := r.GetState(key("claude", "test"))
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

	st, since := r.GetState(k)
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

	st, _ := r.GetState(k)
	if st != StateFrozen {
		t.Fatalf("expected StateFrozen, got %s", st)
	}
}

func TestResetAgentStatus_ClearsAllStates(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	resilience := NewResilience(s)

	frozenKey := ResilienceKey{Harness: "claude", Model: "opus"}
	pausedKey := ResilienceKey{Harness: "opencode", Model: "opencode/big-pickle"}

	if err := resilience.FreezeAgent(frozenKey, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent: %v", err)
	}
	if err := resilience.PauseAgent(pausedKey, 1); err != nil {
		t.Fatalf("PauseAgent: %v", err)
	}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: "codex",
		Model:     "gpt-5",
		EventType: "frozen",
		Timestamp: now.Add(-6 * time.Hour).UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	st, _ := resilience.GetState(frozenKey)
	if st != StateFrozen {
		t.Fatalf("expected frozen, got %s", st)
	}
	st, _ = resilience.GetState(pausedKey)
	if st != StatePaused {
		t.Fatalf("expected paused, got %s", st)
	}
	probationRes := &Resilience{
		Store:          s,
		PauseDuration:  time.Hour,
		FreezeDuration: 5 * time.Hour,
		NowFunc:        func() time.Time { return now },
	}
	st, _ = probationRes.GetState(ResilienceKey{Harness: "codex", Model: "gpt-5"})
	if st != StateProbation {
		t.Fatalf("expected probation, got %s", st)
	}

	if err := s.ResetAgentStatus(); err != nil {
		t.Fatalf("ResetAgentStatus: %v", err)
	}

	st, _ = resilience.GetState(frozenKey)
	if st != StateActive {
		t.Fatalf("expected active after reset for frozen agent, got %s", st)
	}
	st, _ = resilience.GetState(pausedKey)
	if st != StateActive {
		t.Fatalf("expected active after reset for paused agent, got %s", st)
	}
	st, _ = resilience.GetState(ResilienceKey{Harness: "codex", Model: "gpt-5"})
	if st != StateActive {
		t.Fatalf("expected active after reset for probation agent, got %s", st)
	}

	if len(s.AllAgentStatus()) != 0 {
		t.Fatalf("expected 0 agent status events after reset, got %d", len(s.AllAgentStatus()))
	}
}

func TestPerHarnessModelCascadeIsolation(t *testing.T) {
	s := newResilienceTestStore(t)
	resilience := NewResilience(s)
	resilience.HourlyRetriesBeforeFreeze = 3

	busyKey := ResilienceKey{Harness: "opencode", Model: "busy-model"}
	idleKey := ResilienceKey{Harness: "opencode", Model: "idle-model"}

	for i := 0; i < 3; i++ {
		if err := resilience.RecordHourlyFailure(busyKey, 1); err != nil {
			t.Fatalf("RecordHourlyFailure busy: %v", err)
		}
	}

	st, _ := resilience.GetState(busyKey)
	if st != StateFrozen {
		t.Fatalf("expected busy model frozen, got %s", st)
	}

	st, _ = resilience.GetState(idleKey)
	if st != StateActive {
		t.Fatalf("expected idle model still active, got %s", st)
	}

	mix, err := ParseAgentMix([]string{"op:busy-model", "op:idle-model"}, Resolver(mixTestResolver))
	if err != nil {
		t.Fatalf("ParseAgentMix: %v", err)
	}
	picked, _, _, err := resilience.SelectActiveAgent(mix, 0)
	if err != nil {
		t.Fatalf("SelectActiveAgent: %v", err)
	}
	if picked.Model != "idle-model" {
		t.Fatalf("expected idle-model selected (busy is frozen), got %s", picked.Model)
	}
}

func TestResilience_ResilienceKey_String(t *testing.T) {
	tests := []struct {
		key    ResilienceKey
		expect string
	}{
		{ResilienceKey{Harness: "claude"}, "claude"},
		{ResilienceKey{Harness: "claude", Model: "sonnet"}, "claude:sonnet"},
		{ResilienceKey{Harness: "opencode", Model: "kimi-k2.6"}, "opencode:kimi-k2.6"},
	}
	for _, tt := range tests {
		got := tt.key.String()
		if got != tt.expect {
			t.Errorf("ResilienceKey{%q, %q}.String() = %q, want %q", tt.key.Harness, tt.key.Model, got, tt.expect)
		}
	}
}

func TestResilience_persistProbationEvent(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "sonnet")

	if err := r.persistProbationEvent(k); err != nil {
		t.Fatalf("persistProbationEvent failed: %v", err)
	}

	events, err := s.GetAgentStatus(k.Harness, k.Model)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.EventType != "probation" {
		t.Fatalf("expected event type probation, got %s", e.EventType)
	}
	if e.AgentType != "claude" {
		t.Fatalf("expected agent type claude, got %s", e.AgentType)
	}
	if e.Model != "sonnet" {
		t.Fatalf("expected model sonnet, got %s", e.Model)
	}
	if e.Reason != "freeze decayed to probation" {
		t.Fatalf("expected reason 'freeze decayed to probation', got %q", e.Reason)
	}
}
