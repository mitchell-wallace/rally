package runner

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
)

func TestRouteRuntime_ForceUnpauseAll(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:1", "opencode:opencode-go/kimi-k2.6:1"},
	}, false)

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "opus-4.7"}, 1); err != nil {
		t.Fatalf("PauseAgent(claude): %v", err)
	}
	if err := resilience.PauseAgent(ResilienceKey{Harness: "codex", Model: "gpt-5.5"}, 1); err != nil {
		t.Fatalf("PauseAgent(codex): %v", err)
	}

	unpaused, err := rt.forceUnpauseAll(resilience, 1, "", "")
	if err != nil {
		t.Fatalf("forceUnpauseAll: %v", err)
	}
	if unpaused != 2 {
		t.Errorf("unpaused count = %d, want 2", unpaused)
	}

	for _, spec := range []string{"claude:opus-4.7", "codex:gpt-5.5", "opencode:opencode-go/kimi-k2.6"} {
		parts := strings.SplitN(spec, ":", 2)
		st, _ := resilience.GetState(ResilienceKey{Harness: parts[0], Model: parts[1]})
		if st != StateActive {
			t.Errorf("state(%s) = %s, want active", spec, st)
		}
	}

	// Idempotent: a second call finds nothing to unpause.
	again, err := rt.forceUnpauseAll(resilience, 1, "", "")
	if err != nil {
		t.Fatalf("second forceUnpauseAll: %v", err)
	}
	if again != 0 {
		t.Errorf("second unpaused count = %d, want 0", again)
	}
}

func TestHasProbationEventForCurrentFreeze(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
	}, false)
	key := ResilienceKey{Harness: "claude", Model: "opus-4.7"}

	t.Run("frozen then probation returns true", func(t *testing.T) {
		s := newTestStore(t, t.TempDir())
		resilience.Store = s
		appendEvent(t, s, key, "frozen", 1)
		appendEvent(t, s, key, "probation", 1)
		if !rt.hasProbationEventForCurrentFreeze(resilience, key) {
			t.Fatal("expected true for frozen → probation")
		}
	})

	t.Run("frozen only returns false", func(t *testing.T) {
		s := newTestStore(t, t.TempDir())
		resilience.Store = s
		appendEvent(t, s, key, "frozen", 1)
		if rt.hasProbationEventForCurrentFreeze(resilience, key) {
			t.Fatal("expected false for frozen only")
		}
	})

	t.Run("no events returns false", func(t *testing.T) {
		s := newTestStore(t, t.TempDir())
		resilience.Store = s
		if rt.hasProbationEventForCurrentFreeze(resilience, key) {
			t.Fatal("expected false for no events")
		}
	})

	t.Run("frozen active probation returns true", func(t *testing.T) {
		s := newTestStore(t, t.TempDir())
		resilience.Store = s
		appendEvent(t, s, key, "frozen", 1)
		appendEvent(t, s, key, "active", 1)
		appendEvent(t, s, key, "probation", 1)
		if !rt.hasProbationEventForCurrentFreeze(resilience, key) {
			t.Fatal("expected true: probation found before frozen scanning backwards")
		}
	})

	t.Run("probation frozen probation returns true", func(t *testing.T) {
		s := newTestStore(t, t.TempDir())
		resilience.Store = s
		appendEvent(t, s, key, "probation", 1)
		appendEvent(t, s, key, "frozen", 1)
		appendEvent(t, s, key, "probation", 1)
		if !rt.hasProbationEventForCurrentFreeze(resilience, key) {
			t.Fatal("expected true: latest probation found first scanning backwards")
		}
	})
}

func TestRouteRuntime_ProbationOneShotSyncRecoverySignals(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	rt, err := newResolvedRouteRuntime(map[string][]string{
		"default": {"claude:opus-4.7"},
	}, testResolver, false, nil)
	if err != nil {
		t.Fatalf("newResolvedRouteRuntime: %v", err)
	}

	s := newTestStore(t, t.TempDir())
	resilience := &Resilience{
		Store:          s,
		PauseDuration:  time.Hour,
		FreezeDuration: 5 * time.Hour,
		NowFunc:        func() time.Time { return now },
	}

	frozenAt := now.Add(-6 * time.Hour)
	key := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatalf("AppendAgentStatus: %v", err)
	}

	scheduler := rt.schedulers["default"]
	if scheduler == nil {
		t.Fatal("no scheduler for default route")
	}

	entryStates := scheduler.EntryStates()
	if len(entryStates) != 1 {
		t.Fatalf("expected 1 entry state, got %d", len(entryStates))
	}
	entry := entryStates[0]

	// First sync: entry should be unbenched by ResetEntry, probation event persisted.
	rt.syncRecoverySignals(scheduler, resilience, "")
	if entry.Benched {
		t.Fatal("expected entry NOT benched after first sync (ResetEntry)")
	}
	if entry.Exhausted {
		t.Fatal("expected entry NOT exhausted after first sync (ResetEntry)")
	}
	events, err := s.GetAgentStatus(key.Harness, key.Model)
	if err != nil {
		t.Fatal(err)
	}
	probationFound := false
	for _, e := range events {
		if e.EventType == "probation" {
			probationFound = true
			break
		}
	}
	if !probationFound {
		t.Fatal("expected probation event after first sync")
	}

	// Second sync: entry should be Benched (re-benched because probation exists).
	rt.syncRecoverySignals(scheduler, resilience, "")
	if !entry.Benched {
		t.Fatal("expected entry benched after second sync")
	}
	if !entry.Exhausted {
		t.Fatal("expected entry exhausted after second sync")
	}

	// Third sync: no-op because already Benched+Exhausted.
	// Unset Benched to verify the guard re-benches when only Exhausted is set.
	entry.Benched = false
	entry.Exhausted = true
	rt.syncRecoverySignals(scheduler, resilience, "")
	if !entry.Benched || !entry.Exhausted {
		t.Fatal("expected entry re-benched+exhausted when only Benched was clear")
	}
	// Fourth sync: true no-op because already Benched+Exhausted.
	entryBefore := *entry
	rt.syncRecoverySignals(scheduler, resilience, "")
	if entry.Benched != entryBefore.Benched || entry.Exhausted != entryBefore.Exhausted {
		t.Fatal("expected no-op when already Benched+Exhausted")
	}
}

func TestRouteRuntime_ForceUnpauseAllMixedStates(t *testing.T) {
	rt, err := newResolvedRouteRuntime(map[string][]string{
		"routeA": {"claude:opus-4.7"},
		"routeB": {"codex:gpt-5.5"},
	}, testResolver, false, nil)
	if err != nil {
		t.Fatalf("newResolvedRouteRuntime: %v", err)
	}

	s := newTestStore(t, t.TempDir())
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resilience := &Resilience{
		Store:          s,
		PauseDuration:  time.Hour,
		FreezeDuration: 5 * time.Hour,
		NowFunc:        func() time.Time { return now },
	}

	pausedKey := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	frozenKey := ResilienceKey{Harness: "codex", Model: "gpt-5.5"}

	if err := resilience.PauseAgent(pausedKey, 1); err != nil {
		t.Fatalf("PauseAgent: %v", err)
	}
	if err := resilience.FreezeAgent(frozenKey, 1, "test freeze"); err != nil {
		t.Fatalf("FreezeAgent: %v", err)
	}

	// Verify initial states.
	st, _ := resilience.GetState(pausedKey)
	if st != StatePaused {
		t.Fatalf("expected paused, got %s", st)
	}
	st, _ = resilience.GetState(frozenKey)
	if st != StateFrozen {
		t.Fatalf("expected frozen, got %s", st)
	}

	unpaused, err := rt.forceUnpauseAll(resilience, 1, "", "")
	if err != nil {
		t.Fatalf("forceUnpauseAll: %v", err)
	}
	if unpaused != 1 {
		t.Errorf("unpaused count = %d, want 1", unpaused)
	}

	// Paused agent should now be active.
	st, _ = resilience.GetState(pausedKey)
	if st != StateActive {
		t.Errorf("paused agent state = %s, want active", st)
	}

	// Frozen agent should still be frozen.
	st, _ = resilience.GetState(frozenKey)
	if st != StateFrozen {
		t.Errorf("frozen agent state = %s, want frozen", st)
	}
}

func TestRouteRuntime_ProbationOneShotEnforcement(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
	}, false)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resilience.NowFunc = func() time.Time { return now }
	resilience.FreezeDuration = 5 * time.Hour

	frozenAt := now.Add(-6 * time.Hour)
	k := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	if err := resilience.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: k.Harness,
		Model:     k.Model,
		EventType: "frozen",
		Timestamp: frozenAt.UTC().Format(time.RFC3339),
		RelayID:   1,
	}); err != nil {
		t.Fatalf("AppendAgentStatus: %v", err)
	}

	sel := mustNextRouteSelection(t, rt, resilience, "")
	if !sel.Probation {
		t.Fatal("expected probation selection on first sync")
	}

	events, err := resilience.Store.GetAgentStatus(k.Harness, k.Model)
	if err != nil {
		t.Fatal(err)
	}
	foundProbation := false
	for _, e := range events {
		if e.EventType == "probation" {
			foundProbation = true
			break
		}
	}
	if !foundProbation {
		t.Fatal("expected probation event to be persisted")
	}

	// Without runOne resolving the state, a second sync must re-bench the
	// entry so it cannot be re-selected. With a single-entry route, that
	// means scheduler.Next() reports no selectable entries and the runtime
	// returns a routeSelectionError (the entry is exhausted+benched, not
	// strictly frozen, so AllFrozen reflects "no paused agent to wait on").
	if _, err := rt.next(runTask{}, resilience); err == nil {
		t.Fatal("expected error on second sync (probation entry re-benched), got selection")
	}

	probationCount := 0
	events, err = resilience.Store.GetAgentStatus(k.Harness, k.Model)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.EventType == "probation" {
			probationCount++
		}
	}
	if probationCount != 1 {
		t.Fatalf("expected exactly 1 probation event, got %d", probationCount)
	}
}

func TestRouteRuntime_ReasoningResolvedPauseWaitAndForceUnpauseUseVariantKey(t *testing.T) {
	rt, resilience := newReasoningRouteRuntimeOrDie(t, map[string][]string{
		"verify": {"cx:1"},
	})

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resilience.NowFunc = func() time.Time { return now }
	resilience.PauseDuration = time.Hour

	baseKey := ResilienceKey{Harness: "codex", Model: reasoningBaseModel}
	verifyKey := ResilienceKey{Harness: "codex", Model: reasoningVerifyModel}
	if err := resilience.PauseAgent(verifyKey, 7); err != nil {
		t.Fatalf("PauseAgent: %v", err)
	}
	if st, _ := resilience.GetState(baseKey); st != StateActive {
		t.Fatalf("base state(%s) = %s, want active before selection", baseKey, st)
	}

	_, err := rt.next(runTask{Assignee: "verify"}, resilience)
	if err == nil {
		t.Fatal("next() error = nil, want wait while reasoning-resolved variant is paused")
	}
	var routeErr *routeSelectionError
	if !errors.As(err, &routeErr) {
		t.Fatalf("error = %T, want *routeSelectionError", err)
	}
	if routeErr.AllFrozen {
		t.Fatalf("route error = %+v, want paused wait", routeErr)
	}
	if routeErr.Wait != time.Hour {
		t.Fatalf("wait = %v, want 1h from resolved variant pause", routeErr.Wait)
	}

	cleared, err := rt.forceUnpauseAll(resilience, 7, routeErr.RouteName, routeErr.EffectiveAssignee)
	if err != nil {
		t.Fatalf("forceUnpauseAll: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("cleared = %d, want 1 resolved variant key", cleared)
	}
	if st, _ := resilience.GetState(verifyKey); st != StateActive {
		t.Fatalf("state(%s) = %s, want active after force unpause", verifyKey, st)
	}

	selection := mustNextRouteSelection(t, rt, resilience, "verify")
	if got := agentRouteSpec(selection.Agent); got != "codex:"+reasoningVerifyModel {
		t.Fatalf("pick after unpause = %q, want codex:%s", got, reasoningVerifyModel)
	}
}

func TestRouteRuntime_ReasoningResolvedProbationUsesVariantKey(t *testing.T) {
	rt, resilience := newReasoningRouteRuntimeOrDie(t, map[string][]string{
		"verify": {"cx:1"},
	})

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resilience.NowFunc = func() time.Time { return now }
	resilience.FreezeDuration = 5 * time.Hour

	baseKey := ResilienceKey{Harness: "codex", Model: reasoningBaseModel}
	verifyKey := ResilienceKey{Harness: "codex", Model: reasoningVerifyModel}
	if err := resilience.Store.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: verifyKey.Harness,
		Model:     verifyKey.Model,
		EventType: "frozen",
		Timestamp: now.Add(-6 * time.Hour).UTC().Format(time.RFC3339),
		RelayID:   7,
	}); err != nil {
		t.Fatalf("AppendAgentStatus(frozen): %v", err)
	}

	scheduler := rt.schedulers["verify"]
	if scheduler == nil {
		t.Fatal("no scheduler for verify route")
	}
	entry := scheduler.EntryStates()[0]

	rt.syncRecoverySignals(scheduler, resilience, "verify")
	if entry.Benched || entry.Exhausted {
		t.Fatalf("entry after first sync = benched:%v exhausted:%v, want selectable probation probe", entry.Benched, entry.Exhausted)
	}

	variantEvents, err := resilience.Store.GetAgentStatus(verifyKey.Harness, verifyKey.Model)
	if err != nil {
		t.Fatal(err)
	}
	probationFound := false
	for _, event := range variantEvents {
		if event.EventType == "probation" {
			probationFound = true
			break
		}
	}
	if !probationFound {
		t.Fatal("expected probation event on reasoning-resolved variant key")
	}
	baseEvents, err := resilience.Store.GetAgentStatus(baseKey.Harness, baseKey.Model)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range baseEvents {
		if event.EventType == "probation" {
			t.Fatalf("unexpected probation event on base key: %+v", event)
		}
	}

	rt.syncRecoverySignals(scheduler, resilience, "verify")
	if !entry.Benched || !entry.Exhausted {
		t.Fatalf("entry after second sync = benched:%v exhausted:%v, want one-shot probation re-bench", entry.Benched, entry.Exhausted)
	}
}

func TestRouteRuntime_RecoveryPendingRoutesToRecovery(t *testing.T) {
	tests := []struct {
		name string
		rec  store.TryRecord
	}{
		{
			name: "dirty handoff",
			rec:  store.TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffRequested, DirtyHandoff: true},
		},
		{
			name: "handoff timeout",
			rec:  store.TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
				"default":  {"claude:opus-4.7"},
				"senior":   {"claude:sonnet-4.5"},
				"recovery": {"codex:gpt-5.5"},
			}, false)
			rt.store = newRouteRuntimeStore(t, tt.rec)

			sel := mustNextRouteSelection(t, rt, resilience, "senior", "lap-1")
			if sel.Route.Name != "recovery" || !sel.RecoveryForced {
				t.Fatalf("selection route=%q forced=%v, want forced recovery", sel.Route.Name, sel.RecoveryForced)
			}
			if sel.EffectiveAssignee != "recovery" {
				t.Fatalf("EffectiveAssignee = %q, want recovery", sel.EffectiveAssignee)
			}
			if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
				t.Fatalf("agent = %q, want codex:gpt-5.5", got)
			}
		})
	}
}

func TestRouteRuntime_RecoveryPendingFollowupUsesOriginalTrigger(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default":  {"claude:opus-4.7"},
		"junior":   {"claude:sonnet-4.5"},
		"recovery": {"codex:gpt-5.5"},
	}, false)
	rt.store = newRouteRuntimeStore(t, store.TryRecord{
		ID:                   1,
		RunID:                1,
		LapID:                "original",
		AttemptNumber:        1,
		Outcome:              reliability.OutcomeHandoffRequested,
		DirtyHandoff:         true,
		HandoffCreatedLapIDs: []string{"followup"},
	})

	sel := mustNextRouteSelection(t, rt, resilience, "junior", "followup")
	if sel.Route.Name != "recovery" || !sel.RecoveryForced {
		t.Fatalf("selection route=%q forced=%v, want forced recovery", sel.Route.Name, sel.RecoveryForced)
	}
	if sel.RecoveryStatus.TriggerLapID != "original" || !sel.RecoveryStatus.HandoffContinuationMatch {
		t.Fatalf("recovery status = %+v, want original continuation match", sel.RecoveryStatus)
	}
}

func TestRouteRuntime_OrdinaryFailedDoesNotForceRecovery(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default":  {"claude:opus-4.7"},
		"senior":   {"claude:sonnet-4.5"},
		"recovery": {"codex:gpt-5.5"},
	}, false)
	rt.store = newRouteRuntimeStore(t, store.TryRecord{
		ID:            1,
		RunID:         1,
		LapID:         "lap-1",
		AttemptNumber: 1,
		Outcome:       reliability.OutcomeFailed,
	})

	sel := mustNextRouteSelection(t, rt, resilience, "senior", "lap-1")
	if sel.Route.Name != "senior" || sel.RecoveryForced {
		t.Fatalf("selection route=%q forced=%v, want normal senior", sel.Route.Name, sel.RecoveryForced)
	}
	if sel.EffectiveAssignee != "senior" {
		t.Fatalf("EffectiveAssignee = %q, want senior", sel.EffectiveAssignee)
	}
}

func TestRouteRuntime_RecoveryStateSurvivesStoreReload(t *testing.T) {
	rallyDir, s := setupRouteRuntimeStore(t)
	mustAppendRouteTry(t, s, store.TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout})
	reloaded, err := store.NewStore(rallyDir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}

	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default":  {"claude:opus-4.7"},
		"senior":   {"claude:sonnet-4.5"},
		"recovery": {"codex:gpt-5.5"},
	}, false)
	rt.store = reloaded

	sel := mustNextRouteSelection(t, rt, resilience, "senior", "lap-1")
	if sel.Route.Name != "recovery" || !sel.RecoveryForced {
		t.Fatalf("selection route=%q forced=%v, want forced recovery after reload", sel.Route.Name, sel.RecoveryForced)
	}
}

func TestRouteRuntime_MissingRecoveryRouteWarnsAndFallsBack(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
		"senior":  {"claude:sonnet-4.5"},
	}, false)
	rt.store = newRouteRuntimeStore(t, store.TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout})

	sel := mustNextRouteSelection(t, rt, resilience, "senior", "lap-1")
	if sel.Route.Name != "senior" || sel.RecoveryForced {
		t.Fatalf("selection route=%q forced=%v, want normal senior fallback", sel.Route.Name, sel.RecoveryForced)
	}
	if !strings.Contains(sel.Route.Warning, "no recovery route is configured") {
		t.Fatalf("warning = %q, want missing recovery route warning", sel.Route.Warning)
	}
}

func TestRouteRuntime_OverridePrecedenceOverRecoveryRoute(t *testing.T) {
	rt, resilience := newOverrideRouteRuntimeOrDie(t, []string{"op:opencode-go/fancy-new-model"}, map[string][]string{
		"default":  {"claude:opus-4.7"},
		"senior":   {"claude:sonnet-4.5"},
		"recovery": {"codex:gpt-5.5"},
	}, false)
	rt.store = newRouteRuntimeStore(t, store.TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout})

	sel := mustNextRouteSelection(t, rt, resilience, "senior", "lap-1")
	if sel.Route.Source != "override" || !sel.RecoveryForced {
		t.Fatalf("selection source=%q forced=%v, want forced recovery with override route precedence", sel.Route.Source, sel.RecoveryForced)
	}
	if sel.EffectiveAssignee != "recovery" {
		t.Fatalf("EffectiveAssignee = %q, want recovery", sel.EffectiveAssignee)
	}
	if got := agentRouteSpec(sel.Agent); got != "opencode:opencode-go/fancy-new-model" {
		t.Fatalf("agent = %q, want override agent", got)
	}
}

func TestRouteRuntime_RecoveryCapFallsBackAndRaisesFlag(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default":  {"claude:opus-4.7"},
		"senior":   {"claude:sonnet-4.5"},
		"recovery": {"codex:gpt-5.5"},
	}, false)
	rt.store = newRouteRuntimeStore(t,
		store.TryRecord{ID: 1, RunID: 1, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"},
		store.TryRecord{ID: 2, RunID: 2, LapID: "lap-1", AttemptNumber: 1, Outcome: reliability.OutcomeHandoffTimeout, ResolvedRoute: "recovery"},
	)

	sel := mustNextRouteSelection(t, rt, resilience, "senior", "lap-1")
	if sel.Route.Name != "senior" || sel.RecoveryForced {
		t.Fatalf("selection route=%q forced=%v, want normal senior after cap", sel.Route.Name, sel.RecoveryForced)
	}
	if !sel.RecoveryCapHit {
		t.Fatal("RecoveryCapHit = false, want true")
	}
	if !strings.Contains(sel.Route.Warning, "needs_user") {
		t.Fatalf("warning = %q, want needs_user cap warning", sel.Route.Warning)
	}
}
