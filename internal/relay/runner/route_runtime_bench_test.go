package runner

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mitchell-wallace/rally/internal/reliability"
	"github.com/mitchell-wallace/rally/internal/store"
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

func TestApplyRunOutcomeToResilience_AuthOrProxyBenchesQuotaScope(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"antigravity:opus", "antigravity:flash", "codex:gpt-5.5"},
	}, false)

	selection := mustNextRouteSelection(t, rt, resilience, "")
	if selection.Agent.Harness != "antigravity" {
		t.Fatalf("selected harness = %q, want antigravity", selection.Agent.Harness)
	}

	relay := &store.RelayRecord{ID: 42}
	var log bytes.Buffer
	before := time.Now().UTC()
	err := (&Runner{}).applyRunOutcomeToResilience(relay, 0, selection, runOutcome{
		Success:      false,
		Category:     reliability.CategoryAuthOrProxy,
		FailureClass: reliability.FailureAgent,
	}, rt, resilience, &log)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()

	for _, key := range []ResilienceKey{
		{Harness: "antigravity", Model: "opus"},
		{Harness: "antigravity", Model: "flash"},
	} {
		events, err := resilience.Store.GetAgentStatus(key.Harness, key.Model)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatalf("events(%s) = %d, want 1", key, len(events))
		}
		event := events[0]
		if event.Reason != authBenchReason {
			t.Fatalf("reason(%s) = %q, want %q", key, event.Reason, authBenchReason)
		}
		resetAt, err := time.Parse(time.RFC3339, event.ResetAt)
		if err != nil {
			t.Fatalf("reset_at(%s) parse: %v", key, err)
		}
		if resetAt.Before(before.Add(authBenchRetryInterval).Add(-2*time.Second)) || resetAt.After(after.Add(authBenchRetryInterval).Add(2*time.Second)) {
			t.Fatalf("reset_at(%s) = %s, want about 1h from now", key, resetAt.Format(time.RFC3339))
		}
	}
	if st, _ := resilience.GetState(ResilienceKey{Harness: "codex", Model: "gpt-5.5"}); st != StateActive {
		t.Fatalf("codex state = %s, want active", st)
	}

	gotLog := log.String()
	if !strings.Contains(gotLog, `benched quota scope "antigravity"`) || !strings.Contains(gotLog, "harness not authenticated") {
		t.Fatalf("log = %q, want auth bench line", gotLog)
	}
}

func TestApplyRunOutcomeToResilience_UsageLimitReasonUnchanged(t *testing.T) {
	rt, resilience := newResolvedRouteRuntimeOrDie(t, map[string][]string{
		"default": {"claude:opus"},
	}, false)

	selection := mustNextRouteSelection(t, rt, resilience, "")
	resetAfter := 2 * time.Hour
	err := (&Runner{}).applyRunOutcomeToResilience(&store.RelayRecord{ID: 43}, 0, selection, runOutcome{
		Success:       false,
		Category:      reliability.CategoryUsageLimit,
		FailureClass:  reliability.FailureAgent,
		ResetEvidence: &reliability.FailureEvidence{ResetAfter: resetAfter},
	}, rt, resilience, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	events, err := resilience.Store.GetAgentStatus("claude", "opus")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Reason != "usage limit reached" {
		t.Fatalf("reason = %q, want usage limit reached", events[0].Reason)
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
