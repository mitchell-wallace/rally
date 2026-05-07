package routing

import (
	"testing"
)

func parseEntriesOrDie(t *testing.T, raw []string) []ParsedEntry {
	t.Helper()
	entries, err := ParseEntries(raw)
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	return entries
}

func mustNextSelection(t *testing.T, s *Scheduler) *Selection {
	t.Helper()
	selection, err := s.Next()
	if err != nil {
		t.Fatalf("Next() returned error: %v", err)
	}
	return selection
}

func mustNext(t *testing.T, s *Scheduler) *EntryState {
	t.Helper()
	return mustNextSelection(t, s).Current
}

func TestScheduler_Scenario1_NoQuotas(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7",
		"codex:gpt-5.5",
		"opencode:opencode-go/kimi-k2.6",
	})
	s := NewScheduler(entries)

	st0 := mustNext(t, s)
	if st0.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("first pick = %q, want opus", st0.Entry.Spec)
	}
	st1 := mustNext(t, s)
	if st1.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("second pick = %q, want opus (no quota)", st1.Entry.Spec)
	}

	s.OnAgentFailed(st0, "rate limit")

	st2 := mustNext(t, s)
	if st2.Entry.Spec != "codex:gpt-5.5" {
		t.Errorf("after opus fail, pick = %q, want gpt", st2.Entry.Spec)
	}
	st3 := mustNext(t, s)
	if st3.Entry.Spec != "codex:gpt-5.5" {
		t.Errorf("gpt no-quota stay = %q, want gpt", st3.Entry.Spec)
	}

	s.OnAgentFailed(st2, "error")

	st4 := mustNext(t, s)
	if st4.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Errorf("after gpt fail, pick = %q, want kimi", st4.Entry.Spec)
	}
	st5 := mustNext(t, s)
	if st5.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Errorf("kimi no-quota stay = %q, want kimi", st5.Entry.Spec)
	}

	s.OnAgentFailed(st4, "error")

	s.OnAgentRecovered(st0)
	st6 := mustNext(t, s)
	if st6.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("after wrap, pick = %q, want opus", st6.Entry.Spec)
	}
}

func TestSchedulerNext_ExposesPrevAndCurrentOnAdvance(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"opencode:model-a:1",
		"opencode:model-b:1",
	})
	s := NewScheduler(entries)

	first := mustNextSelection(t, s)
	if first.Prev != nil {
		t.Fatalf("first selection prev = %+v, want nil", first.Prev)
	}
	if first.Current.Entry.Spec != "opencode:model-a" {
		t.Fatalf("first current = %q, want opencode:model-a", first.Current.Entry.Spec)
	}

	second := mustNextSelection(t, s)
	if second.Prev == nil {
		t.Fatal("second selection prev = nil, want previous entry")
	}
	if second.Prev.Entry.Spec != "opencode:model-a" {
		t.Fatalf("second prev = %q, want opencode:model-a", second.Prev.Entry.Spec)
	}
	if second.Current.Entry.Spec != "opencode:model-b" {
		t.Fatalf("second current = %q, want opencode:model-b", second.Current.Entry.Spec)
	}
	if second.Prev.Position == second.Current.Position {
		t.Fatalf("second selection positions = %d -> %d, want advance", second.Prev.Position, second.Current.Position)
	}
}

func TestScheduler_Scenario2_MixedQuota(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:3",
		"opencode:opencode-go/kimi-k2.6",
	})
	s := NewScheduler(entries)

	st := mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("pick 1 = %q, want opus", st.Entry.Spec)
	}

	st = mustNext(t, s)
	if st.Entry.Spec != "codex:gpt-5.5" {
		t.Errorf("pick 2 = %q, want gpt", st.Entry.Spec)
	}
	st = mustNext(t, s)
	if st.Entry.Spec != "codex:gpt-5.5" {
		t.Errorf("pick 3 = %q, want gpt", st.Entry.Spec)
	}
	st = mustNext(t, s)
	if st.Entry.Spec != "codex:gpt-5.5" {
		t.Errorf("pick 4 = %q, want gpt", st.Entry.Spec)
	}

	st = mustNext(t, s)
	if st.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Errorf("pick 5 = %q, want kimi", st.Entry.Spec)
	}
	st = mustNext(t, s)
	if st.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Errorf("pick 6 = %q, want kimi (no quota, stays)", st.Entry.Spec)
	}

	s.OnAgentFailed(st, "error")

	st = mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("after wrap pick = %q, want opus", st.Entry.Spec)
	}
}

func TestScheduler_FailureShortCircuitsQuota(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:5",
		"codex:gpt-5.5",
	})
	s := NewScheduler(entries)

	st := mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Fatalf("pick 1 = %q, want opus", st.Entry.Spec)
	}

	s.OnAgentFailed(st, "rate limit")

	st = mustNext(t, s)
	if st.Entry.Spec != "codex:gpt-5.5" {
		t.Errorf("after failure, pick = %q, want gpt", st.Entry.Spec)
	}
}

func TestScheduler_ExhaustedSkippedWithinCycle(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:1",
		"opencode:opencode-go/kimi-k2.6:1",
	})
	s := NewScheduler(entries)

	opus := mustNext(t, s)
	if opus.Entry.Spec != "claude:opus-4.7" {
		t.Fatalf("pick 1 = %q, want opus", opus.Entry.Spec)
	}
	s.OnAgentFailed(opus, "frozen")

	gpt := mustNext(t, s)
	if gpt.Entry.Spec != "codex:gpt-5.5" {
		t.Fatalf("pick 2 = %q, want gpt", gpt.Entry.Spec)
	}
	s.OnAgentFailed(gpt, "frozen")

	kimi := mustNext(t, s)
	if kimi.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Fatalf("pick 3 = %q, want kimi", kimi.Entry.Spec)
	}
	s.OnAgentFailed(kimi, "frozen")

	_, err := s.Next()
	if err == nil {
		t.Fatal("expected all-exhausted error after all entries failed within same cycle")
	}
}

func TestScheduler_CycleWrapResetsQuotaCountersButKeepsFrozenEntriesSkipped(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:1",
	})
	s := NewScheduler(entries)

	opus := mustNext(t, s)
	s.OnAgentFailed(opus, "freeze")

	gpt := mustNext(t, s)
	if gpt.Entry.Spec != "codex:gpt-5.5" {
		t.Fatalf("pick 2 = %q, want gpt", gpt.Entry.Spec)
	}

	s.OnAgentRecovered(opus)

	st := mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("after recovery and wrap, pick = %q, want opus", st.Entry.Spec)
	}
	if st.ConsecutiveRuns != 1 {
		t.Errorf("after wrap, consecutive = %d, want 1", st.ConsecutiveRuns)
	}
}

func TestScheduler_Scenario7_RangeQuotaUnderCascadingFreezes(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:3",
		"codex:gpt-5.5:3",
		"opencode:opencode-go/kimi-k2.6:1-5",
	})
	s := NewScheduler(entries)

	collect := func() *EntryState { return mustNext(t, s) }

	var opusState *EntryState
	var gptState *EntryState

	collect() // opus 1
	collect() // opus 2
	collect() // opus 3 → quota met, advance

	collect() // gpt 1
	collect() // gpt 2
	collect() // gpt 3 → quota met, advance

	collect() // kimi 1 (min reached, others available → advance)

	for _, es := range s.EntryStates() {
		if es.Entry.Spec == "claude:opus-4.7" && opusState == nil {
			opusState = es
		}
		if es.Entry.Spec == "codex:gpt-5.5" && gptState == nil {
			gptState = es
		}
	}
	s.OnAgentFailed(opusState, "rate limit freeze")

	// Cycle wrapped; opus frozen → skip, advance to gpt
	collect() // gpt 1 (cycle 2)
	collect() // gpt 2
	collect() // gpt 3 → quota met

	collect() // kimi 1 (min reached, gpt available next cycle)

	// Now freeze gpt too
	s.OnAgentFailed(gptState, "rate limit freeze")

	// Opus and gpt both frozen → kimi can extend to max 5
	var st *EntryState
	for i := 0; i < 4; i++ {
		st = mustNext(t, s)
		if st.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
			t.Fatalf("expected kimi range burst %d, got %q", i+1, st.Entry.Spec)
		}
	}

	// After 5 consecutive kimi runs (max), should be all exhausted
	_, err := s.Next()
	if err == nil {
		t.Fatal("expected all-exhausted after kimi hits max")
	}

	// Recover opus → should be selectable again
	s.OnAgentRecovered(opusState)
	st = mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("after opus recovery, pick = %q, want opus", st.Entry.Spec)
	}
}

func TestScheduler_AllExhaustedForceWait(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:1",
	})
	s := NewScheduler(entries)

	st := mustNext(t, s)
	s.OnAgentFailed(st, "error")

	st = mustNext(t, s)
	s.OnAgentFailed(st, "error")

	_, err := s.Next()
	if err == nil {
		t.Fatal("expected force-wait error when all exhausted")
	}
}

func TestScheduler_SingleEntryRoute(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7",
	})
	s := NewScheduler(entries)

	st := mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("pick = %q, want opus", st.Entry.Spec)
	}
	st = mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("pick = %q, want opus (single entry, no quota)", st.Entry.Spec)
	}
}

func TestScheduler_SingleEntryWithQuota(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:2",
	})
	s := NewScheduler(entries)

	st := mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Fatalf("pick 1 = %q", st.Entry.Spec)
	}
	st = mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Fatalf("pick 2 = %q", st.Entry.Spec)
	}

	// After quota 2 is met, single entry wraps → cycle resets → available again
	st = mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("pick 3 = %q, want opus (cycle reset)", st.Entry.Spec)
	}
	if st.ConsecutiveRuns != 1 {
		t.Errorf("after cycle reset, consecutive = %d, want 1", st.ConsecutiveRuns)
	}
}

func TestScheduler_RangeQuotaExtendsWhenOthersExhausted(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"opencode:opencode-go/kimi-k2.6:2-4",
	})
	s := NewScheduler(entries)

	// opus runs once
	st := mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Fatalf("pick 1 = %q, want opus", st.Entry.Spec)
	}
	s.OnAgentFailed(st, "frozen")

	// kimi runs min 2
	st = mustNext(t, s)
	if st.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Fatalf("pick 2 = %q, want kimi", st.Entry.Spec)
	}
	st = mustNext(t, s)
	if st.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Fatalf("pick 3 = %q, want kimi", st.Entry.Spec)
	}

	// kimi min reached, but opus is exhausted → kimi continues up to max 4
	st = mustNext(t, s)
	if st.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Fatalf("pick 4 = %q, want kimi (range burst)", st.Entry.Spec)
	}
	st = mustNext(t, s)
	if st.Entry.Spec != "opencode:opencode-go/kimi-k2.6" {
		t.Fatalf("pick 5 = %q, want kimi (range burst)", st.Entry.Spec)
	}

	// After 4 consecutive kimi (max), force-wait
	_, err := s.Next()
	if err == nil {
		t.Fatal("expected force-wait after kimi max reached")
	}
}

func TestScheduler_AllNoQuotaRoute(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7",
		"codex:gpt-5.5",
		"opencode:opencode-go/kimi-k2.6",
	})
	s := NewScheduler(entries)

	st := mustNext(t, s)
	st = mustNext(t, s)
	if st.Entry.Spec != "claude:opus-4.7" {
		t.Errorf("no quota → stays on first: %q", st.Entry.Spec)
	}

	s.OnAgentFailed(st, "error")
	st = mustNext(t, s)
	if st.Entry.Spec != "codex:gpt-5.5" {
		t.Errorf("after fail: %q", st.Entry.Spec)
	}
}

func TestScheduler_AllQuotaRoute(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:2",
		"opencode:opencode-go/kimi-k2.6:3",
	})
	s := NewScheduler(entries)

	var picks []string
	for i := 0; i < 6; i++ {
		st := mustNext(t, s)
		picks = append(picks, st.Entry.Spec)
	}
	expected := []string{
		"claude:opus-4.7",
		"codex:gpt-5.5", "codex:gpt-5.5",
		"opencode:opencode-go/kimi-k2.6", "opencode:opencode-go/kimi-k2.6", "opencode:opencode-go/kimi-k2.6",
	}
	for i, got := range picks {
		if got != expected[i] {
			t.Errorf("pick %d = %q, want %q", i+1, got, expected[i])
		}
	}
}

func TestScheduler_EmptyRoute(t *testing.T) {
	s := NewScheduler(nil)
	_, err := s.Next()
	if err == nil {
		t.Fatal("expected error for empty route")
	}
}

func TestScheduler_CycleCounterIncrements(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:1",
	})
	s := NewScheduler(entries)

	if s.Cycle() != 0 {
		t.Errorf("initial cycle = %d, want 0", s.Cycle())
	}

	mustNext(t, s) // opus
	mustNext(t, s) // gpt

	if s.Cycle() != 0 {
		t.Errorf("mid-cycle = %d, want 0", s.Cycle())
	}

	mustNext(t, s) // opus again → wrap happened

	if s.Cycle() != 1 {
		t.Errorf("after wrap cycle = %d, want 1", s.Cycle())
	}
}

func TestScheduler_OnAgentRecovered(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:1",
	})
	s := NewScheduler(entries)

	opus := mustNext(t, s)
	s.OnAgentFailed(opus, "freeze")

	if !opus.Exhausted {
		t.Fatal("opus should be exhausted after failure")
	}
	if !opus.Frozen {
		t.Fatal("opus should be frozen after detector-driven failure")
	}

	s.OnAgentRecovered(opus)
	if opus.Exhausted {
		t.Error("opus should not be exhausted after recovery")
	}
	if opus.Frozen {
		t.Error("opus should not be frozen after recovery")
	}
}

func TestScheduler_OnAgentRecovered_DoesNotClearRetryBudgetExhaustion(t *testing.T) {
	entries := parseEntriesOrDie(t, []string{
		"claude:opus-4.7:1",
		"codex:gpt-5.5:1",
	})
	s := NewScheduler(entries)

	opus := mustNext(t, s)
	s.OnAgentFailed(opus, "retry-budget-exhausted")

	if !opus.Exhausted {
		t.Fatal("opus should be exhausted after retry-budget exhaustion")
	}
	if opus.Frozen {
		t.Fatal("opus should not be marked frozen for retry-budget exhaustion")
	}

	s.OnAgentRecovered(opus)
	if !opus.Exhausted {
		t.Error("opus exhaustion should not be cleared by recovery")
	}
	if opus.Frozen {
		t.Error("opus should remain unfrozen after recovery no-op")
	}
}
