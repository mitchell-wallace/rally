package relay

import (
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/harnessapi"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestResilience_BenchAgent_WritesBenchedEvent(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "opus")
	resetAt := now.Add(5 * time.Hour)

	if err := r.BenchAgent(k, resetAt, "claude", 7); err != nil {
		t.Fatal(err)
	}

	events, err := s.GetAgentStatus(k.Harness, k.Model)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.EventType != "benched" {
		t.Fatalf("expected event type benched, got %s", e.EventType)
	}
	if e.QuotaScope != "claude" {
		t.Fatalf("expected quota_scope claude, got %q", e.QuotaScope)
	}
	if e.RelayID != 7 {
		t.Fatalf("expected relay_id 7, got %d", e.RelayID)
	}
	gotReset, perr := time.Parse(time.RFC3339, e.ResetAt)
	if perr != nil {
		t.Fatalf("reset_at not RFC3339: %v", perr)
	}
	if !gotReset.Equal(resetAt.UTC().Truncate(time.Second)) {
		t.Fatalf("expected reset_at %v, got %v", resetAt.UTC().Truncate(time.Second), gotReset)
	}
}

func TestResilience_GetState_BenchedBeforeResetDeadline(t *testing.T) {
	s := newResilienceTestStore(t)
	now := time.Now()
	r := testResilience(s, now)
	k := key("claude", "opus")

	// Reset is in the future: agent stays benched.
	if err := r.BenchAgent(k, now.Add(3*time.Hour), "claude", 1); err != nil {
		t.Fatal(err)
	}

	st, _ := r.GetState(k)
	if st != StateBenched {
		t.Fatalf("expected StateBenched before reset deadline, got %s", st)
	}
}

func TestResilience_GetState_BenchedDecaysToActiveAfterReset(t *testing.T) {
	s := newResilienceTestStore(t)
	benchedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetAt := benchedAt.Add(2 * time.Hour)
	// now is past the reset deadline.
	now := resetAt.Add(time.Minute)
	r := testResilience(s, now)
	k := key("claude", "opus")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "benched",
		Timestamp: benchedAt.UTC().Format(time.RFC3339),
		ResetAt:   resetAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	// Single re-probe: GetState surfaces the key as active once the deadline
	// passes. The on-disk log still holds only the benched event.
	st, _ := r.GetState(k)
	if st != StateActive {
		t.Fatalf("expected StateActive (re-probe) after reset deadline, got %s", st)
	}

	events, err := s.GetAgentStatus(k.Harness, k.Model)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "benched" {
		t.Fatalf("expected on-disk log untouched (1 benched event), got %+v", events)
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

	st, since := r.GetState(k)
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

	st, _ := r.GetState(k)
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

	claudeKey := key("claude", "test")
	codexKey := key("codex", "test")

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
		Cycle: []harnessapi.ResolvedAgent{
			{Harness: "claude", Model: "test"},
			{Harness: "codex", Model: "test"},
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

// TestResilience_ProbationSuccess_PromotesToActive seeds a frozen-then-decayed
// agent, then exercises the success path (UnpauseAgent) used by runOne when a
// probation run completes. After the call the state must be active so the
// agent re-enters the normal rotation.
func TestResilience_ProbationSuccess_PromotesToActive(t *testing.T) {
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

	st, _ := r.GetState(k)
	if st != StateProbation {
		t.Fatalf("setup: expected probation, got %s", st)
	}

	if err := r.UnpauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}

	st, _ = r.GetState(k)
	if st != StateActive {
		t.Fatalf("expected active after probation success, got %s", st)
	}
}

// TestResilience_ProbationIncomplete_PromotesToActive mirrors the success case
// — incomplete runs take the same UnpauseAgent path in runOne and must
// transition the agent back to active so it can keep contributing.
func TestResilience_ProbationIncomplete_PromotesToActive(t *testing.T) {
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
	k := key("opencode", "kimi")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	if err := r.UnpauseAgent(k, 1); err != nil {
		t.Fatal(err)
	}

	st, _ := r.GetState(k)
	if st != StateActive {
		t.Fatalf("expected active after probation incomplete, got %s", st)
	}
}

// TestResilience_ProbationFailure_ReFreezesWithFreshTimestamp verifies that a
// probation run that fails (agent- or infra-class) takes the FreezeAgent
// branch in runOne, writing a new frozen event whose timestamp restarts the
// freeze-decay window.
func TestResilience_ProbationFailure_ReFreezesWithFreshTimestamp(t *testing.T) {
	s := newResilienceTestStore(t)
	originalFrozenAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := originalFrozenAt.Add(6 * time.Hour)
	r := &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		FreezeDuration:            5 * time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return now },
	}
	k := key("codex", "gpt-5")

	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "frozen",
		Timestamp: originalFrozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatal(err)
	}

	st, _ := r.GetState(k)
	if st != StateProbation {
		t.Fatalf("setup: expected probation, got %s", st)
	}

	if err := r.FreezeAgent(k, 1, "test freeze"); err != nil {
		t.Fatal(err)
	}

	st, since := r.GetState(k)
	if st != StateFrozen {
		t.Fatalf("expected frozen after probation failure, got %s", st)
	}
	if !since.After(originalFrozenAt) {
		t.Fatalf("expected fresh freeze timestamp newer than original %v, got %v", originalFrozenAt, since)
	}

	events, err := s.GetAgentStatus(k.Harness, k.Model)
	if err != nil {
		t.Fatal(err)
	}
	frozenCount := 0
	for _, e := range events {
		if e.EventType == "frozen" {
			frozenCount++
		}
	}
	if frozenCount != 2 {
		t.Fatalf("expected 2 frozen events (original + re-freeze), got %d", frozenCount)
	}
}
