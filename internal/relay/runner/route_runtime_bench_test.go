package runner

import (
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/reliability"
)

func TestBenchResetDeadline(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := now.Add(5 * time.Hour)

	cases := []struct {
		name string
		ev   *reliability.FailureEvidence
		want time.Time
	}{
		{"nil evidence falls back to default", nil, now.Add(BenchDefaultDuration)},
		{"absolute reset preferred", &reliability.FailureEvidence{ResetAt: &at}, at},
		{"relative reset", &reliability.FailureEvidence{ResetAfter: 3 * time.Hour}, now.Add(3 * time.Hour)},
		{"no deadline falls back to default", &reliability.FailureEvidence{}, now.Add(BenchDefaultDuration)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := benchResetDeadline(tc.ev, now); !got.Equal(tc.want) {
				t.Fatalf("benchResetDeadline = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBenchQuotaScope_BenchesEveryKeyInScopeAcrossLanes verifies that a single
// usage_limit benches every distinct {Harness,Model} key that shares the
// exhausted quota scope, fanning out across all lanes, while leaving keys in
// other scopes untouched.
func TestBenchQuotaScope_BenchesEveryKeyInScopeAcrossLanes(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7", "codex:gpt-5.5"},
		"verify":  {"claude:sonnet-4.5", "antigravity:pro"},
	}, false)

	now := resilience.NowFunc()
	resetAt := now.Add(3 * time.Hour)

	// claude is a direct harness: its QuotaScope ignores the model, so both
	// claude entries (across both lanes) share scope "claude".
	benched, err := rt.benchQuotaScope(resilience, "claude", resetAt, 7, "", "")
	if err != nil {
		t.Fatalf("benchQuotaScope: %v", err)
	}
	if benched != 2 {
		t.Fatalf("benched count = %d, want 2 (claude:opus-4.7 + claude:sonnet-4.5)", benched)
	}

	benchedKeys := []ResilienceKey{
		{Harness: "claude", Model: "opus-4.7"},
		{Harness: "claude", Model: "sonnet-4.5"},
	}
	for _, k := range benchedKeys {
		if st, _ := resilience.GetState(k); st != StateBenched {
			t.Errorf("state(%s) = %s, want benched", k, st)
		}
	}

	// Keys in other quota scopes are not benched.
	for _, k := range []ResilienceKey{
		{Harness: "codex", Model: "gpt-5.5"},
		{Harness: "antigravity", Model: "pro"},
	} {
		if st, _ := resilience.GetState(k); st != StateActive {
			t.Errorf("state(%s) = %s, want active (out of scope)", k, st)
		}
	}
}

func TestBenchQuotaScope_OpencodeProviderScopes(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"opencode:zai-coding-plan/glm-5.2", "opencode:opencode-go/kimi"},
		"verify":  {"opencode:zai-coding-plan/glm-5.1", "claude:opus-4.7"},
	}, false)

	now := resilience.NowFunc()
	resetAt := now.Add(5 * time.Hour)

	benched, err := rt.benchQuotaScope(resilience, "opencode:zai-coding-plan", resetAt, 7, "", "")
	if err != nil {
		t.Fatalf("benchQuotaScope(zai): %v", err)
	}
	if benched != 2 {
		t.Fatalf("zai benched count = %d, want 2", benched)
	}

	for _, k := range []ResilienceKey{
		{Harness: "opencode", Model: "zai-coding-plan/glm-5.2"},
		{Harness: "opencode", Model: "zai-coding-plan/glm-5.1"},
	} {
		if st, _ := resilience.GetState(k); st != StateBenched {
			t.Errorf("state(%s) = %s, want benched", k, st)
		}
	}
	for _, k := range []ResilienceKey{
		{Harness: "opencode", Model: "opencode-go/kimi"},
		{Harness: "claude", Model: "opus-4.7"},
	} {
		if st, _ := resilience.GetState(k); st != StateActive {
			t.Errorf("state(%s) = %s, want active (different quota scope)", k, st)
		}
	}

	opencodeGoReset := now.Add(7 * 24 * time.Hour)
	benched, err = rt.benchQuotaScope(resilience, "opencode:opencode-go", opencodeGoReset, 8, "", "")
	if err != nil {
		t.Fatalf("benchQuotaScope(opencode-go): %v", err)
	}
	if benched != 1 {
		t.Fatalf("opencode-go benched count = %d, want 1", benched)
	}
	if st, _ := resilience.GetState(ResilienceKey{Harness: "opencode", Model: "opencode-go/kimi"}); st != StateBenched {
		t.Fatalf("opencode-go state = %s, want benched", st)
	}
}

func TestRouteRuntime_ReasoningResolvedQuotaBenchUsesVariantKey(t *testing.T) {
	rt, resilience := newReasoningRouteRuntimeOrDie(t, map[string][]string{
		"verify": {"cx:1", "cc:sonnet:1"},
		"junior": {"cx:1"},
	})

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := now
	resilience.NowFunc = func() time.Time { return clock }

	selection := mustNextRouteSelection(t, rt, resilience, "verify")
	if got := agentRouteSpec(selection.Agent); got != "codex:"+reasoningVerifyModel {
		t.Fatalf("initial pick = %q, want codex:%s", got, reasoningVerifyModel)
	}

	resetAt := clock.Add(3 * time.Hour)
	benched, err := rt.benchQuotaScope(resilience, "codex", resetAt, 7, selection.Route.Name, selection.EffectiveAssignee)
	if err != nil {
		t.Fatalf("benchQuotaScope: %v", err)
	}
	if benched != 2 {
		t.Fatalf("benched count = %d, want 2 reasoning-resolved codex variants", benched)
	}

	baseKey := ResilienceKey{Harness: "codex", Model: reasoningBaseModel}
	verifyKey := ResilienceKey{Harness: "codex", Model: reasoningVerifyModel}
	juniorKey := ResilienceKey{Harness: "codex", Model: reasoningJuniorModel}
	for _, key := range []ResilienceKey{verifyKey, juniorKey} {
		if st, _ := resilience.GetState(key); st != StateBenched {
			t.Fatalf("state(%s) = %s, want benched", key, st)
		}
	}
	if st, _ := resilience.GetState(baseKey); st != StateActive {
		t.Fatalf("base state(%s) = %s, want active; bench must target resolved variant key", baseKey, st)
	}

	// The verify codex entry stays out of rotation while the resolved variant
	// key is benched even though the base codex key has no resilience event.
	selection = mustNextRouteSelection(t, rt, resilience, "verify")
	if got := agentRouteSpec(selection.Agent); got != "claude:sonnet" {
		t.Fatalf("pre-reset pick = %q, want claude:sonnet while codex variant is benched", got)
	}

	clock = resetAt.Add(time.Minute)
	selection = mustNextRouteSelection(t, rt, resilience, "verify")
	if got := agentRouteSpec(selection.Agent); got != "codex:"+reasoningVerifyModel {
		t.Fatalf("post-reset pick = %q, want codex:%s after resolved variant bench clears", got, reasoningVerifyModel)
	}
}

// TestForceUnpauseAll_ClearsBenchOnSkip verifies that an operator skip during
// the wait clears benched keys (writing an active event) so the lane retries
// immediately instead of serving out the reset window.
func TestForceUnpauseAll_ClearsBenchOnSkip(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus-4.7:1", "codex:gpt-5.5:1"},
	}, false)

	now := resilience.NowFunc()
	benchedKey := ResilienceKey{Harness: "claude", Model: "opus-4.7"}
	pausedKey := ResilienceKey{Harness: "codex", Model: "gpt-5.5"}
	if err := resilience.BenchAgent(benchedKey, now.Add(5*time.Hour), "claude", 1); err != nil {
		t.Fatalf("BenchAgent: %v", err)
	}
	if err := resilience.PauseAgent(pausedKey, 1); err != nil {
		t.Fatalf("PauseAgent: %v", err)
	}

	cleared, err := rt.forceUnpauseAll(resilience, 1, "", "")
	if err != nil {
		t.Fatalf("forceUnpauseAll: %v", err)
	}
	if cleared != 2 {
		t.Errorf("cleared count = %d, want 2 (benched + paused)", cleared)
	}
	if st, _ := resilience.GetState(benchedKey); st != StateActive {
		t.Errorf("benched key state = %s, want active after skip", st)
	}
	if st, _ := resilience.GetState(pausedKey); st != StateActive {
		t.Errorf("paused key state = %s, want active after skip", st)
	}
}
