package runner

import (
	"errors"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

// This file is the phase-7 task 7.7 state-machine matrix: an end-to-end check
// of the benching subsystem built by the two preceding phase-7 laps
// (StateBenched + reset-deadline decay, and benchQuotaScope + the
// selectionWaitError/syncRecoverySignals/forceUnpauseAll wiring). Each test
// maps to one lettered case in the 7.7 design:
//
//	(a) a benched key is NOT selected before reset_at;
//	(b) all-benched-with-future-reset produces a wait, not an AllFrozen failure;
//	(c) a persisted reset survives a fresh relay via GetState replay and is
//	    re-probed once after it passes;
//	(d) a post-deadline re-probe that again returns usage_limit re-benches a
//	    fresh window;
//	(e) the benched event does not interfere with frozen/probation/paused
//	    state recovery.

// benchTestResilience builds a Resilience over the given store with a fixed
// clock, mirroring the freeze/pause durations used elsewhere in the suite.
func benchTestResilience(s *store.Store, now time.Time) *Resilience {
	return &Resilience{
		Store:                     s,
		PauseDuration:             time.Hour,
		FreezeDuration:            5 * time.Hour,
		HourlyRetriesBeforeFreeze: 5,
		NowFunc:                   func() time.Time { return now },
	}
}

// Case (a): a benched key is not selected before its reset_at, and once the
// deadline passes it becomes selectable again (the re-probe).
func TestBenchMatrix_A_BenchedKeyNotSelectedBeforeReset(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:1"},
	}, false)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	resilience.NowFunc = func() time.Time { return clock }

	claudeKey := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	resetAt := now.Add(3 * time.Hour)
	if err := resilience.BenchAgent(claudeKey, resetAt, "claude", 1); err != nil {
		t.Fatalf("BenchAgent: %v", err)
	}

	// Before reset_at the benched claude key must never be selected; codex is
	// the only selectable runner.
	for i := 0; i < 3; i++ {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
			t.Fatalf("pre-reset pick %d = %q, want codex:gpt-5.5 (claude benched)", i+1, got)
		}
	}
	if st, _ := resilience.GetState(claudeKey); st != StateBenched {
		t.Fatalf("claude state before reset = %s, want benched", st)
	}

	// Advance past reset_at: the bench decays to active for a re-probe and the
	// key becomes selectable again.
	clock = resetAt.Add(time.Minute)
	if st, _ := resilience.GetState(claudeKey); st != StateActive {
		t.Fatalf("claude state after reset = %s, want active (re-probe)", st)
	}

	sawClaude := false
	for i := 0; i < 3; i++ {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if agentRouteSpec(sel.Agent) == "claude:opus-4.7" {
			sawClaude = true
		}
	}
	if !sawClaude {
		t.Fatal("claude not re-selected after reset deadline passed")
	}
}

// Case (b): when every runner in a lane is benched with a future reset, the
// runtime returns a wait (the earliest reset_at) rather than an AllFrozen
// relay failure.
func TestBenchMatrix_B_AllBenchedWaitsNotAllFrozen(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "claude:sonnet-4.5:1"},
	}, false)

	now := resilience.NowFunc()
	// Two distinct deadlines; the wait must track the earliest (2h).
	if err := resilience.BenchAgent(ResilienceKey{Harness: "claude", Model: "opus-4.7"}, now.Add(4*time.Hour), "claude", 1); err != nil {
		t.Fatalf("BenchAgent(opus): %v", err)
	}
	if err := resilience.BenchAgent(ResilienceKey{Harness: "claude", Model: "sonnet-4.5"}, now.Add(2*time.Hour), "claude", 1); err != nil {
		t.Fatalf("BenchAgent(sonnet): %v", err)
	}

	_, err := rt.next(runTask{}, resilience)
	if err == nil {
		t.Fatal("next() error = nil, want wait condition for all-benched lane")
	}

	var routeErr *routeSelectionError
	if !errors.As(err, &routeErr) {
		t.Fatalf("error = %T, want *routeSelectionError", err)
	}
	if routeErr.AllFrozen {
		t.Fatalf("route error = %+v, want benched wait, not AllFrozen", routeErr)
	}
	if routeErr.Wait != 2*time.Hour {
		t.Fatalf("route error wait = %v, want 2h (earliest reset_at)", routeErr.Wait)
	}
}

// Case (c): a bench persisted to the agent-status log survives a fresh relay.
// A brand-new store/runtime opened on the same state dir replays the benched
// event via GetState (no bespoke restoration scanner), keeps the key out of
// rotation before reset_at, and re-probes it once the deadline passes.
func TestBenchMatrix_C_PersistedBenchSurvivesFreshRelay(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resetAt := now.Add(3 * time.Hour)
	claudeKey := ResilienceKey{Harness: "claude", Model: "opus-4.7"}

	// Relay #1 benches claude, then ends.
	s1 := newTestStore(t, dir)
	r1 := benchTestResilience(s1, now)
	if err := r1.BenchAgent(claudeKey, resetAt, "claude", 1); err != nil {
		t.Fatalf("BenchAgent: %v", err)
	}

	// Relay #2: a fresh store + runtime on the same dir, replaying from disk.
	s2 := newTestStore(t, dir)
	rt2, err := newResolvedRouteRuntime(map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:1"},
	}, testResolver, false, nil)
	if err != nil {
		t.Fatalf("newResolvedRouteRuntime: %v", err)
	}
	clock := now
	r2 := benchTestResilience(s2, now)
	r2.NowFunc = func() time.Time { return clock }

	// The replayed bench keeps claude out of rotation before reset_at.
	if st, _ := r2.GetState(claudeKey); st != StateBenched {
		t.Fatalf("replayed claude state = %s, want benched (survived fresh relay)", st)
	}
	for i := 0; i < 2; i++ {
		sel := mustNextRouteSelection(t, rt2, r2, "")
		if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
			t.Fatalf("fresh-relay pre-reset pick %d = %q, want codex:gpt-5.5", i+1, got)
		}
	}

	// After reset_at, the fresh relay re-probes claude exactly once: GetState
	// decays to active and the on-disk log still holds only the benched event.
	clock = resetAt.Add(time.Minute)
	if st, _ := r2.GetState(claudeKey); st != StateActive {
		t.Fatalf("replayed claude state after reset = %s, want active (re-probe)", st)
	}
	events, err := s2.GetAgentStatus(claudeKey.Harness, claudeKey.Model)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "benched" {
		t.Fatalf("on-disk log = %+v, want single untouched benched event", events)
	}

	sawClaude := false
	for i := 0; i < 3; i++ {
		sel := mustNextRouteSelection(t, rt2, r2, "")
		if agentRouteSpec(sel.Agent) == "claude:opus-4.7" {
			sawClaude = true
		}
	}
	if !sawClaude {
		t.Fatal("fresh relay did not re-probe claude after reset deadline")
	}
}

// Case (d): a post-deadline re-probe that again hits usage_limit re-benches a
// fresh window. BenchAgent appends (rather than no-op'ing), so the new deadline
// supersedes the old one and the key leaves rotation again.
func TestBenchMatrix_D_RepeatUsageLimitReBenchesFreshWindow(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:1"},
	}, false)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	resilience.NowFunc = func() time.Time { return clock }

	claudeKey := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	firstReset := now.Add(3 * time.Hour)
	if benched, err := rt.benchQuotaScope(resilience, "claude", firstReset, 1, "", ""); err != nil || benched != 1 {
		t.Fatalf("first benchQuotaScope = (%d, %v), want (1, nil)", benched, err)
	}

	// Deadline passes; the key is up for a single re-probe.
	clock = firstReset.Add(time.Minute)
	if st, _ := resilience.GetState(claudeKey); st != StateActive {
		t.Fatalf("claude state at re-probe = %s, want active", st)
	}

	// The re-probe hits usage_limit again -> bench a fresh window.
	secondReset := clock.Add(5 * time.Hour)
	if benched, err := rt.benchQuotaScope(resilience, "claude", secondReset, 2, "", ""); err != nil || benched != 1 {
		t.Fatalf("re-bench benchQuotaScope = (%d, %v), want (1, nil)", benched, err)
	}

	// The fresh window is in force: benched again, deadline updated, out of
	// rotation. The earlier benched event is retained (append, not replace).
	if st, _ := resilience.GetState(claudeKey); st != StateBenched {
		t.Fatalf("claude state after re-bench = %s, want benched", st)
	}
	gotReset, ok := rt.benchResetAt(resilience, claudeKey)
	if !ok {
		t.Fatal("benchResetAt not found after re-bench")
	}
	if !gotReset.Equal(secondReset.UTC().Truncate(time.Second)) {
		t.Fatalf("re-bench reset_at = %v, want fresh window %v", gotReset, secondReset.UTC().Truncate(time.Second))
	}

	events, err := resilience.Store.GetAgentStatus(claudeKey.Harness, claudeKey.Model)
	if err != nil {
		t.Fatal(err)
	}
	benchedCount := 0
	for _, e := range events {
		if e.EventType == "benched" {
			benchedCount++
		}
	}
	if benchedCount != 2 {
		t.Fatalf("benched event count = %d, want 2 (fresh window appended)", benchedCount)
	}

	// Still within the fresh window: claude stays out of rotation.
	for i := 0; i < 2; i++ {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
			t.Fatalf("post-rebench pick %d = %q, want codex:gpt-5.5", i+1, got)
		}
	}
}

// Case (e): the benched event lives on the same agent-status axis as
// paused/frozen/probation but must not perturb their recovery. A relay holding
// one key in each state reports every state independently, and an operator skip
// (forceUnpauseAll) clears paused and benched while leaving frozen and
// probation untouched.
func TestBenchMatrix_E_BenchedDoesNotInterfereWithOtherStates(t *testing.T) {
	rt, err := newResolvedRouteRuntime(map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:1", "antigravity:pro:1", "opencode:opencode-go/kimi-k2.6:1"},
	}, testResolver, false, nil)
	if err != nil {
		t.Fatalf("newResolvedRouteRuntime: %v", err)
	}

	s := newTestStore(t, t.TempDir())
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resilience := benchTestResilience(s, now)

	benchedKey := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	frozenKey := ResilienceKey{Harness: "codex", Model: "gpt-5.5"}
	probationKey := ResilienceKey{Harness: "antigravity", Model: "pro"}
	pausedKey := ResilienceKey{Harness: "opencode", Model: "opencode-go/kimi-k2.6"}

	if err := resilience.BenchAgent(benchedKey, now.Add(3*time.Hour), "claude", 1); err != nil {
		t.Fatalf("BenchAgent: %v", err)
	}
	if err := resilience.FreezeAgent(frozenKey, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent: %v", err)
	}
	// A frozen event aged past FreezeDuration surfaces as probation.
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: probationKey.Harness,
		Model:     probationKey.Model,
		EventType: "frozen",
		Timestamp: now.Add(-6 * time.Hour).UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatalf("AppendAgentStatus(frozen->probation): %v", err)
	}
	if err := resilience.PauseAgent(pausedKey, 1); err != nil {
		t.Fatalf("PauseAgent: %v", err)
	}

	// Each key reports its own state; the benched event does not bleed across.
	for _, tc := range []struct {
		key  ResilienceKey
		want AgentState
	}{
		{benchedKey, StateBenched},
		{frozenKey, StateFrozen},
		{probationKey, StateProbation},
		{pausedKey, StatePaused},
	} {
		if st, _ := resilience.GetState(tc.key); st != tc.want {
			t.Errorf("state(%s) = %s, want %s", tc.key, st, tc.want)
		}
	}

	// An operator skip clears the recoverable states (paused + benched) and
	// leaves the held states (frozen + probation) exactly as they were.
	cleared, err := rt.forceUnpauseAll(resilience, 1, "", "")
	if err != nil {
		t.Fatalf("forceUnpauseAll: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("cleared count = %d, want 2 (paused + benched)", cleared)
	}
	if st, _ := resilience.GetState(benchedKey); st != StateActive {
		t.Errorf("benched key after skip = %s, want active", st)
	}
	if st, _ := resilience.GetState(pausedKey); st != StateActive {
		t.Errorf("paused key after skip = %s, want active", st)
	}
	if st, _ := resilience.GetState(frozenKey); st != StateFrozen {
		t.Errorf("frozen key after skip = %s, want frozen (untouched)", st)
	}
	if st, _ := resilience.GetState(probationKey); st != StateProbation {
		t.Errorf("probation key after skip = %s, want probation (untouched)", st)
	}
}
