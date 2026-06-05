package relay

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/store"
)

func newRouteRuntimeHarness(t *testing.T) *Resilience {
	t.Helper()

	s := newTestStore(t, t.TempDir())
	r := NewResilience(s)
	r.PauseDuration = time.Hour
	r.NowFunc = func() time.Time {
		return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	}
	return r
}

func newResolvedRouteRuntimeOrDie(t *testing.T, routeSpecs map[string][]string, noBackend bool) (*routeRuntime, *Resilience) {
	t.Helper()

	rt, err := newResolvedRouteRuntime(routeSpecs, testResolver, noBackend, nil)
	if err != nil {
		t.Fatalf("newResolvedRouteRuntime() error = %v", err)
	}
	return rt, newRouteRuntimeHarness(t)
}

func newOverrideRouteRuntimeOrDie(t *testing.T, specs []string, routeSpecs map[string][]string, noBackend bool) (*routeRuntime, *Resilience) {
	t.Helper()

	rt, _, err := newOverrideRouteRuntime(specs, routeSpecs, testResolver, noBackend)
	if err != nil {
		t.Fatalf("newOverrideRouteRuntime() error = %v", err)
	}
	return rt, newRouteRuntimeHarness(t)
}

func mustNextRouteSelection(t *testing.T, rt *routeRuntime, resilience *Resilience, assignee string) routeSelection {
	t.Helper()

	selection, err := rt.next(runTask{Assignee: assignee}, resilience)
	if err != nil {
		t.Fatalf("next(%q) error = %v", assignee, err)
	}
	return selection
}

func TestRouteRuntime_CanonicalScenario1_NoQuotasRunUntilFailure(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5", "opencode:opencode-go/kimi-k2.6"},
	}, false)

	sel := mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "claude:opus-4.7" {
		t.Fatalf("pick 1 = %q, want claude:opus-4.7", got)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "claude:opus-4.7" {
		t.Fatalf("pick 2 = %q, want claude:opus-4.7", got)
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "opus-4.7"}, 1); err != nil {
		t.Fatalf("PauseAgent(claude) error = %v", err)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
		t.Fatalf("pick 3 = %q, want codex:gpt-5.5", got)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
		t.Fatalf("pick 4 = %q, want codex:gpt-5.5", got)
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "codex", Model: "gpt-5.5"}, 1); err != nil {
		t.Fatalf("PauseAgent(codex) error = %v", err)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "opencode:opencode-go/kimi-k2.6" {
		t.Fatalf("pick 5 = %q, want opencode:opencode-go/kimi-k2.6", got)
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "opencode", Model: "opencode-go/kimi-k2.6"}, 1); err != nil {
		t.Fatalf("PauseAgent(opencode) error = %v", err)
	}
	if err := resilience.UnpauseAgent(ResilienceKey{Harness: "claude", Model: "opus-4.7"}, 1); err != nil {
		t.Fatalf("UnpauseAgent(claude) error = %v", err)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "claude:opus-4.7" {
		t.Fatalf("pick 6 = %q, want claude:opus-4.7", got)
	}
}

func TestRouteRuntime_CanonicalScenario2_MixedQuotaThenNoQuotaFallback(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6"},
	}, false)

	want := []string{
		"claude:opus-4.7",
		"codex:gpt-5.5",
		"codex:gpt-5.5",
		"codex:gpt-5.5",
		"opencode:opencode-go/kimi-k2.6",
		"opencode:opencode-go/kimi-k2.6",
	}

	for i, spec := range want {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if got := agentRouteSpec(sel.Agent); got != spec {
			t.Fatalf("pick %d = %q, want %q", i+1, got, spec)
		}
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "opencode", Model: "opencode-go/kimi-k2.6"}, 1); err != nil {
		t.Fatalf("PauseAgent(opencode) error = %v", err)
	}

	sel := mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "claude:opus-4.7" {
		t.Fatalf("pick 7 = %q, want claude:opus-4.7", got)
	}
}

func TestRouteRuntime_CanonicalScenario3_AssigneeFallsBackToDefault(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
		"ROLEA":   {"codex:gpt-5.5"},
	}, false)

	roleA := mustNextRouteSelection(t, rt, resilience, "ROLEA")
	if roleA.Route.Name != "ROLEA" || roleA.Route.Source != "assignee" {
		t.Fatalf("ROLEA route = %+v, want assignee route ROLEA", roleA.Route)
	}
	if got := agentRouteSpec(roleA.Agent); got != "codex:gpt-5.5" {
		t.Fatalf("ROLEA pick = %q, want codex:gpt-5.5", got)
	}

	roleB := mustNextRouteSelection(t, rt, resilience, "ROLEB")
	if roleB.Route.Name != "default" || roleB.Route.Source != "default" {
		t.Fatalf("ROLEB route = %+v, want default fallback", roleB.Route)
	}
	if !strings.Contains(roleB.Route.Warning, "ROLEB") {
		t.Fatalf("ROLEB warning = %q, want unmatched assignee", roleB.Route.Warning)
	}
	if got := agentRouteSpec(roleB.Agent); got != "claude:opus-4.7" {
		t.Fatalf("ROLEB pick = %q, want claude:opus-4.7", got)
	}
}

func TestRouteRuntime_CanonicalScenario4_MissingDefaultErrorsForUnmatchedRole(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"ROLEA": {"claude:opus-4.7"},
	}, false)

	roleA := mustNextRouteSelection(t, rt, resilience, "ROLEA")
	if got := agentRouteSpec(roleA.Agent); got != "claude:opus-4.7" {
		t.Fatalf("ROLEA pick = %q, want claude:opus-4.7", got)
	}

	_, err := rt.next(runTask{Assignee: "ROLEB"}, resilience)
	if err == nil {
		t.Fatal("next(ROLEB) error = nil, want missing-default failure")
	}
	if !strings.Contains(err.Error(), "ROLEB") || !strings.Contains(err.Error(), "default") {
		t.Fatalf("error = %q, want unmatched role and default mentioned", err.Error())
	}
}

func TestRouteRuntime_CanonicalScenario5_OverrideIgnoresAssigneeForEntireRelay(t *testing.T) {
	rt, resilience := newOverrideRouteRuntimeOrDie(t, []string{"op:opencode-go/fancy-new-model"}, map[string][]string{
		"default": {"claude:opus-4.7"},
		"ROLEA":   {"codex:gpt-5.5"},
		"ROLEB":   {"gemini:gemini-2.5-pro"},
	}, false)

	for _, assignee := range []string{"ROLEA", "ROLEB", ""} {
		sel := mustNextRouteSelection(t, rt, resilience, assignee)
		if sel.Route.Source != "override" {
			t.Fatalf("assignee %q route source = %q, want override", assignee, sel.Route.Source)
		}
		if got := agentRouteSpec(sel.Agent); got != "opencode:opencode-go/fancy-new-model" {
			t.Fatalf("assignee %q pick = %q, want opencode:opencode-go/fancy-new-model", assignee, got)
		}
	}
}

func TestRouteRuntime_CanonicalScenario6_OverrideRoleReferenceAdvancesDefaultCursor(t *testing.T) {
	rt, resilience := newOverrideRouteRuntimeOrDie(t, []string{"op:opencode-go/fancy-new-model", "DEFAULT:1"}, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
		"ROLEA":   {"claude:sonnet-4.6"},
		"ROLEB":   {"gemini:gemini-2.5-pro"},
	}, false)

	sel := mustNextRouteSelection(t, rt, resilience, "ROLEA")
	if got := agentRouteSpec(sel.Agent); got != "opencode:opencode-go/fancy-new-model" {
		t.Fatalf("pick 1 = %q, want fancy override", got)
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "opencode", Model: "opencode-go/fancy-new-model"}, 1); err != nil {
		t.Fatalf("PauseAgent(opencode) error = %v", err)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "ROLEB")
	if got := agentRouteSpec(sel.Agent); got != "claude:opus-4.7" {
		t.Fatalf("pick 2 = %q, want first default entry", got)
	}

	if err := resilience.UnpauseAgent(ResilienceKey{Harness: "opencode", Model: "opencode-go/fancy-new-model"}, 1); err != nil {
		t.Fatalf("UnpauseAgent(opencode) error = %v", err)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "ROLEA")
	if got := agentRouteSpec(sel.Agent); got != "opencode:opencode-go/fancy-new-model" {
		t.Fatalf("pick 3 = %q, want fancy override", got)
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "opencode", Model: "opencode-go/fancy-new-model"}, 1); err != nil {
		t.Fatalf("PauseAgent(opencode) error = %v", err)
	}

	sel = mustNextRouteSelection(t, rt, resilience, "ROLEB")
	if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
		t.Fatalf("pick 4 = %q, want second default entry", got)
	}
}

func TestRouteRuntime_CanonicalScenario7_RangeQuotaWaitsWhenAllOthersPaused(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:3", "codex:gpt-5.5:3", "opencode:opencode-go/kimi-k2.6:1-5"},
	}, false)

	want := []string{
		"claude:opus-4.7",
		"claude:opus-4.7",
		"claude:opus-4.7",
		"codex:gpt-5.5",
		"codex:gpt-5.5",
		"codex:gpt-5.5",
		"opencode:opencode-go/kimi-k2.6",
		"claude:opus-4.7",
	}

	for i, spec := range want {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if got := agentRouteSpec(sel.Agent); got != spec {
			t.Fatalf("pick %d = %q, want %q", i+1, got, spec)
		}
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "opus-4.7"}, 1); err != nil {
		t.Fatalf("PauseAgent(claude) error = %v", err)
	}

	want = []string{
		"codex:gpt-5.5",
		"codex:gpt-5.5",
		"codex:gpt-5.5",
		"opencode:opencode-go/kimi-k2.6",
	}
	for i, spec := range want {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if got := agentRouteSpec(sel.Agent); got != spec {
			t.Fatalf("post-pause pick %d = %q, want %q", i+1, got, spec)
		}
	}

	if err := resilience.PauseAgent(ResilienceKey{Harness: "codex", Model: "gpt-5.5"}, 1); err != nil {
		t.Fatalf("PauseAgent(codex) error = %v", err)
	}

	for i := 0; i < 4; i++ {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if got := agentRouteSpec(sel.Agent); got != "opencode:opencode-go/kimi-k2.6" {
			t.Fatalf("extended kimi pick %d = %q, want opencode:opencode-go/kimi-k2.6", i+1, got)
		}
	}

	_, err := rt.next(runTask{}, resilience)
	if err == nil {
		t.Fatal("next() error = nil, want wait condition after kimi max is reached")
	}

	var routeErr *routeSelectionError
	if !strings.Contains(err.Error(), "all agents paused") {
		t.Fatalf("error = %q, want paused wait error", err.Error())
	}
	if ok := strings.Contains(err.Error(), "frozen"); ok {
		t.Fatalf("error = %q, want paused wait instead of frozen", err.Error())
	}
	if !strings.Contains(err.Error(), "paused") {
		t.Fatalf("error = %q, want paused message", err.Error())
	}
	if !errors.As(err, &routeErr) {
		t.Fatalf("error = %T, want *routeSelectionError", err)
	}
	if routeErr.AllFrozen {
		t.Fatalf("route error = %+v, want paused wait, not frozen", routeErr)
	}
	if routeErr.Wait <= 0 {
		t.Fatalf("route error wait = %v, want positive wait", routeErr.Wait)
	}
}

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

	unpaused, err := rt.forceUnpauseAll(resilience, 1)
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
	again, err := rt.forceUnpauseAll(resilience, 1)
	if err != nil {
		t.Fatalf("second forceUnpauseAll: %v", err)
	}
	if again != 0 {
		t.Errorf("second unpaused count = %d, want 0", again)
	}
}

func TestRouteRuntime_ActiveExhaustedEntryStaysAdvanced(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"op:glm:1", "cx:gpt-5:1"},
	}, false)

	first := mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(first.Agent); got != "opencode:glm" {
		t.Fatalf("pick 1 = %q, want opencode:glm", got)
	}

	first.Entry.Exhausted = true
	first.Entry.Benched = false

	second := mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(second.Agent); got != "codex:gpt-5" {
		t.Fatalf("pick 2 = %q, want codex:gpt-5 after exhausting active entry", got)
	}
}

func TestRouteRuntime_PausedExpiryResetsExhaustedEntry(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
	}, false)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	resilience.NowFunc = func() time.Time { return now }
	resilience.PauseDuration = time.Hour

	first := mustNextRouteSelection(t, rt, resilience, "")
	first.Entry.Exhausted = true
	first.Entry.Benched = false

	if err := resilience.PauseAgent(ResilienceKey{Harness: "claude", Model: "opus-4.7"}, 1); err != nil {
		t.Fatalf("PauseAgent(claude) error = %v", err)
	}

	now = now.Add(2 * time.Hour)

	second := mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(second.Agent); got != "claude:opus-4.7" {
		t.Fatalf("pick 2 = %q, want claude:opus-4.7 after pause expiry reset", got)
	}
	if second.Entry.Exhausted {
		t.Fatal("entry should be selectable again after pause expiry reset")
	}
}

func TestFormatMixLabel(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{"empty", "", "(empty)"},
		{"routes marker", relaySelectionModeRoutes, "configured routes"},
		{"override with specs", relaySelectionModeOverridePrefix + "cc ge op", "cc ge op"},
		{"override with quotas", relaySelectionModeOverridePrefix + "cc:1 ge:1", "cc:1 ge:1"},
		{"override bare", relaySelectionModeOverridePrefix, "(override)"},
		{"override only whitespace", relaySelectionModeOverridePrefix + "  ", "(override)"},
		{"legacy mix", "cc:1 cx:2", "cc:1 cx:2"},
		{"trims whitespace", "  cc cx  ", "cc cx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatMixLabel(tt.stored); got != tt.want {
				t.Errorf("FormatMixLabel(%q) = %q, want %q", tt.stored, got, tt.want)
			}
		})
	}
}

func TestRouteRuntime_NoBackendAlwaysUsesDefaultRoute(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"codex:gpt-5.5:1", "gemini:gemini-2.5-pro:1"},
		"SENIOR":  {"claude:opus-4.7"},
	}, true)

	want := []string{"codex:gpt-5.5", "gemini:gemini-2.5-pro", "codex:gpt-5.5"}
	for i, assignee := range []string{"SENIOR", "JUNIOR", ""} {
		sel := mustNextRouteSelection(t, rt, resilience, assignee)
		if sel.Route.Name != "default" || sel.Route.Source != "default" {
			t.Fatalf("assignee %q route = %+v, want default route", assignee, sel.Route)
		}
		if got := agentRouteSpec(sel.Agent); got != want[i] {
			t.Fatalf("assignee %q pick = %q, want %q", assignee, got, want[i])
		}
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

func appendEvent(t *testing.T, s *store.Store, key ResilienceKey, eventType string, relayID int) {
	t.Helper()
	if err := s.AppendAgentStatus(store.AgentStatusEvent{
		AgentType: key.Harness,
		Model:     key.Model,
		EventType: eventType,
		Timestamp: "2026-01-01T12:00:00Z",
		RelayID:   relayID,
	}); err != nil {
		t.Fatalf("AppendAgentStatus(%s): %v", eventType, err)
	}
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
	rt.syncRecoverySignals(scheduler, resilience)
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
	rt.syncRecoverySignals(scheduler, resilience)
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
	rt.syncRecoverySignals(scheduler, resilience)
	if !entry.Benched || !entry.Exhausted {
		t.Fatal("expected entry re-benched+exhausted when only Benched was clear")
	}
	// Fourth sync: true no-op because already Benched+Exhausted.
	entryBefore := *entry
	rt.syncRecoverySignals(scheduler, resilience)
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

	unpaused, err := rt.forceUnpauseAll(resilience, 1)
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

func TestRouteRuntime_SingleRunnerLaneWarns(t *testing.T) {
	rt, _ := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"solo": {"claude:opus-4.7"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "solo") || !strings.Contains(warnings[0], "single runner") {
		t.Fatalf("warning = %q, want single-runner warning for lane %q", warnings[0], "solo")
	}
}

func TestRouteRuntime_MultiRunnerLaneDoesNotWarn(t *testing.T) {
	rt, _ := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for multi-runner lane, got %d: %v", len(warnings), warnings)
	}
}

func TestRouteRuntime_SingleRunnerOverrideWarns(t *testing.T) {
	rt, _ := newOverrideRouteRuntimeOrDie(t, []string{"op:opencode-go/fancy-model"}, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for single-runner override, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "override") || !strings.Contains(warnings[0], "single runner") {
		t.Fatalf("warning = %q, want single-runner warning for override lane", warnings[0])
	}
}

func TestRouteRuntime_MixedLanesWarnsOnlySingleRunner(t *testing.T) {
	rt, _ := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
		"fragile": {"gemini:gemini-2.5-pro"},
	}, false)

	warnings := rt.Warnings()
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning (fragile lane only), got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "fragile") {
		t.Fatalf("warning = %q, want warning for %q lane", warnings[0], "fragile")
	}
}
