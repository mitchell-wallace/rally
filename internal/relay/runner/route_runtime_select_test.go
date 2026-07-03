package runner

import (
	"errors"
	"strings"
	"testing"
	"time"
)

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
		"ROLEB":   {"antigravity:pro"},
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
		"ROLEB":   {"antigravity:pro"},
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

func TestRouteRuntime_NoBackendAlwaysUsesDefaultRoute(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"codex:gpt-5.5:1", "antigravity:pro:1"},
		"SENIOR":  {"claude:opus-4.7"},
	}, true)

	want := []string{"codex:gpt-5.5", "antigravity:pro", "codex:gpt-5.5"}
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

// TestSyncRecoverySignals_BenchedEntryKeptOutOfRotation verifies that a benched
// key is not selected by Next(): syncRecoverySignals benches the entry every
// cycle while the reset deadline is in the future.
func TestSyncRecoverySignals_BenchedEntryKeptOutOfRotation(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:1"},
	}, false)

	now := resilience.NowFunc()
	claudeKey := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	if err := resilience.BenchAgent(claudeKey, now.Add(3*time.Hour), "claude", 1); err != nil {
		t.Fatalf("BenchAgent: %v", err)
	}

	// claude is benched, so the only selectable runner is codex.
	sel := mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
		t.Fatalf("pick = %q, want codex:gpt-5.5 (claude benched out of rotation)", got)
	}
}

// TestSelectionWaitError_AllBenchedLaneWaitsNotFrozen verifies that a lane whose
// every key is benched with a future reset returns a positive wait derived from
// the earliest reset_at, rather than falling through to AllFrozen.
func TestSelectionWaitError_AllBenchedLaneWaitsNotFrozen(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "claude:sonnet-4.5:1"},
	}, false)

	now := resilience.NowFunc()
	// Two distinct reset deadlines; the wait must follow the earliest (2h).
	if err := resilience.BenchAgent(ResilienceKey{Harness: "claude", Model: "opus-4.7"}, now.Add(5*time.Hour), "claude", 1); err != nil {
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
