package runner

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/routing"
)

// TestBenchQuotaScope_ProviderGroupSpansHarnesses verifies that a provider quota
// scope benches every member across harnesses (a codex provider that also owns an
// opencode model) while leaving runners outside the provider untouched.
func TestBenchQuotaScope_ProviderGroupSpansHarnesses(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"codex:gpt-5.5", "opencode:openai/gpt-5.5"},
		"verify":  {"codex:gpt-5.4", "claude:opus-4.7"},
	}, false)

	idx := routing.NewProviderIndex()
	idx.Add("codex", "codex", "gpt-5.5", false)
	idx.Add("codex", "codex", "gpt-5.4", false)
	idx.Add("codex", "opencode", "openai/gpt-5.5", false)
	rt.providers = idx

	// The provider scope is what a usage limit on any member resolves to.
	scope := rt.quotaScope("codex", "gpt-5.5")
	if scope != "provider:codex" {
		t.Fatalf("quotaScope = %q, want provider:codex", scope)
	}

	now := resilience.NowFunc()
	resetAt := now.Add(3 * time.Hour)
	benched, err := rt.benchQuotaScope(resilience, scope, resetAt, 7, "", "")
	if err != nil {
		t.Fatalf("benchQuotaScope: %v", err)
	}
	if benched != 3 {
		t.Fatalf("benched count = %d, want 3 codex-provider members across harnesses", benched)
	}

	for _, k := range []ResilienceKey{
		{Harness: "codex", Model: "gpt-5.5"},
		{Harness: "codex", Model: "gpt-5.4"},
		{Harness: "opencode", Model: "openai/gpt-5.5"},
	} {
		if st, _ := resilience.GetState(k); st != StateBenched {
			t.Errorf("state(%s) = %s, want benched (provider member)", k, st)
		}
	}
	// claude is not in the provider; its harness-default scope differs.
	if st, _ := resilience.GetState(ResilienceKey{Harness: "claude", Model: "opus-4.7"}); st != StateActive {
		t.Errorf("claude state = %s, want active (outside provider)", st)
	}
}

// TestProviderDisabled_SidelinesEntriesAcrossLanes verifies an operator-disabled
// provider keeps its members out of rotation while leaving other runners
// selectable.
func TestProviderDisabled_SidelinesEntriesAcrossLanes(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
	}, false)

	idx := routing.NewProviderIndex()
	idx.Add("claude", "claude", "opus-4.7", true) // disabled
	rt.providers = idx

	// claude is disabled, so selection must skip to codex even though claude is
	// first in the lane.
	for i := 0; i < 3; i++ {
		sel := mustNextRouteSelection(t, rt, resilience, "")
		if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
			t.Fatalf("pick %d = %q, want codex:gpt-5.5 (claude disabled)", i+1, got)
		}
	}
}

// TestProviderDisabled_AllDisabledLaneTerminates verifies that a lane whose every
// member is disabled returns a clear terminal error (no wait, not AllFrozen).
func TestProviderDisabled_AllDisabledLaneTerminates(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "claude:sonnet-4.5"},
	}, false)

	idx := routing.NewProviderIndex()
	idx.Add("anthropic", "claude", "opus-4.7", true)
	idx.Add("anthropic", "claude", "sonnet-4.5", true)
	rt.providers = idx

	_, err := rt.next(runTask{}, resilience)
	if err == nil {
		t.Fatal("next() error = nil, want terminal error for all-disabled lane")
	}
	var routeErr *routeSelectionError
	if !errors.As(err, &routeErr) {
		t.Fatalf("error = %T, want *routeSelectionError", err)
	}
	if routeErr.AllFrozen {
		t.Fatalf("route error = %+v, want disabled terminal, not AllFrozen", routeErr)
	}
	if routeErr.Wait > 0 {
		t.Fatalf("route error wait = %v, want no wait for disabled lane", routeErr.Wait)
	}
	if !strings.Contains(routeErr.Error(), "disabled") {
		t.Fatalf("error = %q, want disabled message", routeErr.Error())
	}
}

// TestApplyProviders_WarnsOnDisabledProvider verifies a disabled provider with
// route members surfaces a single startup warning.
func TestApplyProviders_WarnsOnDisabledProvider(t *testing.T) {
	rt, _ := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
		"verify":  {"claude:opus-4.7"},
	}, false)

	idx := routing.NewProviderIndex()
	idx.Add("anthropic", "claude", "opus-4.7", true)
	rt.applyProviders(idx)

	var found int
	for _, w := range rt.Warnings() {
		if strings.Contains(w, "anthropic") && strings.Contains(w, "disabled") {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("disabled-provider warnings = %d, want exactly 1; warnings=%v", found, rt.Warnings())
	}
}

// TestProviderDisabled_StaysDisabledAfterForceUnpause verifies that an operator
// skip during a wait (forceUnpauseAll writes active events) does not re-enable a
// disabled provider: the next syncRecoverySignals re-benches its members.
func TestProviderDisabled_StaysDisabledAfterForceUnpause(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
	}, false)

	idx := routing.NewProviderIndex()
	idx.Add("anthropic", "claude", "opus-4.7", true)
	rt.providers = idx

	// Prime the scheduler so the disabled entry is benched.
	scheduler := rt.schedulers["default"]
	rt.syncRecoverySignals(scheduler, resilience, "")

	if _, err := rt.forceUnpauseAll(resilience, 1, "", ""); err != nil {
		t.Fatalf("forceUnpauseAll: %v", err)
	}

	// After a skip, selection must still avoid the disabled claude runner.
	sel := mustNextRouteSelection(t, rt, resilience, "")
	if got := agentRouteSpec(sel.Agent); got != "codex:gpt-5.5" {
		t.Fatalf("post-skip pick = %q, want codex:gpt-5.5 (claude still disabled)", got)
	}
}

// TestProviderDisabled_NotUnbenchedBySync guards the ordering in
// syncRecoverySignals: a disabled entry whose resilience state is Active must
// stay sidelined and never be un-benched by the StateActive arm.
func TestProviderDisabled_NotUnbenchedBySync(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7"},
	}, false)

	idx := routing.NewProviderIndex()
	idx.Add("anthropic", "claude", "opus-4.7", true)
	rt.providers = idx

	scheduler := rt.schedulers["default"]
	entry := scheduler.EntryStates()[0]

	// Multiple syncs must keep the disabled entry benched+exhausted.
	for i := 0; i < 3; i++ {
		rt.syncRecoverySignals(scheduler, resilience, "")
		if !entry.Benched || !entry.Exhausted {
			t.Fatalf("after sync %d: benched=%v exhausted=%v, want disabled entry sidelined", i+1, entry.Benched, entry.Exhausted)
		}
	}
}
